package claude_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jim80net/claude-gatekeeper/internal/adapter/claude"
	"github.com/jim80net/gatekeeper-core/canonical"
)

func TestParseInput(t *testing.T) {
	a := claude.New()
	tc, err := a.ParseInput(strings.NewReader(
		`{"tool_name":"Bash","tool_input":{"command":"git push origin main"},"cwd":"/repo","hook_event_name":"PreToolUse","permission_mode":"default"}`))
	if err != nil {
		t.Fatalf("ParseInput: %v", err)
	}
	if tc.Tool != canonical.ToolBash {
		t.Errorf("Tool = %q, want Bash", tc.Tool)
	}
	if tc.InputString != "git push origin main" {
		t.Errorf("InputString = %q", tc.InputString)
	}
	if tc.CWD != "/repo" {
		t.Errorf("CWD = %q, want /repo", tc.CWD)
	}
	if tc.EventName != "PreToolUse" {
		t.Errorf("EventName = %q", tc.EventName)
	}
}

func TestParseInputError(t *testing.T) {
	a := claude.New()
	if _, err := a.ParseInput(strings.NewReader("{not json}")); err == nil {
		t.Error("expected parse error for malformed json")
	}
}

// TestEncodeWire pins the exact Claude wire bytes + exit code per decision.
// This is the byte-compatibility regression bar for existing installs.
func TestEncodeWire(t *testing.T) {
	tests := []struct {
		name     string
		verdict  canonical.Verdict
		wantOut  string
		wantCode int
	}{
		{
			name:     "deny",
			verdict:  canonical.Verdict{Decision: canonical.Deny, Reason: "Push to protected branch (main/master)"},
			wantOut:  `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Push to protected branch (main/master)"}}` + "\n",
			wantCode: 0,
		},
		{
			name:     "allow",
			verdict:  canonical.Verdict{Decision: canonical.Allow, Reason: "Approved by gatekeeper"},
			wantOut:  `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"Approved by gatekeeper"}}` + "\n",
			wantCode: 0,
		},
		{
			name:     "abstain writes nothing, exit 0",
			verdict:  canonical.Verdict{Decision: canonical.Abstain},
			wantOut:  "",
			wantCode: 0,
		},
	}

	a := claude.New()
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
