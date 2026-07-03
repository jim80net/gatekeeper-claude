package adapter_test

import (
	"testing"

	"github.com/jim80net/claude-gatekeeper/internal/adapter"
)

func TestForKnownHarnesses(t *testing.T) {
	tests := []struct {
		arg  string
		want string
	}{
		{"", "claude"}, // empty defaults to claude for back-compat
		{"claude", "claude"},
		{"codex", "codex"},
		{"grok", "grok"},
	}
	for _, tt := range tests {
		t.Run(tt.arg, func(t *testing.T) {
			a, err := adapter.For(tt.arg)
			if err != nil {
				t.Fatalf("For(%q): %v", tt.arg, err)
			}
			if a.Name() != tt.want {
				t.Errorf("Name = %q, want %q", a.Name(), tt.want)
			}
		})
	}
}

func TestForUnknownHarness(t *testing.T) {
	if _, err := adapter.For("emacs"); err == nil {
		t.Error("expected error for unknown harness")
	}
}
