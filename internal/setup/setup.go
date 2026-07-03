// Package setup manages the hook registration in ~/.claude/settings.json.
package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const hookCommand = "claude-gatekeeper"

// Install adds the PreToolUse hook to settings.json.
// binaryPath is the absolute path to the installed binary.
func Install(binaryPath string) error {
	settingsPath, err := settingsFilePath()
	if err != nil {
		return err
	}

	settings, err := readSettingsMap(settingsPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", settingsPath, err)
	}
	if settings == nil {
		settings = map[string]interface{}{}
	}

	// If this exact binary is already registered, nothing to do.
	if gatekeeperHookHasCommand(settings, binaryPath) {
		fmt.Fprintf(os.Stderr, "Hook already configured in %s\n", settingsPath)
		return nil
	}

	if err := backup(settingsPath); err != nil {
		return err
	}

	// Remove any existing gatekeeper hooks from a different path
	// (e.g., standalone binary being replaced by plugin version).
	if hasGatekeeperHook(settings) {
		removeGatekeeperHook(settings)
	}

	hookEntry := map[string]interface{}{
		"type":          "command",
		"command":       binaryPath,
		"timeout":       10,
		"statusMessage": "Checking permissions...",
	}

	matcherEntry := map[string]interface{}{
		"matcher": "",
		"hooks":   []interface{}{hookEntry},
	}

	hooks := getMap(settings, "hooks")
	preToolUse := getSlice(hooks, "PreToolUse")
	preToolUse = append(preToolUse, matcherEntry)
	hooks["PreToolUse"] = preToolUse
	settings["hooks"] = hooks

	if err := writeSettings(settingsPath, settings); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Hook installed in %s\n", settingsPath)
	return nil
}

// Uninstall removes the gatekeeper hook from settings.json.
func Uninstall() error {
	settingsPath, err := settingsFilePath()
	if err != nil {
		return err
	}

	settings, err := readSettingsMap(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "No settings file found at %s\n", settingsPath)
			return nil
		}
		return fmt.Errorf("reading %s: %w", settingsPath, err)
	}

	if !hasGatekeeperHook(settings) {
		fmt.Fprintf(os.Stderr, "No gatekeeper hook found in %s\n", settingsPath)
		return nil
	}

	if err := backup(settingsPath); err != nil {
		return err
	}

	removeGatekeeperHook(settings)

	if err := writeSettings(settingsPath, settings); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Hook removed from %s\n", settingsPath)
	return nil
}

// InstallGrok registers a grok-native global blocking hook at
// ~/.grok/hooks/gatekeeper.json. The schema fields
// (matcher/enabled/command_raw/timeout_ms) are grok-native (from the grok
// binary's embedded 10-hooks.md). The project folder must be /hooks-trust'd for
// grok to execute the hook. binaryPath is the absolute path to the installed
// gatekeeper binary.
func InstallGrok(binaryPath string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	hookDir := filepath.Join(homeDir, ".grok", "hooks")
	if err := os.MkdirAll(hookDir, 0755); err != nil {
		return fmt.Errorf("creating %s: %w", hookDir, err)
	}
	hookPath := filepath.Join(hookDir, "gatekeeper.json")

	if err := backup(hookPath); err != nil {
		return err
	}

	hook := map[string]interface{}{
		"matcher":     "",
		"enabled":     true,
		"command_raw": binaryPath + " --harness grok",
		"timeout_ms":  5000,
	}
	if err := writeJSONFile(hookPath, hook); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "grok hook installed in %s\n", hookPath)
	fmt.Fprintf(os.Stderr, "Run /hooks-trust on each grok project folder so the hook executes.\n")
	return nil
}

// InstallCodex registers a codex PreToolUse hook in <projectDir>/.codex/hooks.json.
// The structure is the Claude-shaped hooks config verified against a real
// .codex/hooks.json and the codex binary's embedded schema. Codex's support for
// a GLOBAL hooks config is not yet verified, so this writes the project-level
// file; run it per project until that is confirmed. An empty projectDir defaults
// to the current directory.
func InstallCodex(binaryPath, projectDir string) error {
	if projectDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("cannot determine current directory: %w", err)
		}
		projectDir = wd
	}
	hookDir := filepath.Join(projectDir, ".codex")
	if err := os.MkdirAll(hookDir, 0755); err != nil {
		return fmt.Errorf("creating %s: %w", hookDir, err)
	}
	hookPath := filepath.Join(hookDir, "hooks.json")

	if err := backup(hookPath); err != nil {
		return err
	}

	hookEntry := map[string]interface{}{
		"type":          "command",
		"command":       binaryPath + " --harness codex",
		"timeout":       10,
		"statusMessage": "Checking permissions...",
	}
	config := map[string]interface{}{
		"hooks": map[string]interface{}{
			"PreToolUse": []interface{}{
				map[string]interface{}{
					"hooks": []interface{}{hookEntry},
				},
			},
		},
	}
	if err := writeJSONFile(hookPath, config); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "codex hook installed in %s\n", hookPath)
	fmt.Fprintf(os.Stderr, "Grant hook trust (persisted, or --dangerously-bypass-hook-trust) for automation.\n")
	return nil
}

// writeJSONFile atomically writes v as indented JSON to path.
func writeJSONFile(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling JSON: %w", err)
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func settingsFilePath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(homeDir, ".claude", "settings.json"), nil
}

func readSettingsMap(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing JSON: %w", err)
	}
	return m, nil
}

func writeSettings(path string, settings map[string]interface{}) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling JSON: %w", err)
	}
	data = append(data, '\n')
	// Atomic write: write to temp file then rename to avoid corruption on crash.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func backup(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	backupPath := path + ".backup." + time.Now().Format("20060102-150405")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading for backup: %w", err)
	}
	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		return fmt.Errorf("writing backup: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Backup: %s\n", backupPath)
	return nil
}

// hasGatekeeperHook checks if a gatekeeper hook is already configured.
// Uses non-mutating peekMap/peekSlice to avoid polluting settings on read.
func hasGatekeeperHook(settings map[string]interface{}) bool {
	hooks := peekMap(settings, "hooks")
	preToolUse := peekSlice(hooks, "PreToolUse")
	for _, entry := range preToolUse {
		if m, ok := entry.(map[string]interface{}); ok {
			for _, h := range peekSlice(m, "hooks") {
				if hm, ok := h.(map[string]interface{}); ok {
					if cmd, _ := hm["command"].(string); isGatekeeperCommand(cmd) {
						return true
					}
				}
			}
		}
	}
	return false
}

// removeGatekeeperHook removes gatekeeper entries from hooks.PreToolUse.
func removeGatekeeperHook(settings map[string]interface{}) {
	hooks := getMap(settings, "hooks")
	preToolUse := getSlice(hooks, "PreToolUse")

	var filtered []interface{}
	for _, entry := range preToolUse {
		m, ok := entry.(map[string]interface{})
		if !ok {
			filtered = append(filtered, entry)
			continue
		}
		innerHooks := getSlice(m, "hooks")
		var kept []interface{}
		for _, h := range innerHooks {
			hm, ok := h.(map[string]interface{})
			if !ok {
				kept = append(kept, h)
				continue
			}
			cmd, _ := hm["command"].(string)
			if !isGatekeeperCommand(cmd) {
				kept = append(kept, h)
			}
		}
		if len(kept) > 0 {
			m["hooks"] = kept
			filtered = append(filtered, m)
		}
		// If no hooks left in this matcher block, drop the whole block.
	}

	if len(filtered) > 0 {
		hooks["PreToolUse"] = filtered
	} else {
		delete(hooks, "PreToolUse")
	}

	if len(hooks) > 0 {
		settings["hooks"] = hooks
	} else {
		delete(settings, "hooks")
	}
}

// gatekeeperHookHasCommand checks if a gatekeeper hook with the exact command path exists.
func gatekeeperHookHasCommand(settings map[string]interface{}, command string) bool {
	hooks := peekMap(settings, "hooks")
	preToolUse := peekSlice(hooks, "PreToolUse")
	for _, entry := range preToolUse {
		if m, ok := entry.(map[string]interface{}); ok {
			for _, h := range peekSlice(m, "hooks") {
				if hm, ok := h.(map[string]interface{}); ok {
					if cmd, _ := hm["command"].(string); cmd == command {
						return true
					}
				}
			}
		}
	}
	return false
}

func isGatekeeperCommand(cmd string) bool {
	if cmd == "" {
		return false
	}
	// The command field may include flags: "/path/to/claude-gatekeeper --debug"
	fields := strings.Fields(cmd)
	return filepath.Base(fields[0]) == hookCommand
}

// getMap returns settings[key] as a map, creating it if absent.
func getMap(m map[string]interface{}, key string) map[string]interface{} {
	if v, ok := m[key].(map[string]interface{}); ok {
		return v
	}
	v := map[string]interface{}{}
	m[key] = v
	return v
}

// getSlice returns m[key] as a slice, returning nil if absent.
func getSlice(m map[string]interface{}, key string) []interface{} {
	if v, ok := m[key].([]interface{}); ok {
		return v
	}
	return nil
}

// peekMap returns settings[key] as a map without mutating the parent.
func peekMap(m map[string]interface{}, key string) map[string]interface{} {
	if v, ok := m[key].(map[string]interface{}); ok {
		return v
	}
	return nil
}

// peekSlice returns m[key] as a slice without mutating the parent.
func peekSlice(m map[string]interface{}, key string) []interface{} {
	return getSlice(m, key) // getSlice is already non-mutating
}
