package setup_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jim80net/claude-gatekeeper/internal/setup"
)

func TestInstallGrok(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	if err := setup.InstallGrok("/usr/local/bin/gatekeeper"); err != nil {
		t.Fatalf("InstallGrok: %v", err)
	}

	hookPath := filepath.Join(homeDir, ".grok", "hooks", "gatekeeper.json")
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("reading grok hook: %v", err)
	}
	var hook map[string]interface{}
	if err := json.Unmarshal(data, &hook); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if hook["command_raw"] != "/usr/local/bin/gatekeeper --harness grok" {
		t.Errorf("command_raw = %v", hook["command_raw"])
	}
	if hook["enabled"] != true {
		t.Errorf("enabled = %v, want true", hook["enabled"])
	}
	if _, ok := hook["matcher"]; !ok {
		t.Error("missing matcher field")
	}
	if _, ok := hook["timeout_ms"]; !ok {
		t.Error("missing timeout_ms field")
	}
}

func TestInstallCodex(t *testing.T) {
	projectDir := t.TempDir()

	if err := setup.InstallCodex("/usr/local/bin/gatekeeper", projectDir); err != nil {
		t.Fatalf("InstallCodex: %v", err)
	}

	hookPath := filepath.Join(projectDir, ".codex", "hooks.json")
	data, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("reading codex hook: %v", err)
	}
	var cfg struct {
		Hooks struct {
			PreToolUse []struct {
				Hooks []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cfg.Hooks.PreToolUse) != 1 || len(cfg.Hooks.PreToolUse[0].Hooks) != 1 {
		t.Fatalf("unexpected codex hook structure: %s", data)
	}
	h := cfg.Hooks.PreToolUse[0].Hooks[0]
	if h.Type != "command" {
		t.Errorf("type = %q, want command", h.Type)
	}
	if h.Command != "/usr/local/bin/gatekeeper --harness codex" {
		t.Errorf("command = %q", h.Command)
	}
}

// TestInstallGrokIsIdempotentlyBackedUp confirms a second install backs up the
// prior hook rather than clobbering silently.
func TestInstallGrokBackup(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)

	if err := setup.InstallGrok("/bin/one"); err != nil {
		t.Fatal(err)
	}
	if err := setup.InstallGrok("/bin/two"); err != nil {
		t.Fatal(err)
	}
	// A backup of the first install should exist alongside the hook.
	entries, _ := os.ReadDir(filepath.Join(homeDir, ".grok", "hooks"))
	backups := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" && len(e.Name()) > len("gatekeeper.json") {
			backups++
		}
	}
	if backups == 0 {
		t.Error("expected a backup of the prior grok hook")
	}
}
