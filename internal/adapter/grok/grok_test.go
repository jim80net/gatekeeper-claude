package grok_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jim80net/claude-gatekeeper/internal/adapter/grok"
	"github.com/jim80net/claude-gatekeeper/internal/canonical"
)

// TestParseInputLivePayload is the golden regression for grok's REAL hook stdin,
// captured verbatim from a live grok 0.2.82 PreToolUse probe (2026-07-03). The
// schema is camelCase (toolName/toolInput/hookEventName/permissionMode), the
// shell tool is "Shell", the event value is "pre_tool_use", and the command
// lives in toolInput.command. A regression here means the adapter has drifted
// from grok's actual wire and would silently enforce nothing.
func TestParseInputLivePayload(t *testing.T) {
	const live = `{"hookEventName":"pre_tool_use","sessionId":"019f25eb","cwd":"/proj","workspaceRoot":"/proj","timestamp":"2026-07-03T03:00:26Z","transcriptPath":"/x/updates.jsonl","toolName":"Shell","toolUseId":"call-abc","toolInput":{"command":"touch /tmp/gkprobe_deny_canary","description":"Create deny canary"},"toolInputTruncated":false,"permissionMode":"bypassPermissions"}`
	tc, err := grok.New().ParseInput(strings.NewReader(live))
	if err != nil {
		t.Fatalf("ParseInput: %v", err)
	}
	if tc.Tool != canonical.ToolBash {
		t.Errorf("Tool = %q, want Bash (Shell alias)", tc.Tool)
	}
	if tc.InputString != "touch /tmp/gkprobe_deny_canary" {
		t.Errorf("InputString = %q", tc.InputString)
	}
	if tc.CWD != "/proj" {
		t.Errorf("CWD = %q, want /proj", tc.CWD)
	}
	if tc.EventName != "PreToolUse" {
		t.Errorf("EventName = %q, want PreToolUse (normalised from pre_tool_use)", tc.EventName)
	}
	if tc.PermissionMode != "bypassPermissions" {
		t.Errorf("PermissionMode = %q", tc.PermissionMode)
	}
}

// TestParseInputTaxonomy verifies grok's tool taxonomy is normalised onto the
// canonical names and the command/path is extracted from toolInput. The shell
// tool "Shell" and the camelCase field names are live-verified (2026-07-03);
// the read/edit/grep aliases remain design-inferred (unverified) but are covered
// so a future live capture can confirm or correct them in one place.
func TestParseInputTaxonomy(t *testing.T) {
	tests := []struct {
		name      string
		stdin     string
		wantTool  string
		wantInput string
	}{
		{
			name:      "Shell -> Bash (live-verified)",
			stdin:     `{"toolName":"Shell","toolInput":{"command":"git push origin main"},"cwd":"/repo"}`,
			wantTool:  canonical.ToolBash,
			wantInput: "git push origin main",
		},
		{
			name:      "run_terminal_cmd -> Bash (defensive alias)",
			stdin:     `{"toolName":"run_terminal_cmd","toolInput":{"command":"ls"},"cwd":"/repo"}`,
			wantTool:  canonical.ToolBash,
			wantInput: "ls",
		},
		{
			name:      "read_file -> Read (inferred alias)",
			stdin:     `{"toolName":"read_file","toolInput":{"file_path":"/home/u/.env"},"workspaceRoot":"/repo"}`,
			wantTool:  canonical.ToolRead,
			wantInput: "/home/u/.env",
		},
		{
			name:      "unknown tool passes through, input falls back to tool name",
			stdin:     `{"toolName":"mcp__x__y","toolInput":{"z":"q"}}`,
			wantTool:  "mcp__x__y",
			wantInput: "mcp__x__y",
		},
	}

	a := grok.New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc, err := a.ParseInput(strings.NewReader(tt.stdin))
			if err != nil {
				t.Fatalf("ParseInput: %v", err)
			}
			if tc.Tool != tt.wantTool {
				t.Errorf("Tool = %q, want %q", tc.Tool, tt.wantTool)
			}
			if tc.InputString != tt.wantInput {
				t.Errorf("InputString = %q, want %q", tc.InputString, tt.wantInput)
			}
		})
	}
}

// TestParseInputCWDFallback verifies workspaceRoot is used when cwd is absent.
func TestParseInputCWDFallback(t *testing.T) {
	a := grok.New()
	tc, err := a.ParseInput(strings.NewReader(
		`{"toolName":"Shell","toolInput":{"command":"ls"},"workspaceRoot":"/wd"}`))
	if err != nil {
		t.Fatalf("ParseInput: %v", err)
	}
	if tc.CWD != "/wd" {
		t.Errorf("CWD = %q, want /wd (workspaceRoot fallback)", tc.CWD)
	}
}

func TestParseInputError(t *testing.T) {
	a := grok.New()
	if _, err := a.ParseInput(strings.NewReader("{not json}")); err == nil {
		t.Error("expected parse error for malformed json")
	}
}

// TestEncodeWire pins grok's native wire per decision. Abstain routes through
// grok's fail-open path: NO output, exit 1 (non-zero, != deny's exit 2) so grok
// asserts neither allow nor deny (design §4.3, Q6).
func TestEncodeWire(t *testing.T) {
	tests := []struct {
		name     string
		verdict  canonical.Verdict
		wantOut  string
		wantCode int
	}{
		{
			name:     "deny -> exit 2",
			verdict:  canonical.Verdict{Decision: canonical.Deny, Reason: "Push to protected branch (main/master)"},
			wantOut:  `{"decision":"deny","reason":"Push to protected branch (main/master)"}` + "\n",
			wantCode: 2,
		},
		{
			name:     "allow -> exit 0",
			verdict:  canonical.Verdict{Decision: canonical.Allow, Reason: "Approved by gatekeeper"},
			wantOut:  `{"decision":"allow"}` + "\n",
			wantCode: 0,
		},
		{
			name:     "abstain -> fail-open: no output, exit 1",
			verdict:  canonical.Verdict{Decision: canonical.Abstain},
			wantOut:  "",
			wantCode: 1,
		},
	}

	a := grok.New()
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
			// Abstain must NEVER assert an explicit allow on grok.
			if tt.verdict.Decision == canonical.Abstain && strings.Contains(buf.String(), "allow") {
				t.Error("abstain emitted an allow verdict on grok — forbidden")
			}
		})
	}
}
