package codex_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jim80net/claude-gatekeeper/internal/adapter/codex"
	"github.com/jim80net/gatekeeper-core/canonical"
)

func TestParseInput(t *testing.T) {
	a := codex.New()
	tc, err := a.ParseInput(strings.NewReader(
		`{"tool_name":"Bash","tool_input":{"command":"git status"},"cwd":"/repo","hook_event_name":"PreToolUse"}`))
	if err != nil {
		t.Fatalf("ParseInput: %v", err)
	}
	if tc.Tool != canonical.ToolBash || tc.InputString != "git status" {
		t.Errorf("got tool=%q input=%q", tc.Tool, tc.InputString)
	}
	if tc.CWD != "/repo" {
		t.Errorf("CWD = %q, want /repo", tc.CWD)
	}
	if tc.EventName != "PreToolUse" {
		t.Errorf("EventName = %q, want PreToolUse", tc.EventName)
	}
}

// TestParseInputShellAlias verifies a codex-native shell tool name normalises
// to canonical Bash so the shared push-to-main rules apply.
func TestParseInputShellAlias(t *testing.T) {
	a := codex.New()
	tc, err := a.ParseInput(strings.NewReader(
		`{"tool_name":"shell","tool_input":{"command":"git push origin main"},"cwd":"/repo"}`))
	if err != nil {
		t.Fatalf("ParseInput: %v", err)
	}
	if tc.Tool != canonical.ToolBash {
		t.Errorf("Tool = %q, want Bash (alias)", tc.Tool)
	}
	if tc.InputString != "git push origin main" {
		t.Errorf("InputString = %q", tc.InputString)
	}
}

// TestEncodeWire pins codex's per-decision wire.
//
// P0 regression coverage (2026-07-08): codex's PreToolUse handler rejects an
// explicit permissionDecision:"allow" with a hard error — confirmed via the
// literal error string extracted from the codex-cli 0.143.0 binary,
// "PreToolUse hook returned unsupported permissionDecision:allow" — despite
// "allow" being a schema-legal value of the shared wire enum. ALLOW must
// therefore encode identically to ABSTAIN (no hookSpecificOutput, exit 0).
// Only DENY is ever encoded explicitly.
func TestEncodeWire(t *testing.T) {
	tests := []struct {
		name     string
		verdict  canonical.Verdict
		wantOut  string
		wantCode int
	}{
		{
			name:     "deny",
			verdict:  canonical.Verdict{Decision: canonical.Deny, Reason: "blocked"},
			wantOut:  `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"blocked"}}` + "\n",
			wantCode: 0,
		},
		{
			name:     "deny with empty reason falls back to a non-empty default",
			verdict:  canonical.Verdict{Decision: canonical.Deny, Reason: ""},
			wantOut:  `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Denied by gatekeeper (no reason configured)"}}` + "\n",
			wantCode: 0,
		},
		{
			name:     "allow: NOT emitted explicitly — codex rejects permissionDecision:allow; encodes as abstain",
			verdict:  canonical.Verdict{Decision: canonical.Allow, Reason: "ok"},
			wantOut:  "",
			wantCode: 0,
		},
		{
			name:     "abstain: no hookSpecificOutput, exit 0",
			verdict:  canonical.Verdict{Decision: canonical.Abstain},
			wantOut:  "",
			wantCode: 0,
		},
	}

	a := codex.New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			code, err := a.Encode(&buf, tt.verdict)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			if buf.String() != tt.wantOut {
				t.Errorf("out = %q, want %q", buf.String(), tt.wantOut)
			}
		})
	}
}

// TestEncodeNeverEmitsUnsupportedPermissionDecision is a blunt regression
// guard: for every canonical.Decision value, codex's encoded output must
// never contain the literal substring `"permissionDecision":"allow"` or
// `"permissionDecision":"ask"` — both are confirmed-unsupported by codex's
// own binary (see package doc). Only "deny" may appear.
func TestEncodeNeverEmitsUnsupportedPermissionDecision(t *testing.T) {
	a := codex.New()
	decisions := []canonical.Decision{canonical.Allow, canonical.Deny, canonical.Abstain}
	for _, d := range decisions {
		var buf bytes.Buffer
		if _, err := a.Encode(&buf, canonical.Verdict{Decision: d, Reason: "x"}); err != nil {
			t.Fatalf("Encode(%v): %v", d, err)
		}
		out := buf.String()
		if strings.Contains(out, `"permissionDecision":"allow"`) {
			t.Errorf("decision=%v: encoded output contains unsupported permissionDecision:allow: %s", d, out)
		}
		if strings.Contains(out, `"permissionDecision":"ask"`) {
			t.Errorf("decision=%v: encoded output contains unsupported permissionDecision:ask: %s", d, out)
		}
	}
}
