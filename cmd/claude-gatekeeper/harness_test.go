package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jim80net/claude-gatekeeper/internal/adapter"
	"github.com/jim80net/gatekeeper-core/canonical"
)

// writeHomeConfig sets HOME to a temp dir containing ~/.claude/gatekeeper.toml
// with the given content.
func writeHomeConfig(t *testing.T, content string) {
	t.Helper()
	homeDir := t.TempDir()
	claudeDir := filepath.Join(homeDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "gatekeeper.toml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	origHome := os.Getenv("HOME")
	t.Cleanup(func() { os.Setenv("HOME", origHome) })
	os.Setenv("HOME", homeDir)
	// Ensure XDG does not shadow the ~/.claude config during the test.
	origXDG, hadXDG := os.LookupEnv("XDG_CONFIG_HOME")
	os.Unsetenv("XDG_CONFIG_HOME")
	t.Cleanup(func() {
		if hadXDG {
			os.Setenv("XDG_CONFIG_HOME", origXDG)
		}
	})
}

// writeHomeShippedConfig installs the ACTUAL shipped gatekeeper.toml into the
// test HOME, so tests exercise the real rules instead of a hand-copied fixture
// that can silently drift from the shipped policy.
func writeHomeShippedConfig(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile("../../gatekeeper.toml")
	if err != nil {
		t.Fatalf("reading shipped gatekeeper.toml: %v", err)
	}
	writeHomeConfig(t, string(data))
}

// TestHarnessSelectionGrokDeny confirms --harness grok emits grok-native deny
// wire (exit 2, {"decision":"deny"}) for a push-to-main, routed end-to-end
// through run() against the SHIPPED rules.
func TestHarnessSelectionGrokDeny(t *testing.T) {
	writeHomeShippedConfig(t)
	// grok's real hook wire (camelCase, toolName "Shell", event "pre_tool_use"),
	// live-verified 2026-07-03.
	stdin := strings.NewReader(`{"toolName":"Shell","toolInput":{"command":"git push origin main"},"hookEventName":"pre_tool_use","cwd":"/tmp"}`)
	var stdout bytes.Buffer

	code := run(stdin, &stdout, []string{"--harness", "grok"})
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (grok explicit deny)", code)
	}
	if !strings.Contains(stdout.String(), `"decision":"deny"`) {
		t.Errorf("stdout = %q, want grok deny wire", stdout.String())
	}
}

// TestShippedForcePushRule is the golden policy fixture for issue #33. It uses
// the shipped config so positional or token-boundary regressions cannot hide in
// a hand-copied test regex.
func TestShippedForcePushRule(t *testing.T) {
	writeHomeShippedConfig(t)
	tests := []struct {
		name         string
		command      string
		wantDecision string
	}{
		{"force flag after refs", "git push origin feature --force", `"permissionDecision":"deny"`},
		{"force flag before refs", "git push --force origin feature", `"permissionDecision":"deny"`},
		{"bundled short force flag", "git push origin feature -uf", `"permissionDecision":"deny"`},
		{"force with lease", "git push origin feature --force-with-lease", `"permissionDecision":"deny"`},
		{"safe push", "git push origin feature", `"permissionDecision":"allow"`},
		{"force text in commit message", "git commit -m 'use --force later'", `"permissionDecision":"allow"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			code := run(strings.NewReader(hookJSON("Bash", tc.command)), &stdout, nil)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0", code)
			}
			if !strings.Contains(stdout.String(), tc.wantDecision) {
				t.Errorf("command %q: stdout = %q, want %s", tc.command, stdout.String(), tc.wantDecision)
			}
		})
	}
}

// TestOnErrorMatrix drives every error class through each harness under both
// on_error postures and asserts the harness-correct abstain vs deny encoding.
func TestOnErrorMatrix(t *testing.T) {
	const (
		validBash  = `{"tool_name":"Bash","tool_input":{"command":"git status"},"cwd":"/tmp"}`
		grokBash   = `{"toolName":"Shell","toolInput":{"command":"git status"},"hookEventName":"pre_tool_use","cwd":"/tmp"}`
		malformed  = `{not json}`
		badRegex   = "on_error = \"deny\"\n[[rules]]\ntool='Bash'\ninput='[unclosed'\ndecision=\"allow\"\nreason=\"x\"\n"
		abstainCfg = "on_error = \"abstain\"\n"
		denyCfg    = "on_error = \"deny\"\n"
		junkCfg    = "this is = = not valid toml ["
	)

	// assertion helpers per outcome
	type outcome int
	const (
		abstain outcome = iota // no output, native exit (0 for claude/codex, 1 for grok fail-open)
		deny                   // harness deny wire
	)

	cases := []struct {
		name    string
		harness string
		config  string
		stdin   string
		want    outcome
	}{
		// --- malformed stdin ---
		{"claude/malformed/abstain", "claude", abstainCfg, malformed, abstain},
		{"codex/malformed/abstain", "codex", abstainCfg, malformed, abstain},
		{"grok/malformed/abstain", "grok", abstainCfg, malformed, abstain},
		{"claude/malformed/deny", "claude", denyCfg, malformed, deny},
		{"codex/malformed/deny", "codex", denyCfg, malformed, deny},
		{"grok/malformed/deny", "grok", denyCfg, malformed, deny},

		// --- engine compile error (bad rule regex), on_error=deny ---
		{"claude/badregex/deny", "claude", badRegex, validBash, deny},
		{"grok/badregex/deny", "grok", badRegex, grokBash, deny},

		// --- unparseable config: on_error itself is unreadable -> safe abstain ---
		{"claude/junkconfig/abstain", "claude", junkCfg, validBash, abstain},
		{"grok/junkconfig/abstain", "grok", junkCfg, grokBash, abstain},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeHomeConfig(t, tc.config)
			var stdout bytes.Buffer
			code := run(strings.NewReader(tc.stdin), &stdout, []string{"--harness", tc.harness})
			out := stdout.String()

			switch tc.want {
			case abstain:
				if out != "" {
					t.Errorf("abstain: want empty stdout, got %q", out)
				}
				wantCode := 0
				if tc.harness == "grok" {
					wantCode = 1 // grok abstain routes through fail-open (exit 1)
				}
				if code != wantCode {
					t.Errorf("abstain: exit = %d, want %d", code, wantCode)
				}
				// Never assert an allow on the abstain/error path.
				if strings.Contains(out, "allow") {
					t.Errorf("abstain path emitted allow: %q", out)
				}
			case deny:
				if tc.harness == "grok" {
					if code != 2 || !strings.Contains(out, `"decision":"deny"`) {
						t.Errorf("grok deny: exit=%d out=%q", code, out)
					}
				} else {
					if code != 0 || !strings.Contains(out, `"permissionDecision":"deny"`) {
						t.Errorf("%s deny: exit=%d out=%q", tc.harness, code, out)
					}
				}
			}
		})
	}
}

// fakeAdapter panics in ParseInput to exercise runHook's panic-recover path.
type fakeAdapter struct {
	encoded *canonical.Verdict
}

func (f *fakeAdapter) Name() string { return "fake" }
func (f *fakeAdapter) ParseInput(io.Reader) (*canonical.ToolCall, error) {
	panic("boom")
}
func (f *fakeAdapter) Encode(_ io.Writer, v canonical.Verdict) (int, error) {
	f.encoded = &v
	return 0, nil
}

// TestPanicRecover confirms a panic in the hook pipeline is recovered and the
// on_error verdict (abstain by default, since the panic occurs before config
// load) is emitted through the adapter rather than crashing the process.
func TestPanicRecover(t *testing.T) {
	var _ adapter.Adapter = (*fakeAdapter)(nil) // compile-time interface check
	f := &fakeAdapter{}
	var stdout bytes.Buffer

	code := runHook(strings.NewReader("{}"), &stdout, f, false)

	if f.encoded == nil {
		t.Fatal("panic was not recovered — Encode never called")
	}
	if f.encoded.Decision != canonical.Abstain {
		t.Errorf("recovered verdict = %s, want abstain (default posture before config load)", f.encoded.Decision)
	}
	if code != 0 {
		t.Errorf("exit = %d, want 0 (fake abstain)", code)
	}
}
