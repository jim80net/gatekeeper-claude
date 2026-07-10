// Package adapter defines the per-harness SPI that translates a harness's
// native hook wire (stdin JSON in, stdout JSON + exit code out) to and from the
// harness-agnostic canonical types.
//
// One policy (the gatekeeper.toml rules) is evaluated by the engine over a
// canonical.ToolCall and produces a canonical.Verdict. Each adapter is
// responsible for two translations:
//
//   - ParseInput: harness-native stdin JSON -> canonical.ToolCall, normalising
//     the harness's tool taxonomy onto the canonical tool names so the same
//     rules apply to every harness.
//   - Encode: canonical.Verdict -> harness-native stdout bytes + process exit
//     code, including the harness-correct encoding of an ABSTAIN (no verdict).
//
// The abstain encoding is the load-bearing, harness-specific part: Claude and
// Codex express "no opinion" by writing nothing and exiting 0 (their native
// permission flow then runs); grok has no first-class defer, so its adapter
// routes abstain through grok's documented fail-open path (see the grok
// package). No adapter may encode an abstain as an authoritative allow.
package adapter

import (
	"fmt"
	"io"

	"github.com/jim80net/claude-gatekeeper/internal/adapter/claude"
	"github.com/jim80net/claude-gatekeeper/internal/adapter/codex"
	"github.com/jim80net/claude-gatekeeper/internal/adapter/grok"
	"github.com/jim80net/gatekeeper-core/canonical"
)

// Adapter is the per-harness wire translator SPI.
type Adapter interface {
	// Name returns the harness name ("claude", "codex", "grok").
	Name() string
	// ParseInput reads the harness-native hook stdin and returns a canonical
	// tool call. It returns an error when the stdin cannot be parsed; the
	// caller then applies the on_error posture.
	ParseInput(r io.Reader) (*canonical.ToolCall, error)
	// Encode writes the harness-native representation of the verdict to w and
	// returns the process exit code. It handles allow, deny, and abstain.
	Encode(w io.Writer, v canonical.Verdict) (exitCode int, err error)
}

// For returns the adapter for the named harness. An empty name defaults to
// "claude" for backward compatibility with existing installs that invoke the
// hook without a --harness flag.
func For(name string) (Adapter, error) {
	switch name {
	case "", "claude":
		return claude.New(), nil
	case "codex":
		return codex.New(), nil
	case "grok":
		return grok.New(), nil
	default:
		return nil, fmt.Errorf("unknown harness %q (want claude|codex|grok)", name)
	}
}
