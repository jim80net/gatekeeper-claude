// Package codex implements the adapter for the OpenAI Codex CLI PreToolUse hook.
//
// Codex's PreToolUse hook wire is Claude-compatible in SHAPE: stdin carries
// tool_name / tool_input / cwd / hook_event_name / permission_mode, and stdout
// uses the same hookSpecificOutput { permissionDecision, permissionDecisionReason }
// envelope. It is NOT Claude-compatible in the set of VALUES the PreToolUse
// handler actually accepts for permissionDecision — see the P0 fix below.
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
// P0 FIX (2026-07-08): codex's PreToolUse handler does NOT support an
// explicit permissionDecision:"allow" — confirmed by extracting the literal
// error strings straight out of the codex-cli 0.143.0 native binary
// (.../codex-linux-arm64/.../bin/codex, `strings` on the ELF): the binary
// contains "PreToolUse hook returned unsupported permissionDecision:allow"
// verbatim (and "...unsupported permissionDecision:ask" alongside it — "ask"
// was already known-unsupported per the note below, now doubly confirmed).
// This is despite "allow" being a schema-legal member of the shared
// PreToolUsePermissionDecisionWire enum (also extracted from the binary's
// embedded JSON schema: `"enum": ["allow", "deny", "ask"]`) — the wire TYPE
// permits all three, but the PreToolUse RUNTIME HANDLER only implements
// "deny"; "allow" and "ask" both hit an explicit unsupported-value error.
// Root cause is therefore a VALUE-set mismatch in the shared encoder, not a
// harness-detection failure — `--harness codex` selection is a plain flag/env
// lookup (cmd/claude-gatekeeper/main.go) with no auto-detection to misfire.
//
// Fix: codex's Encode treats an ALLOW verdict identically to ABSTAIN (write
// nothing, exit 0) rather than emitting the unsupported explicit value. This
// is a real, measurable behavior change on auto-approve codex desks running
// approval_policy=never: it is a no-op there (abstain already auto-executes
// under full-auto, same outcome as allow). On a more restrictive
// approval_policy (untrusted/on-request/on-failure), it means an ALLOW rule
// no longer skips codex's own approval prompt — it now falls through to that
// prompt instead. That is a strictly SAFER regression (an extra prompt, never
// a broken hook) than the alternative of continuing to emit a value codex's
// own binary rejects outright, and "fail safe by abstaining when a decision
// is unsupported" is the explicit standing requirement for this adapter.
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

// Encode writes codex's verdict encoding and returns the exit code. Codex
// signals through stdout JSON and exits 0.
//
// Only DENY is encoded explicitly (the Claude-compatible
// hookSpecificOutput.permissionDecision:"deny" wire, live-verified to
// block). ALLOW and ABSTAIN are BOTH encoded as silent abstain (write
// nothing) — codex's PreToolUse handler has no supported value for an
// explicit "allow" (see the package doc's P0 FIX note); abstain is the only
// safe way to signal "no objection" without hitting codex's
// unsupported-permissionDecision error. "ask" is intentionally never
// emitted (it fails open headless — see the package doc).
func (a *Adapter) Encode(w io.Writer, v canonical.Verdict) (int, error) {
	if v.Decision != canonical.Deny {
		return 0, nil // allow and abstain both encode as silent abstain
	}
	reason := v.Reason
	if reason == "" {
		// Defensive: codex's own binary additionally rejects an empty
		// permissionDecisionReason on a deny ("...deny without a non-empty
		// permissionDecisionReason"). The engine always supplies a rule's
		// `reason` field in practice, but fail safe rather than ship a
		// second unsupported-decision error class if a rule is ever
		// misconfigured with reason = "".
		reason = "Denied by gatekeeper (no reason configured)"
	}
	out := claude.ClaudeOutput(canonical.Verdict{Decision: canonical.Deny, Reason: reason})
	return 0, protocol.WriteOutput(w, out)
}
