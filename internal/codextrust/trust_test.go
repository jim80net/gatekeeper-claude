package codextrust

import (
	"os"
	"path/filepath"
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

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
