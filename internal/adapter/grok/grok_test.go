package grok_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jim80net/claude-gatekeeper/internal/adapter/grok"
	"github.com/jim80net/gatekeeper-core/canonical"
)

// TestParseInputLivePayload is the golden regression for grok's REAL hook stdin,
// captured verbatim from a live grok 0.2.82 PreToolUse probe (2026-07-03). The
// schema is camelCase (toolName/toolInput/hookEventName/permissionMode), the
// shell tool is "Shell", the event value is "pre_tool_use", and the command
// lives in toolInput.command. A regression here means the adapter has drifted
// from grok's actual wire and would silently enforce nothing.
func TestParseInputLivePayload(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "pre_tool_use_shell_live.json"))
	if err != nil {
		t.Fatalf("open live fixture: %v", err)
	}
	defer f.Close()
	tc, err := grok.New().ParseInput(f)
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

// TestParseInputGoldenFixtures pins the real native names and primary input
// fields from Grok 0.2.101's shipped hook guide and captured tool schemas.
func TestParseInputGoldenFixtures(t *testing.T) {
	tests := []struct {
		fixture   string
		wantTool  string
		wantInput string
	}{
		{"pre_tool_use_run_terminal_command.json", canonical.ToolBash, "git status"},
		{"pre_tool_use_read_file.json", canonical.ToolRead, "secrets/.env"},
		{"pre_tool_use_search_replace.json", canonical.ToolEdit, "internal/main.go"},
		{"pre_tool_use_write.json", canonical.ToolWrite, "build/output.txt"},
		{"pre_tool_use_list_dir.json", canonical.ToolGlob, "internal/adapter"},
		{"pre_tool_use_grep.json", canonical.ToolGrep, "password\\s*="},
		{"pre_tool_use_web_fetch.json", canonical.ToolWebFetch, "https://example.com/private"},
	}

	a := grok.New()
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", tt.fixture))
			if err != nil {
				t.Fatalf("open fixture: %v", err)
			}
			defer f.Close()
			tc, err := a.ParseInput(f)
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

func TestParseInputUnverifiedShapeFallsBack(t *testing.T) {
	tc, err := grok.New().ParseInput(strings.NewReader(
		`{"toolName":"web_search","toolInput":{"query":"secrets"}}`))
	if err != nil {
		t.Fatalf("ParseInput: %v", err)
	}
	if tc.Tool != canonical.ToolWebSearch || tc.InputString != canonical.ToolWebSearch {
		t.Fatalf("unverified WebSearch = tool %q input %q", tc.Tool, tc.InputString)
	}
}

func TestParseInputDoesNotAcceptFormerGuesses(t *testing.T) {
	tests := []string{
		`{"toolName":"read_file","toolInput":{"file_path":"secrets/.env"}}`,
		`{"toolName":"list_dir","toolInput":{"path":"secrets"}}`,
		`{"toolName":"grep_search","toolInput":{"query":"secret"}}`,
		`{"toolName":"run_terminal_cmd","toolInput":{"cmd":"git push"}}`,
	}
	for _, stdin := range tests {
		tc, err := grok.New().ParseInput(strings.NewReader(stdin))
		if err != nil {
			t.Fatalf("ParseInput(%s): %v", stdin, err)
		}
		if strings.Contains(tc.InputString, "secret") || tc.InputString == "git push" {
			t.Errorf("guessed shape unexpectedly extracted input: %#v", tc)
		}
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
