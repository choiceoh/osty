package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runOsty invokes the shared cmd/osty test binary with the given args.
// Returning combined stdout+stderr keeps the test terse — every assertion
// in this file checks substring presence, not stream-of-origin.
func runOsty(t *testing.T, dir string, args ...string) (combined string, exitCode int) {
	t.Helper()
	cmd := exec.Command(buildOstyBinary(t), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=") // ensure -mod is unset
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return out, exitErr.ExitCode()
	}
	if err != nil {
		t.Fatalf("go run osty: %v\n%s", err, out)
	}
	return out, 0
}

// repoRoot finds the cmd/osty package directory. Tests in this
// package run with the cwd already set to it, so we just return ".".
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	return wd
}

// TestCiCleanProjectExits0 is the canonical happy path: a fully
// formatted project with a license set produces zero failures.
func TestCiCleanProjectExits0(t *testing.T) {
	if testing.Short() {
		t.Skip("CLI integration test (slow)")
	}
	dir := t.TempDir()
	mustWrite(t, dir, "osty.toml", `[package]
name = "demo"
version = "0.1.0"
edition = "0.3"
license = "MIT"
description = "demo"
`)
	mustWrite(t, dir, "lib.osty", "pub fn hello() {}\n")

	out, code := runOsty(t, dir, "ci", dir)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d. output:\n%s", code, out)
	}
	if !strings.Contains(out, "PASS  format") {
		t.Errorf("missing PASS format in output:\n%s", out)
	}
	if !strings.Contains(out, "osty ci: OK") {
		t.Errorf("missing OK summary:\n%s", out)
	}
}

// TestCiUnformattedProjectExits1 verifies the FAIL path — the
// CLI must propagate non-zero exit when any check has errors.
func TestCiUnformattedProjectExits1(t *testing.T) {
	if testing.Short() {
		t.Skip("CLI integration test (slow)")
	}
	dir := t.TempDir()
	mustWrite(t, dir, "osty.toml", `[package]
name = "demo"
version = "0.1.0"
edition = "0.3"
license = "MIT"
`)
	// Indented declaration is non-canonical — format check fails.
	mustWrite(t, dir, "lib.osty", "    pub fn bad() {}\n")

	out, code := runOsty(t, dir, "ci", dir)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d. output:\n%s", code, out)
	}
	if !strings.Contains(out, "FAIL  format") {
		t.Errorf("missing FAIL format in output:\n%s", out)
	}
	if !strings.Contains(out, "CI003") {
		t.Errorf("missing CI003 diagnostic code:\n%s", out)
	}
}

// TestCiJSONOutput verifies --json produces a parseable Report
// object. Downstream tooling (CI dashboards, release-note bots)
// is the only consumer of the JSON format, so we only assert on
// the structural shape, not the formatted text.
func TestCiJSONOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("CLI integration test (slow)")
	}
	dir := t.TempDir()
	mustWrite(t, dir, "osty.toml", `[package]
name = "demo"
version = "0.1.0"
edition = "0.3"
license = "MIT"
`)
	mustWrite(t, dir, "lib.osty", "pub fn hello() {}\n")

	out, code := runOsty(t, dir, "--json", "ci", dir)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d. output:\n%s", code, out)
	}
	// The JSON object is on stdout; PASS lines are still on
	// stderr in the human renderer but JSON mode skips them. The
	// captured `out` interleaves both. Split on the first '{' to
	// find the JSON.
	jsonStart := strings.Index(out, "{")
	if jsonStart < 0 {
		t.Fatalf("no JSON in output:\n%s", out)
	}
	var rep struct {
		Checks []struct {
			Name    string `json:"name"`
			Passed  bool   `json:"passed"`
			Skipped bool   `json:"skipped"`
		} `json:"checks"`
	}
	dec := json.NewDecoder(strings.NewReader(out[jsonStart:]))
	if err := dec.Decode(&rep); err != nil {
		t.Fatalf("decode: %v\nbody=%s", err, out[jsonStart:])
	}
	if len(rep.Checks) == 0 {
		t.Fatalf("no checks in report: %+v", rep)
	}
	hasFormat := false
	for _, c := range rep.Checks {
		if c.Name == "format" {
			hasFormat = true
		}
	}
	if !hasFormat {
		t.Errorf("format check missing from JSON report: %+v", rep.Checks)
	}
}

func TestFmtAutoRepairWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("CLI integration test (slow)")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte("function main(){ console.log(null); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runOsty(t, dir, "fmt", "--write", path)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d. output:\n%s", code, out)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "fn main() {\n    println(None)\n}\n"
	if string(got) != want {
		t.Fatalf("formatted repair mismatch:\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestFmtNoRepairLeavesParserStrict(t *testing.T) {
	if testing.Short() {
		t.Skip("CLI integration test (slow)")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte("function main(){ console.log(null); }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, code := runOsty(t, dir, "fmt", "--no-repair", "--write", path)
	if code == 0 {
		t.Fatalf("expected non-zero exit without repair. output:\n%s", out)
	}
}

func mustWrite(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
