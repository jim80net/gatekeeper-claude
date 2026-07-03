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
// SHIP-GATE (design §8): the grok deny path (and on_error="deny") depends on
// Q1 — whether an explicit deny/hook fires under grok's --always-approve. Build
// and unit-test the wire shapes; do not declare grok live-verified until the
// Q1/Q6 probe passes. See README.
package grok

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/jim80net/claude-gatekeeper/internal/canonical"
)

// Grok exit codes for blocking hooks (design §2.3b).
const (
	exitAllow = 0 // {"decision":"allow"} / success
	exitDeny  = 2 // {"decision":"deny"} — explicit deny
	// exitAbstain triggers grok's fail-open-on-error path so NO verdict is
	// asserted (not an allow, not a deny). Non-zero and != exitDeny. See Q6.
	exitAbstain = 1
)

// toolAliases maps grok-native tool names onto the canonical taxonomy
// (design §4.1). Unmapped names pass through unchanged. The mapped set is what
// the design cites from the grok binary; extend once a live grok probe
// confirms the full tool taxonomy.
var toolAliases = map[string]string{
	"run_terminal_cmd": canonical.ToolBash,
	"search_replace":   canonical.ToolEdit,
	"read_file":        canonical.ToolRead,
	"grep_search":      canonical.ToolGrep,
}

// inputKeys lists, per canonical tool, the candidate tool_input JSON keys to
// try when extracting the primary matchable string. Grok's exact tool_input
// field names are UNVERIFIED (design §2.3b lists the stdin envelope fields but
// not the per-tool input schema), so a superset of plausible keys is tried in
// order; the first present string value wins. Confirm and tighten once a live
// grok probe captures a real tool_input payload.
var inputKeys = map[string][]string{
	canonical.ToolBash:      {"command", "cmd"},
	canonical.ToolRead:      {"file_path", "path", "target_file"},
	canonical.ToolWrite:     {"file_path", "path", "target_file"},
	canonical.ToolEdit:      {"file_path", "path", "target_file"},
	canonical.ToolGlob:      {"pattern", "glob_pattern", "path"},
	canonical.ToolGrep:      {"pattern", "query", "regex"},
	canonical.ToolWebFetch:  {"url"},
	canonical.ToolWebSearch: {"query", "search_term"},
}

// grokInput is grok's hook stdin envelope (design §2.3b). Grok sends either
// "cwd" or "working_directory"; both are accepted.
type grokInput struct {
	ToolName         string          `json:"tool_name"`
	ToolInput        json.RawMessage `json:"tool_input"`
	SessionID        string          `json:"session_id"`
	CWD              string          `json:"cwd"`
	WorkingDirectory string          `json:"working_directory"`
	HookEventName    string          `json:"hook_event_name"`
	PermissionMode   string          `json:"permission_mode"`
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
// normalising grok's tool taxonomy (run_terminal_cmd -> Bash, etc.) and
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
		cwd = in.WorkingDirectory
	}

	return &canonical.ToolCall{
		Tool:           tool,
		InputString:    extractInputString(tool, in.ToolInput),
		CWD:            cwd,
		PermissionMode: in.PermissionMode,
		EventName:      in.HookEventName,
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
			if json.Unmarshal(v, &s) == nil && s != "" {
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
