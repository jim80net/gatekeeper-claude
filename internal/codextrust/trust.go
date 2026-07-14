// Package codextrust verifies Codex CLI's persisted hook trust state.
package codextrust

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Status describes whether a hook's current normalized identity is trusted.
type Status struct {
	Key         string
	CurrentHash string
	TrustedHash string
}

// Trusted reports whether the persisted hash matches the current hook identity.
func (s Status) Trusted() bool { return s.TrustedHash == s.CurrentHash }

type hooksFile struct {
	Hooks map[string][]matcherGroup `json:"hooks"`
}

type matcherGroup struct {
	Matcher *string       `json:"matcher,omitempty"`
	Hooks   []commandHook `json:"hooks"`
}

type commandHook struct {
	Type           string  `json:"type"`
	Command        string  `json:"command"`
	CommandWindows *string `json:"commandWindows,omitempty"`
	Timeout        *uint64 `json:"timeout,omitempty"`
	Async          bool    `json:"async"`
	StatusMessage  *string `json:"statusMessage,omitempty"`
}

type configFile struct {
	Hooks struct {
		State map[string]struct {
			TrustedHash string `toml:"trusted_hash"`
		} `toml:"state"`
	} `toml:"hooks"`
}

// Inspect finds command in hookPath and compares its normalized identity with
// the trust state persisted in <home>/.codex/config.toml.
func Inspect(home, hookPath, command string) (Status, error) {
	data, err := os.ReadFile(hookPath)
	if err != nil {
		return Status{}, fmt.Errorf("read Codex hooks %s: %w", hookPath, err)
	}
	var file hooksFile
	if err := json.Unmarshal(data, &file); err != nil {
		return Status{}, fmt.Errorf("parse Codex hooks %s: %w", hookPath, err)
	}
	groups := file.Hooks["PreToolUse"]
	for groupIndex, group := range groups {
		for handlerIndex, handler := range group.Hooks {
			if handler.Type != "command" || handler.Command != command {
				continue
			}
			absoluteHookPath, err := filepath.Abs(hookPath)
			if err != nil {
				return Status{}, fmt.Errorf("resolve Codex hook path %s: %w", hookPath, err)
			}
			key := fmt.Sprintf("%s:pre_tool_use:%d:%d", filepath.Clean(absoluteHookPath), groupIndex, handlerIndex)
			currentHash, err := hashPreToolUse(group.Matcher, handler)
			if err != nil {
				return Status{}, err
			}
			cfgData, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
			if err != nil && !os.IsNotExist(err) {
				return Status{}, fmt.Errorf("read Codex config: %w", err)
			}
			var cfg configFile
			if len(cfgData) > 0 {
				if _, err := toml.Decode(string(cfgData), &cfg); err != nil {
					return Status{}, fmt.Errorf("parse Codex config: %w", err)
				}
			}
			return Status{Key: key, CurrentHash: currentHash, TrustedHash: cfg.Hooks.State[key].TrustedHash}, nil
		}
	}
	return Status{}, fmt.Errorf("Codex hook command %q not found in %s", command, hookPath)
}

func hashPreToolUse(matcher *string, handler commandHook) (string, error) {
	timeout := uint64(600)
	if handler.Timeout != nil {
		timeout = *handler.Timeout
	}
	if timeout < 1 {
		timeout = 1
	}
	normalized := map[string]interface{}{
		"event_name": "pre_tool_use",
		"hooks": []interface{}{map[string]interface{}{
			"type": handler.Type, "command": handler.Command,
			"timeout": timeout, "async": handler.Async,
		}},
	}
	if matcher != nil {
		normalized["matcher"] = *matcher
	}
	hook := normalized["hooks"].([]interface{})[0].(map[string]interface{})
	if handler.CommandWindows != nil {
		hook["commandWindows"] = *handler.CommandWindows
	}
	if handler.StatusMessage != nil {
		hook["statusMessage"] = *handler.StatusMessage
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("marshal normalized Codex hook: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
