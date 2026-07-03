// Package codex implements the adapter for the OpenAI Codex CLI PreToolUse hook.
//
// Codex's PreToolUse hook wire is Claude-compatible: stdin carries
// tool_name / tool_input / cwd / hook_event_name / permission_mode, and stdout
// uses the same hookSpecificOutput { permissionDecision, permissionDecisionReason }
// shape. This adapter therefore reuses the Claude wire encoder. Abstain emits
// NO hookSpecificOutput and NO top-level decision (exit 0), so codex treats the
// hook as absent and falls through to its native approval_policy.
//
// LIVE-VERIFIED (probe 2026-07-03, codex-cli 0.142.5, ChatGPT auth):
//   - permissionDecision:"deny" BLOCKS even under approval_policy=never / full
//     auto ("Command blocked by PreToolUse hook" → "PreToolUse Blocked"; the
//     command did not run). So this adapter's deny path (and on_error="deny")
//     is a hard gate on fully-autonomous codex desks — not merely advisory.
//   - Silent abstain (empty stdout, exit 0) falls through to the native
//     approval_policy — exactly the documented abstain semantics.
//   - Codex reads BOTH the global ~/.codex/hooks.json AND a project
//     .codex/hooks.json (the installer covers both).
//
// Do NOT use permissionDecision:"ask" as a defer: under `codex exec` (headless)
// "ask" does NOT override approval_policy=never — it logs "PreToolUse Failed"
// and the command RUNS (fails open). TUI-interactive "ask" is unverified; treat
// "ask" as unavailable. This is why the error path uses silent abstain, never ask.
package codex

import (
	"io"

	"github.com/jim80net/claude-gatekeeper/internal/adapter/claude"
	"github.com/jim80net/claude-gatekeeper/internal/canonical"
	"github.com/jim80net/claude-gatekeeper/internal/protocol"
)

// toolAliases maps codex-native tool names that differ from the canonical
// taxonomy onto canonical names. Codex maps near-1:1 to Claude's names, so this
// table is intentionally small; unmapped names pass through unchanged.
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
// abstain writes nothing so codex falls through to its native approval_policy
// (verified: under approval_policy=never the command then auto-executes — so on
// an auto-approve codex agent, use on_error="deny" if a hard error-path gate is
// wanted). "ask" is intentionally never emitted (it fails open headless — see
// the package doc).
func (a *Adapter) Encode(w io.Writer, v canonical.Verdict) (int, error) {
	out := claude.ClaudeOutput(v)
	if out == nil {
		return 0, nil // abstain: write nothing, exit 0
	}
	return 0, protocol.WriteOutput(w, out)
}
