// Package inventory inspects live gatekeeper hook registrations without modifying them.
package inventory

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
)

const binaryName = "claude-gatekeeper"

type Options struct {
	Home            string
	ExpectedBinary  string
	ExpectedVersion string
	VersionProbe    func(string) (string, error)
}

type Report struct {
	OK              bool      `json:"ok"`
	ExpectedBinary  string    `json:"expected_binary"`
	ExpectedVersion string    `json:"expected_version"`
	Surfaces        []Surface `json:"surfaces"`
}

type Surface struct {
	Kind            string   `json:"surface"`
	ConfigPath      string   `json:"config_path"`
	Command         string   `json:"command"`
	BinaryPath      string   `json:"binary_path"`
	Version         string   `json:"version,omitempty"`
	Harness         string   `json:"harness"`
	ExpectedBinary  string   `json:"expected_binary"`
	ExpectedHarness string   `json:"expected_harness"`
	Drift           []string `json:"drift"`
}

type candidate struct {
	kind, path, expectedHarness, pluginRoot string
}

func Collect(opts Options) (Report, error) {
	if opts.Home == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Report{}, fmt.Errorf("determine home: %w", err)
		}
		opts.Home = home
	}
	if opts.VersionProbe == nil {
		opts.VersionProbe = probeVersion
	}
	report := Report{OK: true, ExpectedBinary: cleanPath(opts.ExpectedBinary), ExpectedVersion: opts.ExpectedVersion}
	candidates := []candidate{
		{"grok-global", filepath.Join(opts.Home, ".grok", "hooks", "gatekeeper.json"), "grok", ""},
		{"codex-global", filepath.Join(opts.Home, ".codex", "hooks.json"), "codex", ""},
	}
	settingsPaths, err := filepath.Glob(filepath.Join(opts.Home, ".claude", "settings*.json"))
	if err != nil {
		return Report{}, fmt.Errorf("find Claude settings: %w", err)
	}
	for _, path := range settingsPaths {
		candidates = append(candidates, candidate{"claude-settings", path, "claude", ""})
	}
	pluginRoots, err := installedPluginRoots(opts.Home)
	if err != nil {
		return Report{}, err
	}
	for _, root := range pluginRoots {
		candidates = append(candidates, candidate{"claude-plugin", filepath.Join(root, "hooks", "hooks.json"), "claude", root})
	}
	seen := map[string]bool{}
	for _, c := range candidates {
		commands, err := commandsFromFile(c.path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return Report{}, fmt.Errorf("read %s: %w", c.path, err)
		}
		for _, command := range commands {
			if !referencesGatekeeper(command) && !strings.Contains(command, "${CLAUDE_PLUGIN_ROOT}/bin/run.sh") {
				continue
			}
			key := c.path + "\x00" + command
			if seen[key] {
				continue
			}
			seen[key] = true
			s := inspect(c, command, opts)
			if len(s.Drift) > 0 {
				report.OK = false
			}
			report.Surfaces = append(report.Surfaces, s)
		}
	}
	sort.Slice(report.Surfaces, func(i, j int) bool {
		if report.Surfaces[i].Kind == report.Surfaces[j].Kind {
			return report.Surfaces[i].ConfigPath < report.Surfaces[j].ConfigPath
		}
		return report.Surfaces[i].Kind < report.Surfaces[j].Kind
	})
	return report, nil
}

// installedPluginRoots reads Claude Code's live-install registry. Cached older
// versions are deliberately excluded: their hook manifests are not active.
func installedPluginRoots(home string) ([]string, error) {
	path := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var registry struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &registry); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var roots []string
	for _, installs := range registry.Plugins {
		for _, install := range installs {
			if install.InstallPath != "" {
				roots = append(roots, cleanPath(install.InstallPath))
			}
		}
	}
	return roots, nil
}

func inspect(c candidate, command string, opts Options) Surface {
	fields := strings.Fields(command)
	binary := ""
	if len(fields) > 0 {
		binary = strings.Trim(fields[0], `"'`)
	}
	if c.pluginRoot != "" {
		binary = filepath.Join(c.pluginRoot, "bin", binaryName)
	} else {
		binary = expandHome(binary, opts.Home)
	}
	harness := "claude"
	for i := 0; i < len(fields); i++ {
		if fields[i] == "--harness" && i+1 < len(fields) {
			harness = fields[i+1]
		}
		if strings.HasPrefix(fields[i], "--harness=") {
			harness = strings.TrimPrefix(fields[i], "--harness=")
		}
	}
	expectedBinary := cleanPath(opts.ExpectedBinary)
	if c.pluginRoot != "" {
		expectedBinary = filepath.Join(c.pluginRoot, "bin", binaryName)
	}
	s := Surface{Kind: c.kind, ConfigPath: c.path, Command: command, BinaryPath: cleanPath(binary), Harness: harness, ExpectedBinary: expectedBinary, ExpectedHarness: c.expectedHarness, Drift: []string{}}
	if s.Harness != c.expectedHarness {
		s.Drift = append(s.Drift, fmt.Sprintf("harness: expected %s, got %s", c.expectedHarness, s.Harness))
	}
	if expectedBinary != "" && s.BinaryPath != expectedBinary {
		s.Drift = append(s.Drift, fmt.Sprintf("binary: expected %s", expectedBinary))
	}
	if _, err := os.Stat(s.BinaryPath); err != nil {
		s.Drift = append(s.Drift, "binary: "+err.Error())
		return s
	}
	ver, err := opts.VersionProbe(s.BinaryPath)
	if err != nil {
		s.Drift = append(s.Drift, "version: "+err.Error())
		return s
	}
	s.Version = ver
	if opts.ExpectedVersion != "" && ver != opts.ExpectedVersion {
		s.Drift = append(s.Drift, fmt.Sprintf("version: expected %s, got %s", opts.ExpectedVersion, ver))
	}
	return s
}

func commandsFromFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	hooks, _ := root["hooks"].(map[string]any)
	entries, _ := hooks["PreToolUse"].([]any)
	var commands []string
	for _, entry := range entries {
		m, _ := entry.(map[string]any)
		inner, _ := m["hooks"].([]any)
		for _, hook := range inner {
			hm, _ := hook.(map[string]any)
			if command, ok := hm["command"].(string); ok {
				commands = append(commands, command)
			}
		}
	}
	return commands, nil
}

func referencesGatekeeper(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	return strings.TrimSuffix(filepath.Base(strings.Trim(fields[0], `"'`)), ".exe") == binaryName
}

func probeVersion(path string) (string, error) {
	cmd := exec.Command(path, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(string(out))
	return strings.TrimSpace(strings.TrimPrefix(text, binaryName)), nil
}

func WriteJSON(w io.Writer, report Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func WriteTable(w io.Writer, report Report) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SURFACE\tCONFIG\tHARNESS\tVERSION\tBINARY\tDRIFT")
	for _, s := range report.Surfaces {
		drift := "OK"
		if len(s.Drift) > 0 {
			drift = strings.Join(s.Drift, "; ")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", s.Kind, s.ConfigPath, s.Harness, emptyDash(s.Version), s.BinaryPath, drift)
	}
	_ = tw.Flush()
	if len(report.Surfaces) == 0 {
		fmt.Fprintln(w, "No live gatekeeper hook surfaces found.")
	}
}

func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}
func cleanPath(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}
func emptyDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
