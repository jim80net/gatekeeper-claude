// Package codex implements the adapter for the OpenAI Codex CLI PreToolUse hook.
//
// Codex's PreToolUse hook wire is Claude-compatible (design §2.5b, verified
// against the codex 0.142.5 binary's embedded schema strings): stdin carries
// tool_name / tool_input / cwd / hook_event_name / permission_mode, and stdout
// uses the same hookSpecificOutput { permissionDecision, permissionDecisionReason }
// shape. This adapter therefore reuses the Claude wire encoder. Abstain emits
// NO hookSpecificOutput and NO top-level decision (exit 0), so codex treats the
// hook as absent and falls through to its native approval_policy.
//
// SHIP-GATE (design §4.4, §8): the codex adapter is built and unit-tested
// against the documented wire shapes, but must not be declared live-verified
// until `codex login` allows resolving Q2 (does permissionDecision:"deny"
// actually block?), Q3 (global vs project-only hooks config?), and Q7 (does an
// empty response fall through to approval_policy, and does "ask" override
// approval_policy=never?). See README.
package codex

import (
	"io"

	"github.com/jim80net/claude-gatekeeper/internal/adapter/claude"
	"github.com/jim80net/claude-gatekeeper/internal/canonical"
	"github.com/jim80net/claude-gatekeeper/internal/protocol"
)

// toolAliases maps codex-native tool names that differ from the canonical
// taxonomy onto canonical names. Codex maps near-1:1 to Claude's names
// (design §2.5a), so this table is intentionally small; unmapped names pass
// through unchanged. Extend once `codex login` confirms codex's exact tool
// taxonomy (Q2/Q3).
var toolAliases = map[string]string{
	"shell":       canonical.ToolBash,
	"local_shell": canonical.ToolBash,
}

// Adapter implements adapter.Adapter for OpenAI Codex.
type Adapter struct{}

// New returns a Codex adapter.
func New() *Adapter { return &Adapter{} }

// Name returns the harness name.
func (a *Adapter) Name() string { return "codex" }

// ParseInput reads codex's Claude-compatible HookInput JSON and maps it to a
// canonical tool call, normalising any codex-specific shell tool name to Bash.
func (a *Adapter) ParseInput(r io.Reader) (*canonical.ToolCall, error) {
	in, err := protocol.ReadInput(r)
	if err != nil {
		return nil, err
	}
	tool := in.ToolName
	if canon, ok := toolAliases[tool]; ok {
		tool = canon
	}
	return &canonical.ToolCall{
		Tool:           tool,
		InputString:    protocol.ExtractInputString(tool, in.ToolInput),
		CWD:            in.CWD,
		PermissionMode: in.PermissionMode,
		EventName:      in.HookEventName,
	}, nil
}

// Encode writes codex's verdict encoding (the shared Claude PreToolUse wire)
// and returns the exit code. Codex signals through stdout JSON and exits 0;
// abstain writes nothing so codex falls through to its native approval_policy.
//
// Abstain seam (Q7): on a codex agent running approval_policy=never, silent
// abstain means the command auto-executes. Codex additionally supports
// permissionDecision:"ask" to FORCE its approval prompt; if a live probe
// confirms "ask" overrides approval_policy=never, an "ask" error-path encoding
// would be a materially safer defer than silence. That is deliberately NOT
// emitted here until Q7 is resolved, to avoid asserting unverified behaviour.
func (a *Adapter) Encode(w io.Writer, v canonical.Verdict) (int, error) {
	out := claude.ClaudeOutput(v)
	if out == nil {
		return 0, nil // abstain: write nothing, exit 0
	}
	return 0, protocol.WriteOutput(w, out)
}
