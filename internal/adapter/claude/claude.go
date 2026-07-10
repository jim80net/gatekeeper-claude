// Package claude implements the adapter for Claude Code's PreToolUse hook wire.
//
// This is the reference adapter and is byte-for-byte compatible with the
// gatekeeper's original (pre-refactor) Claude behaviour: input is Claude's
// HookInput JSON, output is the hookSpecificOutput.permissionDecision shape,
// and abstain writes nothing and exits 0 so Claude Code runs its native
// permission flow.
package claude

import (
	"io"

	"github.com/jim80net/claude-gatekeeper/internal/protocol"
	"github.com/jim80net/gatekeeper-core/canonical"
)

// Adapter implements adapter.Adapter for Claude Code.
type Adapter struct{}

// New returns a Claude adapter.
func New() *Adapter { return &Adapter{} }

// Name returns the harness name.
func (a *Adapter) Name() string { return "claude" }

// ParseInput reads Claude's HookInput JSON and maps it to a canonical tool
// call. Claude's tool names ARE the canonical names, so the tool passes through
// unchanged; ExtractInputString pulls the primary matchable string.
func (a *Adapter) ParseInput(r io.Reader) (*canonical.ToolCall, error) {
	in, err := protocol.ReadInput(r)
	if err != nil {
		return nil, err
	}
	return &canonical.ToolCall{
		Tool:           in.ToolName,
		InputString:    protocol.ExtractInputString(in.ToolName, in.ToolInput),
		CWD:            in.CWD,
		PermissionMode: in.PermissionMode,
		EventName:      in.HookEventName,
	}, nil
}

// Encode writes the Claude-native verdict encoding and returns the exit code.
// Claude signals its decision entirely through stdout JSON and always exits 0;
// abstain writes nothing (Claude then runs its native permission prompt).
func (a *Adapter) Encode(w io.Writer, v canonical.Verdict) (int, error) {
	out := ClaudeOutput(v)
	if out == nil {
		return 0, nil // abstain: write nothing, exit 0
	}
	return 0, protocol.WriteOutput(w, out)
}

// ClaudeOutput builds the Claude/Codex hookSpecificOutput wire for a verdict,
// or nil for abstain. It is exported so the codex adapter (which shares the
// Claude PreToolUse wire) can reuse the exact same encoding.
func ClaudeOutput(v canonical.Verdict) *protocol.HookOutput {
	switch v.Decision {
	case canonical.Allow:
		return makeOutput(protocol.Allow, v.Reason)
	case canonical.Deny:
		return makeOutput(protocol.Deny, v.Reason)
	default: // Abstain
		return nil
	}
}

func makeOutput(decision protocol.Decision, reason string) *protocol.HookOutput {
	return &protocol.HookOutput{
		HookSpecificOutput: &protocol.HookSpecificOutput{
			HookEventName:            "PreToolUse",
			PermissionDecision:       decision,
			PermissionDecisionReason: reason,
		},
	}
}
