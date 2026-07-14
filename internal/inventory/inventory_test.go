package inventory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectEnumeratesLiveSurfacesAndReportsDrift(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "go", "bin", "claude-gatekeeper")
	writeFile(t, bin, "binary", 0755)
	writeHook(t, filepath.Join(home, ".grok", "hooks", "gatekeeper.json"), bin+" --harness grok")
	writeHook(t, filepath.Join(home, ".codex", "hooks.json"), bin+" --harness claude")
	writeHook(t, filepath.Join(home, ".claude", "settings.json"), bin)

	plugin := filepath.Join(home, ".claude", "plugins", "cache", "market", "claude-gatekeeper", "1.2.3")
	writeFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), `{"version":2,"plugins":{"claude-gatekeeper@market":[{"installPath":"`+plugin+`","version":"1.2.3"}]}}`, 0644)
	writeFile(t, filepath.Join(plugin, "hooks", "hooks.json"), `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"${CLAUDE_PLUGIN_ROOT}/bin/run.sh"}]}]}}`, 0644)
	writeFile(t, filepath.Join(plugin, "bin", "run.sh"), "#!/bin/sh\n", 0755)
	writeFile(t, filepath.Join(plugin, "bin", "claude-gatekeeper"), "binary", 0755)

	report, err := Collect(Options{
		Home:            home,
		ExpectedBinary:  bin,
		ExpectedVersion: "1.2.3",
		VersionProbe: func(path string) (string, error) {
			return "1.2.3", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Surfaces) != 4 {
		t.Fatalf("got %d surfaces: %#v", len(report.Surfaces), report.Surfaces)
	}
	var codex, pluginSurface Surface
	for _, surface := range report.Surfaces {
		switch surface.Kind {
		case "codex-global":
			codex = surface
		case "claude-plugin":
			pluginSurface = surface
		}
	}
	if codex.Harness != "claude" || !contains(codex.Drift, "harness: expected codex, got claude") {
		t.Fatalf("codex drift = %#v", codex)
	}
	if pluginSurface.BinaryPath != filepath.Join(plugin, "bin", "claude-gatekeeper") {
		t.Fatalf("plugin binary = %q", pluginSurface.BinaryPath)
	}
	if len(pluginSurface.Drift) != 0 {
		t.Fatalf("plugin drift = %#v", pluginSurface.Drift)
	}
	if report.OK {
		t.Fatal("report should not be OK when a surface has drift")
	}
}

func TestCollectExcludesStalePluginCacheVersions(t *testing.T) {
	home := t.TempDir()
	live := filepath.Join(home, ".claude", "plugins", "cache", "market", "claude-gatekeeper", "2.0.0")
	stale := filepath.Join(home, ".claude", "plugins", "cache", "market", "claude-gatekeeper", "1.0.0")
	writeFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), `{"version":2,"plugins":{"claude-gatekeeper@market":[{"installPath":"`+live+`"}]}}`, 0644)
	for _, root := range []string{live, stale} {
		writeFile(t, filepath.Join(root, "hooks", "hooks.json"), `{"hooks":{"PreToolUse":[{"hooks":[{"command":"${CLAUDE_PLUGIN_ROOT}/bin/run.sh"}]}]}}`, 0644)
		writeFile(t, filepath.Join(root, "bin", "claude-gatekeeper"), "binary", 0755)
	}
	report, err := Collect(Options{Home: home, ExpectedVersion: "2.0.0", VersionProbe: func(string) (string, error) { return "2.0.0", nil }})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Surfaces) != 1 || !strings.Contains(report.Surfaces[0].ConfigPath, "2.0.0") {
		t.Fatalf("surfaces = %#v", report.Surfaces)
	}
}

func TestCollectIgnoresNonGatekeeperHooksAndMissingSurfaces(t *testing.T) {
	home := t.TempDir()
	writeHook(t, filepath.Join(home, ".claude", "settings.json"), "/opt/other-hook")
	report, err := Collect(Options{Home: home, ExpectedBinary: "/bin/claude-gatekeeper", ExpectedVersion: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Surfaces) != 0 || !report.OK {
		t.Fatalf("report = %#v", report)
	}
}

func TestWriteJSONAndTable(t *testing.T) {
	report := Report{OK: true, ExpectedVersion: "1.0.0", Surfaces: []Surface{{
		Kind: "grok-global", ConfigPath: "/home/me/.grok/hooks/gatekeeper.json",
		BinaryPath: "/home/me/bin/claude-gatekeeper", Version: "1.0.0", Harness: "grok",
	}}}
	var jsonOut strings.Builder
	if err := WriteJSON(&jsonOut, report); err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal([]byte(jsonOut.String()), &decoded); err != nil || !decoded.OK {
		t.Fatalf("json = %q, err = %v", jsonOut.String(), err)
	}
	var table strings.Builder
	WriteTable(&table, report)
	for _, want := range []string{"SURFACE", "grok-global", "1.0.0", "OK"} {
		if !strings.Contains(table.String(), want) {
			t.Errorf("table missing %q:\n%s", want, table.String())
		}
	}
}

func writeHook(t *testing.T, path, command string) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command}}}}}})
	writeFile(t, path, string(data), 0644)
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
