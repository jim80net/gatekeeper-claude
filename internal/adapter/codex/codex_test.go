package codex_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jim80net/claude-gatekeeper/internal/adapter/codex"
	"github.com/jim80net/claude-gatekeeper/internal/canonical"
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

// TestEncodeWire pins codex's Claude-compatible wire per decision. Abstain
// emits no hookSpecificOutput and no top-level decision (design §4.3).
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
			name:     "allow",
			verdict:  canonical.Verdict{Decision: canonical.Allow, Reason: "ok"},
			wantOut:  `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"ok"}}` + "\n",
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
