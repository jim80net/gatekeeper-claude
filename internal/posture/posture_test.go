package posture_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jim80net/claude-gatekeeper/internal/posture"
	"github.com/jim80net/gatekeeper-core/canonical"
)

func TestResolve(t *testing.T) {
	for _, tc := range []struct {
		name     string
		set      bool
		value    string
		active   bool
		decision canonical.Decision
		warning  bool
	}{
		{"absent is compatible", false, "", false, canonical.Abstain, false},
		{"deny", true, "deny", true, canonical.Deny, false},
		{"abstain", true, "abstain", true, canonical.Abstain, false},
		{"invalid fails closed", true, "denny", true, canonical.Deny, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(posture.EnvOnError, tc.value)
			} else {
				os.Unsetenv(posture.EnvOnError)
				t.Cleanup(func() { os.Unsetenv(posture.EnvOnError) })
			}
			got := posture.Resolve()
			if got.Active != tc.active || got.Decision != tc.decision || (got.Warning != "") != tc.warning {
				t.Fatalf("Resolve() = %+v", got)
			}
		})
	}
}

func TestConfigWarningsEmptyXDGShadow(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if err := os.MkdirAll(filepath.Join(xdg, "gatekeeper"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "gatekeeper", "gatekeeper.toml"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "gatekeeper.toml"), []byte("on_error='deny'"), 0644); err != nil {
		t.Fatal(err)
	}
	warnings := posture.ConfigWarnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "shadows non-empty legacy config") {
		t.Fatalf("warnings = %#v", warnings)
	}
}
