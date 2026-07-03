package grok_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jim80net/claude-gatekeeper/internal/adapter/grok"
	"github.com/jim80net/claude-gatekeeper/internal/canonical"
)

// TestParseInputTaxonomy verifies grok's tool taxonomy is normalised onto the
// canonical names and the command string is extracted from tool_input.
//
// NOTE: grok's exact per-tool tool_input field names are UNVERIFIED (design
// §2.3b); these fixtures are constructed to exercise the adapter's candidate-key
// extraction, NOT captured from a live grok run.
func TestParseInputTaxonomy(t *testing.T) {
	tests := []struct {
		name      string
		stdin     string
		wantTool  string
		wantInput string
	}{
		{
			name:      "run_terminal_cmd -> Bash",
			stdin:     `{"tool_name":"run_terminal_cmd","tool_input":{"command":"git push origin main"},"cwd":"/repo"}`,
			wantTool:  canonical.ToolBash,
			wantInput: "git push origin main",
		},
		{
			name:      "read_file -> Read",
			stdin:     `{"tool_name":"read_file","tool_input":{"file_path":"/home/u/.env"},"working_directory":"/repo"}`,
			wantTool:  canonical.ToolRead,
			wantInput: "/home/u/.env",
		},
		{
			name:      "search_replace -> Edit",
			stdin:     `{"tool_name":"search_replace","tool_input":{"file_path":"/repo/main.go"}}`,
			wantTool:  canonical.ToolEdit,
			wantInput: "/repo/main.go",
		},
		{
			name:      "grep_search -> Grep",
			stdin:     `{"tool_name":"grep_search","tool_input":{"pattern":"TODO"}}`,
			wantTool:  canonical.ToolGrep,
			wantInput: "TODO",
		},
		{
			name:      "unknown tool passes through, input falls back to tool name",
			stdin:     `{"tool_name":"mcp__x__y","tool_input":{"z":"q"}}`,
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

func TestParseInputCWDFallback(t *testing.T) {
	a := grok.New()
	tc, err := a.ParseInput(strings.NewReader(
		`{"tool_name":"run_terminal_cmd","tool_input":{"command":"ls"},"working_directory":"/wd"}`))
	if err != nil {
		t.Fatalf("ParseInput: %v", err)
	}
	if tc.CWD != "/wd" {
		t.Errorf("CWD = %q, want /wd (working_directory fallback)", tc.CWD)
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
