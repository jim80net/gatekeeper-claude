package policytest_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jim80net/claude-gatekeeper/internal/policytest"
)

func TestLoadAndRunTOML(t *testing.T) {
	dir := t.TempDir()
	testsPath := filepath.Join(dir, "cases.toml")
	configPath := filepath.Join(dir, "gatekeeper.toml")
	mustWrite(t, configPath, `[[rules]]
tool = 'Bash'
input = '^danger$'
decision = 'deny'
reason = 'blocked danger'
`)
	mustWrite(t, testsPath, `[[cases]]
name = 'deny danger'
tool = 'Bash'
command = 'danger'
expected = 'deny'
expected_reason = 'blocked danger'

[[cases]]
name = 'unknown abstains'
tool = 'Bash'
input = 'unknown'
expected = 'abstain'
`)
	f, err := policytest.LoadFile(testsPath)
	if err != nil {
		t.Fatal(err)
	}
	results, err := policytest.Run(f, configPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !policytest.Passed(results) {
		t.Fatalf("results = %+v", results)
	}
	var out bytes.Buffer
	if err := policytest.WriteTable(&out, results); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "PASS") || !strings.Contains(out.String(), "blocked danger") {
		t.Fatalf("table = %q", out.String())
	}
}

func TestLoadJSONAndDetectFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cases.json")
	mustWrite(t, path, `{"cases":[{"name":"wrong","tool":"Bash","input":"anything","expected":"allow"}]}`)
	f, err := policytest.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "gatekeeper.toml")
	mustWrite(t, configPath, "")
	results, err := policytest.Run(f, configPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	if policytest.Passed(results) || results[0].FailureMessage == "" {
		t.Fatalf("results = %+v", results)
	}
}

func TestValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	mustWrite(t, path, "[[cases]]\ntool='Read'\ncommand='x'\nexpected='maybe'\n")
	if _, err := policytest.LoadFile(path); err == nil {
		t.Fatal("expected validation error")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
