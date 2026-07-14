package codextrust

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectTrustedUntrustedAndHashDrift(t *testing.T) {
	home := t.TempDir()
	hookPath := filepath.Join(home, ".codex", "hooks.json")
	command := "/usr/local/bin/claude-gatekeeper --harness codex"
	writeTestFile(t, hookPath, `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"`+command+`","timeout":10,"statusMessage":"Checking permissions..."}]}]}}`)

	untrusted, err := Inspect(home, hookPath, command)
	if err != nil {
		t.Fatal(err)
	}
	if untrusted.Trusted() || untrusted.TrustedHash != "" {
		t.Fatalf("untrusted status = %#v", untrusted)
	}
	// Golden value independently matches Codex CLI's normalized identity hash.
	const wantHash = "sha256:7f3fa6af0d98bdca44b57287aa615345899666dbcaeb7f865dedf3666868f8af"
	if untrusted.CurrentHash != wantHash {
		t.Fatalf("current hash = %q, want %q", untrusted.CurrentHash, wantHash)
	}

	configPath := filepath.Join(home, ".codex", "config.toml")
	writeTestFile(t, configPath, `[hooks.state."`+untrusted.Key+`"]
trusted_hash = "`+wantHash+`"
`)
	trusted, err := Inspect(home, hookPath, command)
	if err != nil {
		t.Fatal(err)
	}
	if !trusted.Trusted() {
		t.Fatalf("trusted status = %#v", trusted)
	}

	writeTestFile(t, configPath, `[hooks.state."`+untrusted.Key+`"]
trusted_hash = "sha256:stale"
`)
	drift, err := Inspect(home, hookPath, command)
	if err != nil {
		t.Fatal(err)
	}
	if drift.Trusted() || drift.TrustedHash == "" || drift.TrustedHash == drift.CurrentHash {
		t.Fatalf("hash-drift status = %#v", drift)
	}
}

func TestHashPreToolUseGoldenCanonicalization(t *testing.T) {
	timeout10 := uint64(10)
	windows := "tool.exe --check"
	matcher := "Bash|Shell"
	tests := []struct {
		name    string
		matcher *string
		handler commandHook
		want    string
	}{
		{
			name:    "HTML-sensitive shell operators are not escaped",
			handler: commandHook{Type: "command", Command: "tool --check && next 2>&1", Timeout: &timeout10},
			want:    "sha256:182581d1f57c023049251c749fab579626af4e8fa848e566d4ab58c1ea98932c",
		},
		{
			name:    "commandWindows is excluded from normalized identity",
			handler: commandHook{Type: "command", Command: "tool --check", CommandWindows: &windows, Timeout: &timeout10},
			want:    "sha256:60630626d46ec6b3a746a08c2ecca9d6e0c39e2fc7b936bead626f2d03301bf5",
		},
		{
			name:    "missing timeout defaults to 600",
			handler: commandHook{Type: "command", Command: "tool --check"},
			want:    "sha256:29ea4bafff3f768d81a84261150c15c9e9aa3defeaab42bb1f0b6d9854264ed7",
		},
		{
			name:    "matcher participates in identity",
			matcher: &matcher,
			handler: commandHook{Type: "command", Command: "tool --check", Timeout: &timeout10},
			want:    "sha256:c527659e968c7cfa7b9f65d34cafc5b9baf920a8902b61618fa84dccf6fef6f9",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := hashPreToolUse(tt.matcher, tt.handler)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("hash = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestInspectExplainsHooksCodexSkipsBeforeTrust(t *testing.T) {
	tests := []struct {
		name, group, want string
	}{
		{"async handler", `{"hooks":[{"type":"command","command":"tool","async":true}]}`, "async hooks are not supported"},
		{"invalid matcher", `{"matcher":"(","hooks":[{"type":"command","command":"tool"}]}`, "invalid matcher"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			hookPath := filepath.Join(home, ".codex", "hooks.json")
			writeTestFile(t, hookPath, `{"hooks":{"PreToolUse":[`+tt.group+`]}}`)
			_, err := Inspect(home, hookPath, "tool")
			if err == nil || !strings.Contains(err.Error(), "before trust evaluation") || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want inert-hook explanation containing %q", err, tt.want)
			}
		})
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
