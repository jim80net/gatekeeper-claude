// claude-gatekeeper is a PreToolUse permission hook for coding agents
// (Claude Code, OpenAI Codex, xAI grok). The product is "agent-gatekeeper";
// the binary/repo name is retained for install compatibility.
//
// Default mode (no subcommand): reads the harness's hook JSON from stdin,
// evaluates the shared TOML rules, and writes a permission decision on the
// harness-native wire. The target harness is chosen by --harness
// (claude|codex|grok), the GATEKEEPER_HARNESS env var, or defaults to claude.
//
// On any gatekeeper error (unparseable stdin, missing/unparseable config, bad
// rule regex, evaluate error, panic) the on_error policy decides the verdict:
// "abstain" (default) emits no decision so the harness's native permission
// system decides; "deny" emits an explicit deny. A clean "no rule matched"
// always abstains.
//
// Subcommands:
//
//	migrate   Convert settings.json permissions to gatekeeper TOML.
//	setup     Register the hook for a harness (--harness claude|codex|grok).
//	uninstall Remove the Claude hook registration.
//	doctor    Inventory live gatekeeper hook surfaces and report drift.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jim80net/claude-gatekeeper/internal/adapter"
	"github.com/jim80net/claude-gatekeeper/internal/inventory"
	"github.com/jim80net/claude-gatekeeper/internal/migrate"
	"github.com/jim80net/claude-gatekeeper/internal/setup"
	"github.com/jim80net/gatekeeper-core/canonical"
	"github.com/jim80net/gatekeeper-core/config"
	"github.com/jim80net/gatekeeper-core/engine"
)

var version = "dev"

func main() {
	os.Exit(run(os.Stdin, os.Stdout, os.Args[1:]))
}

func run(stdin io.Reader, stdout io.Writer, args []string) int {
	// Check for subcommands before flag parsing.
	if len(args) > 0 {
		switch args[0] {
		case "migrate":
			return runMigrate(args[1:])
		case "setup":
			return runSetup(args[1:])
		case "uninstall":
			return runUninstall()
		case "doctor", "inventory":
			return runDoctor(stdout, args[1:])
		case "version":
			fmt.Fprintf(os.Stderr, "claude-gatekeeper %s\n", version)
			return 0
		}
	}

	// Hook mode flags.
	fs := flag.NewFlagSet("claude-gatekeeper", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	debug := fs.Bool("debug", false, "Enable debug output to stderr")
	showVersion := fs.Bool("version", false, "Show version")
	harness := fs.String("harness", "", "Target harness: claude|codex|grok (default claude)")
	if err := fs.Parse(args); err != nil {
		return 0 // abstain on flag errors
	}

	if *showVersion {
		fmt.Fprintf(os.Stderr, "claude-gatekeeper %s\n", version)
		return 0
	}

	canonical.DebugEnabled = *debug

	// Select the harness adapter: flag > env > default (claude).
	harnessName := *harness
	if harnessName == "" {
		harnessName = os.Getenv("GATEKEEPER_HARNESS")
	}
	ad, err := adapter.For(harnessName)
	if err != nil {
		// Unknown harness: abstain rather than assert a verdict on the wrong wire.
		canonical.Debugf("adapter selection: %v", err)
		return 0
	}

	// Auto-install default config on first run (best-effort, back-compat).
	installDefaultConfig()

	return runHook(stdin, stdout, ad, *debug)
}

func runDoctor(stdout io.Writer, args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOutput := fs.Bool("json", false, "Emit machine-readable JSON")
	expectedBinary := fs.String("expected-binary", "", "Expected binary path (default: this executable)")
	expectedVersion := fs.String("expected-version", version, "Expected version stamp")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *expectedBinary == "" {
		if exe, err := os.Executable(); err == nil {
			*expectedBinary = exe
		}
	}
	report, err := inventory.Collect(inventory.Options{ExpectedBinary: *expectedBinary, ExpectedVersion: *expectedVersion})
	if err != nil {
		fmt.Fprintf(os.Stderr, "doctor: %v\n", err)
		return 2
	}
	if *jsonOutput {
		if err := inventory.WriteJSON(stdout, report); err != nil {
			fmt.Fprintf(os.Stderr, "doctor: %v\n", err)
			return 2
		}
	} else {
		inventory.WriteTable(stdout, report)
	}
	if !report.OK {
		return 1
	}
	return 0
}

// runHook parses the hook input, evaluates the rules, and encodes the verdict
// on the adapter's wire. Every error path applies the on_error posture; a
// recovered panic does too.
func runHook(stdin io.Reader, stdout io.Writer, ad adapter.Adapter, debug bool) (code int) {
	// onError starts at the safe default (abstain) and is upgraded once the
	// config's on_error knob is known. The deferred recover uses whatever the
	// current value is at panic time.
	onError := canonical.Abstain
	defer func() {
		if r := recover(); r != nil {
			canonical.Debugf("panic recovered: %v", r)
			code = emit(stdout, ad, errVerdict(onError, fmt.Sprintf("panic: %v", r)))
		}
	}()

	// Parse hook input from stdin.
	tc, err := ad.ParseInput(stdin)
	if err != nil {
		canonical.Debugf("error reading input: %v", err)
		// No cwd is available on a parse failure; use the global-only posture.
		onError = config.GlobalOnError()
		return emit(stdout, ad, errVerdict(onError, "unparseable hook input"))
	}

	// Only handle PreToolUse events; other events abstain (clean, not an error).
	if tc.EventName != "" && tc.EventName != "PreToolUse" {
		canonical.Debugf("ignoring event: %s", tc.EventName)
		return emit(stdout, ad, canonical.Verdict{Decision: canonical.Abstain})
	}

	// Load config (global + project overlay from the tool call's cwd).
	cfg, err := config.Load(tc.CWD)
	if err != nil {
		canonical.Debugf("error loading config: %v", err)
		// The full load could not be trusted; fall back to the global-only
		// posture (which is Abstain if the global config is itself the problem).
		onError = config.GlobalOnError()
		return emit(stdout, ad, errVerdict(onError, "config load error"))
	}
	onError = cfg.OnErrorDecision()

	// Compile the engine.
	eng, err := engine.New(cfg, debug)
	if err != nil {
		canonical.Debugf("error creating engine: %v", err)
		return emit(stdout, ad, errVerdict(onError, "engine compile error"))
	}

	// Evaluate.
	v, err := eng.Evaluate(tc)
	if err != nil {
		canonical.Debugf("error evaluating: %v", err)
		return emit(stdout, ad, errVerdict(onError, "evaluate error"))
	}

	return emit(stdout, ad, v)
}

// emit encodes the verdict on the adapter's wire and returns the exit code.
func emit(w io.Writer, ad adapter.Adapter, v canonical.Verdict) int {
	code, err := ad.Encode(w, v)
	if err != nil {
		canonical.Debugf("error encoding output: %v", err)
	}
	return code
}

// errVerdict builds the verdict to emit on a gatekeeper error, per the on_error
// posture. Abstain carries no reason (nothing is written for it anyway).
func errVerdict(d canonical.Decision, ctx string) canonical.Verdict {
	if d == canonical.Abstain {
		return canonical.Verdict{Decision: canonical.Abstain}
	}
	return canonical.Verdict{Decision: d, Reason: "gatekeeper error: " + ctx}
}

// installDefaultConfig writes the default gatekeeper.toml to the back-compat
// global path on first run, best-effort.
func installDefaultConfig() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return
	}
	templatePath := filepath.Join(filepath.Dir(resolved), "..", "gatekeeper.toml")
	if err := config.EnsureGlobalConfig(templatePath); err != nil {
		canonical.Debugf("auto-config: %v", err)
	}
}

func runSetup(args []string) int {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	binaryPath := fs.String("binary", "", "Absolute path to the installed binary (auto-detected if omitted)")
	harness := fs.String("harness", "claude", "Target harness: claude|codex|grok")
	projectDir := fs.String("project-dir", "", "Codex hook location: empty = global ~/.codex/hooks.json (default); a path = that project's .codex/hooks.json")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	bin := *binaryPath
	if bin == "" {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: cannot determine binary path: %v\n", err)
			return 1
		}
		bin = exe
	}

	var err error
	switch *harness {
	case "claude":
		err = setup.Install(bin)
	case "grok":
		err = setup.InstallGrok(bin)
	case "codex":
		err = setup.InstallCodex(bin, *projectDir)
	default:
		fmt.Fprintf(os.Stderr, "Error: unknown harness %q (want claude|codex|grok)\n", *harness)
		return 1
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

func runUninstall() int {
	if err := setup.Uninstall(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}

func runMigrate(args []string) int {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	fs.SetOutput(os.Stderr)
	settingsPath := fs.String("settings", "", "Path to settings.json (auto-detected if omitted)")
	outputPath := fs.String("output", "", "Output path for gatekeeper.toml (default: ~/.claude/gatekeeper.toml)")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	if err := migrate.Run(*settingsPath, *outputPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}
