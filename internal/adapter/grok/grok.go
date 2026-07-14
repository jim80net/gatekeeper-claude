// Package grok implements the adapter for the xAI grok CLI PreToolUse hook.
//
// Grok's blocking-hook wire is grok-native, NOT Claude's hookSpecificOutput
// shape (design §2.3b, from the grok 0.2.82 binary's embedded 10-hooks.md):
//
//   - deny  -> {"decision":"deny","reason":"..."} on stdout, exit 2
//   - allow -> {"decision":"allow"} on stdout, exit 0
//   - grok fail-opens on any hook ERROR ("a hook that crashes will not block
//     the tool") — the command proceeds and grok's native permission layer
//     (--allow/--deny + always-approve) decides.
//
// ABSTAIN on grok (design §4.3, requirement #2, open question Q6). Grok's
// blocking-hook contract has no first-class "defer/no-opinion" affordance:
// only allow (exit 0) and deny (exit 2). Exit 0 may be read as an authoritative
// ALLOW, which the gatekeeper must never assert on its abstain path. So abstain
// is routed through grok's documented FAIL-OPEN path: emit nothing and exit
// with a non-zero, non-deny code (exitAbstain), which grok treats as a hook
// error and fail-opens, handing the decision to its native layer. This is the
// faithful "no verdict asserted" encoding.
//
// SEAM (Q6): whether grok treats a silent exit-0 as allow vs defer is
// UNVERIFIED pending a live probe. If the probe shows exit-0-silent already
// defers to native, exitAbstain can be set to 0. It is deliberately non-zero
// here so no allow is asserted before Q6 resolves. Change exitAbstain (and its
// test) once the probe answers.
//
// Q1 — LIVE-VERIFIED PASS (probe 2026-07-03, grok 0.2.82). In an isolated
// sandbox under --permission-mode bypassPermissions (grok full-auto), a global
// ~/.grok/hooks/ PreToolUse hook emitting this adapter's grok-native
// {"decision":"deny"} + exit 2 BLOCKED a canary command (file never created),
// while an abstaining call (exit 1, no output) let a control command run. So
// this adapter is a real in-harness hard control on auto-approve grok agents:
// grok evaluates PreToolUse hooks before the permission system, regardless of
// permission mode. (grok's separate settings-layer --deny list is NOT enforced
// under --always-approve — the hook is; see README.)
package grok

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/jim80net/gatekeeper-core/canonical"
)

// Grok exit codes for blocking hooks (design §2.3b).
const (
	exitAllow = 0 // {"decision":"allow"} / success
	exitDeny  = 2 // {"decision":"deny"} — explicit deny
	// exitAbstain triggers grok's fail-open-on-error path so NO verdict is
	// asserted (not an allow, not a deny). Non-zero and != exitDeny. See Q6.
	exitAbstain = 1
)

// toolAliases maps grok-native tool names onto the canonical taxonomy.
// "Shell" is from the 0.2.82 live hook capture. The remaining native names are
// pinned by the 0.2.101 shipping hook guide, which says toolName contains the
// real tool name and publishes these Claude-to-Grok matcher aliases.
// Unmapped names pass through unchanged.
var toolAliases = map[string]string{
	"Shell":                canonical.ToolBash, // live capture, grok 0.2.82
	"run_terminal_command": canonical.ToolBash,
	"search_replace":       canonical.ToolEdit,
	"write":                canonical.ToolWrite,
	"read_file":            canonical.ToolRead,
	"grep":                 canonical.ToolGrep,
	"list_dir":             canonical.ToolGlob,
	"web_fetch":            canonical.ToolWebFetch,
	"web_search":           canonical.ToolWebSearch,
}

// inputKeys contains only statically or live-verified primary fields. The
// 0.2.101 tool schemas captured in ~/.grok pin these fields. WebSearch is
// intentionally absent: its native name is documented, but no authoritative
// input schema/capture was available without a live probe. Missing verified
// shapes therefore fall back visibly to the tool name instead of silently
// relying on guessed fields.
var inputKeys = map[string][]string{
	canonical.ToolBash:     {"command"},
	canonical.ToolRead:     {"target_file"},
	canonical.ToolWrite:    {"file_path"},
	canonical.ToolEdit:     {"file_path"},
	canonical.ToolGlob:     {"target_directory"},
	canonical.ToolGrep:     {"pattern"},
	canonical.ToolWebFetch: {"url"},
}

// grokInput is grok's hook stdin envelope.
// LIVE-VERIFIED (probe 2026-07-03, grok 0.2.82): the fields are camelCase
// (toolName / toolInput / hookEventName / permissionMode / sessionId /
// workspaceRoot), NOT the snake_case the design inferred. hookEventName's VALUE
// is "pre_tool_use". The command lives in toolInput.command.
type grokInput struct {
	ToolName       string          `json:"toolName"`
	ToolInput      json.RawMessage `json:"toolInput"`
	SessionID      string          `json:"sessionId"`
	CWD            string          `json:"cwd"`
	WorkspaceRoot  string          `json:"workspaceRoot"`
	HookEventName  string          `json:"hookEventName"`
	PermissionMode string          `json:"permissionMode"`
}

// grokPreToolUseEvent is the hookEventName value grok sends for a PreToolUse
// hook (verified). normalizeEvent maps it to the canonical "PreToolUse" so the
// caller's event filter accepts it.
const grokPreToolUseEvent = "pre_tool_use"

func normalizeEvent(name string) string {
	if name == grokPreToolUseEvent {
		return "PreToolUse"
	}
	return name
}

// grokOutput is grok's blocking-hook stdout shape (design §2.3b).
type grokOutput struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

// Adapter implements adapter.Adapter for xAI grok.
type Adapter struct{}

// New returns a grok adapter.
func New() *Adapter { return &Adapter{} }

// Name returns the harness name.
func (a *Adapter) Name() string { return "grok" }

// ParseInput reads grok's hook stdin and maps it to a canonical tool call,
// normalising grok's tool taxonomy (run_terminal_command -> Bash, etc.) and
// extracting the primary matchable string from tool_input.
func (a *Adapter) ParseInput(r io.Reader) (*canonical.ToolCall, error) {
	var in grokInput
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		return nil, fmt.Errorf("parsing grok hook input: %w", err)
	}

	tool := in.ToolName
	if canon, ok := toolAliases[tool]; ok {
		tool = canon
	}

	cwd := in.CWD
	if cwd == "" {
		cwd = in.WorkspaceRoot
	}

	return &canonical.ToolCall{
		Tool:           tool,
		InputString:    extractInputString(tool, in.ToolInput),
		CWD:            cwd,
		PermissionMode: in.PermissionMode,
		EventName:      normalizeEvent(in.HookEventName),
	}, nil
}

// extractInputString pulls the primary matchable string for a canonical tool
// from grok's tool_input, trying the candidate keys in order. Falls back to the
// tool name (matching the Claude adapter's default for unknown tools) when no
// candidate key holds a string.
func extractInputString(tool string, raw json.RawMessage) string {
	keys, ok := inputKeys[tool]
	if !ok || len(raw) == 0 {
		return tool
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return tool
	}
	for _, k := range keys {
		if v, present := fields[k]; present {
			var s string
			if json.Unmarshal(v, &s) == nil {
				// Return the decoded value even when empty: a present key that
				// decodes to "" is the actual input (an empty command), matching
				// the claude/codex adapters. Only a MISSING or non-string key
				// falls through to the next candidate / the tool-name default.
				return s
			}
		}
	}
	return tool
}

// Encode writes grok's native verdict encoding and returns the exit code.
// Deny emits {"decision":"deny","reason":...} + exit 2; allow emits
// {"decision":"allow"} + exit 0; abstain emits NOTHING and exits exitAbstain
// so grok fail-opens (no verdict asserted — see the package doc / Q6).
func (a *Adapter) Encode(w io.Writer, v canonical.Verdict) (int, error) {
	switch v.Decision {
	case canonical.Deny:
		if err := writeJSON(w, grokOutput{Decision: "deny", Reason: v.Reason}); err != nil {
			return exitDeny, err
		}
		return exitDeny, nil
	case canonical.Allow:
		if err := writeJSON(w, grokOutput{Decision: "allow"}); err != nil {
			return exitAllow, err
		}
		return exitAllow, nil
	default: // Abstain — route through grok's fail-open path.
		return exitAbstain, nil
	}
}

func writeJSON(w io.Writer, out grokOutput) error {
	return json.NewEncoder(w).Encode(out)
}
