// Package posture resolves the gatekeeper error posture independently of the
// policy file whose absence or corruption may itself be the error.
package posture

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jim80net/gatekeeper-core/canonical"
)

// EnvOnError is the independent error-posture environment variable.
const EnvOnError = "GATEKEEPER_ON_ERROR"

// Override is an independently resolved error posture. Active is false when
// the variable is absent, preserving the legacy TOML/default behavior.
type Override struct {
	Active   bool
	Decision canonical.Decision
	Warning  string
}

// Resolve reads EnvOnError. Invalid non-empty values fail closed so a typo in
// a requested hard posture cannot silently widen authority.
func Resolve() Override {
	value, ok := os.LookupEnv(EnvOnError)
	if !ok || value == "" {
		return Override{}
	}
	switch value {
	case "deny":
		return Override{Active: true, Decision: canonical.Deny}
	case "abstain":
		return Override{Active: true, Decision: canonical.Abstain}
	default:
		return Override{
			Active:   true,
			Decision: canonical.Deny,
			Warning:  fmt.Sprintf("invalid %s=%q; failing closed (want deny|abstain)", EnvOnError, value),
		}
	}
}

// ConfigWarnings returns configuration-selection hazards that must remain
// visible even when debug logging is disabled.
func ConfigWarnings() []string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return nil
	}
	xdgRoot := os.Getenv("XDG_CONFIG_HOME")
	if xdgRoot == "" {
		xdgRoot = filepath.Join(home, ".config")
	}
	xdg := filepath.Join(xdgRoot, "gatekeeper", "gatekeeper.toml")
	info, err := os.Stat(xdg)
	if err != nil || info.Size() != 0 {
		return nil
	}
	legacy := filepath.Join(home, ".claude", "gatekeeper.toml")
	message := fmt.Sprintf("empty XDG config %s is selected and contains zero rules", xdg)
	if legacyInfo, legacyErr := os.Stat(legacy); legacyErr == nil && legacyInfo.Size() > 0 {
		message += fmt.Sprintf("; it shadows non-empty legacy config %s", legacy)
	}
	return []string{strings.TrimSpace(message)}
}
