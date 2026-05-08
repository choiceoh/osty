package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLegacyGlobals_FlagOnEmitsW0750 pins the end-to-end wiring: a DIR
// `osty check` invocation with `--legacy-globals` set must surface the
// W0750 deprecation warning at every legacy-global call site without
// failing the build (Warning severity → exit 0). This is the
// authoritative front-end behaviour test for the v0.6.x transition
// flag.
func TestLegacyGlobals_FlagOnEmitsW0750(t *testing.T) {
	dir := t.TempDir()
	src := []byte(`fn main() {
    let _ = time.now()
    let _ = fs.read("/etc/hosts")
}
`)
	if err := os.WriteFile(filepath.Join(dir, "main.osty"), src, 0o644); err != nil {
		t.Fatal(err)
	}

	stderr := captureStderr(t, func() int {
		flags := cliFlags{noColor: true, native: true, legacyGlobals: true}
		return runCheckPackageNative(dir, flags)
	})

	if c := strings.Count(stderr, "warning[W0750]"); c != 2 {
		t.Fatalf("W0750 warning count in stderr = %d, want 2\nstderr:\n%s", c, stderr)
	}
	if !strings.Contains(stderr, "time.now") {
		t.Errorf("stderr missing `time.now` reference:\n%s", stderr)
	}
	if !strings.Contains(stderr, "fs.read") {
		t.Errorf("stderr missing `fs.read` reference:\n%s", stderr)
	}
	if !strings.Contains(stderr, "Clock") {
		t.Errorf("stderr missing `Clock` capability hint:\n%s", stderr)
	}
}

// TestLegacyGlobals_FlagOffStaysSilent guarantees the default
// behaviour: without `--legacy-globals`, no W0750 should fire at any
// legacy-global call site. This pins the opt-in contract — v0.6.0
// users that don't pass the flag should not see migration noise.
func TestLegacyGlobals_FlagOffStaysSilent(t *testing.T) {
	dir := t.TempDir()
	src := []byte(`fn main() {
    let _ = time.now()
}
`)
	if err := os.WriteFile(filepath.Join(dir, "main.osty"), src, 0o644); err != nil {
		t.Fatal(err)
	}

	stderr := captureStderr(t, func() int {
		// No legacyGlobals: false default — explicitly omit it.
		flags := cliFlags{noColor: true, native: true}
		return runCheckPackageNative(dir, flags)
	})
	if strings.Contains(stderr, "W0750") {
		t.Fatalf("stderr emitted W0750 without --legacy-globals:\n%s", stderr)
	}
}

// captureStderr redirects os.Stderr to a pipe, runs body, and returns
// what was written. Mirrors the pipe pattern from
// check_native_test.go but encapsulated so the legacy-globals tests
// stay readable. Stdout is silenced to keep the suite quiet.
func captureStderr(t *testing.T, body func() int) string {
	t.Helper()
	origStdout := os.Stdout
	origStderr := os.Stderr
	rout, wout, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	rerr, werr, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}
	os.Stdout = wout
	os.Stderr = werr
	t.Cleanup(func() {
		os.Stdout = origStdout
		os.Stderr = origStderr
	})
	drained := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(io.Discard, rout); drained <- struct{}{} }()
	stderrCh := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(rerr)
		stderrCh <- string(b)
		drained <- struct{}{}
	}()

	_ = body()

	_ = wout.Close()
	_ = werr.Close()
	<-drained
	<-drained
	return <-stderrCh
}
