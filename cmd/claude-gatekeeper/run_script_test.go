package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunScriptBootstrapFailurePostures(t *testing.T) {
	tests := []struct {
		name        string
		global      string
		project     string
		escape      bool
		wantExit    int
		wantPosture string
	}{
		{name: "missing config defaults deny", wantExit: 2, wantPosture: "default deny"},
		{name: "configured deny", global: `on_error = "deny"`, wantExit: 2, wantPosture: `on_error = "deny"`},
		{name: "configured abstain", global: `on_error = "abstain"`, wantExit: 0, wantPosture: `on_error = "abstain"`},
		{name: "project overrides global", global: `on_error = "deny"`, project: `on_error = "abstain"`, wantExit: 0, wantPosture: `on_error = "abstain"`},
		{name: "explicit bootstrap escape hatch", global: `on_error = "deny"`, escape: true, wantExit: 0, wantPosture: "GATEKEEPER_BOOTSTRAP_ABSTAIN=1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			project := filepath.Join(root, "project")
			home := filepath.Join(root, "home")
			fakeBin := filepath.Join(root, "fake-bin")
			for _, dir := range []string{filepath.Join(root, "plugin", "bin"), project, filepath.Join(home, ".claude"), fakeBin} {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			runScript, err := os.ReadFile("../../bin/run.sh")
			if err != nil {
				t.Fatal(err)
			}
			writeExecutable(t, filepath.Join(root, "plugin", "bin", "run.sh"), string(runScript))
			writeExecutable(t, filepath.Join(root, "plugin", "bin", "install.sh"), "#!/bin/sh\necho download-attempt >&2\nexit 1\n")
			writeExecutable(t, filepath.Join(fakeBin, "go"), "#!/bin/sh\necho build-attempt >&2\nexit 1\n")
			if tc.global != "" {
				if err := os.WriteFile(filepath.Join(home, ".claude", "gatekeeper.toml"), []byte(tc.global+"\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.project != "" {
				if err := os.MkdirAll(filepath.Join(project, ".gatekeeper"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(project, ".gatekeeper", "gatekeeper.toml"), []byte(tc.project+"\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}

			cmd := exec.Command(filepath.Join(root, "plugin", "bin", "run.sh"))
			cmd.Dir = project
			cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "PATH="+fakeBin+":"+os.Getenv("PATH"))
			if tc.escape {
				cmd.Env = append(cmd.Env, "GATEKEEPER_BOOTSTRAP_ABSTAIN=1")
			}
			output, runErr := cmd.CombinedOutput()
			gotExit := 0
			if exitErr, ok := runErr.(*exec.ExitError); ok {
				gotExit = exitErr.ExitCode()
			} else if runErr != nil {
				t.Fatalf("run wrapper: %v", runErr)
			}
			if gotExit != tc.wantExit {
				t.Errorf("exit = %d, want %d; output:\n%s", gotExit, tc.wantExit, output)
			}
			for _, want := range []string{"download-attempt", "build-attempt", tc.wantPosture} {
				if !strings.Contains(string(output), want) {
					t.Errorf("output missing %q:\n%s", want, output)
				}
			}
			if _, err := os.Stat(filepath.Join(root, "plugin", "bin", "claude-gatekeeper")); !os.IsNotExist(err) {
				t.Fatalf("test unexpectedly produced binary: %v", err)
			}
		})
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
}
