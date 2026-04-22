package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/selfhost"
)

// captureStdouterr redirects os.Stdout + os.Stderr to io.Discard for
// the duration of fn, so subcommand bodies that call printDiags /
// printTypes don't pollute go test output when the test only cares
// about the astbridge counter.
func captureStdouterr(t *testing.T, fn func()) {
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
	defer func() {
		os.Stdout = origStdout
		os.Stderr = origStderr
	}()
	drained := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(io.Discard, rout); drained <- struct{}{} }()
	go func() { _, _ = io.Copy(io.Discard, rerr); drained <- struct{}{} }()
	fn()
	_ = wout.Close()
	_ = werr.Close()
	<-drained
	<-drained
}

// warmStdlibCachesForBaseline is the shared pre-step for the legacy
// baseline assertions: run the legacy path once before taking a
// measurement so stdlib/canonical/checker caches stabilize. On a
// cold process the first run bumps the astbridge counter for every
// lazily-parsed stdlib module (hundreds of bumps on a trivial
// source); subsequent runs see those already-parsed and only lower
// the user-file AST. Measuring the warm state is what "baseline" is
// supposed to capture.
func warmStdlibCachesForBaseline(t *testing.T, path string, src []byte, flags cliFlags) {
	t.Helper()
	formatter := newFormatter(path, src, flags)
	captureStdouterr(t, func() {
		_ = runCheckFileLegacy(path, src, formatter, flags)
		_ = runTypecheckFileLegacy(path, src, formatter, flags)
	})
}

// TestRunCheckFileLegacyAstbridgeBaseline documents the CURRENT
// warm-cache astbridge cost of the non-native `osty check FILE`
// path. A cold first run bumps the counter dozens to hundreds of
// times because stdlib modules lower lazily through the Go checker;
// once those caches are warm, each subsequent check lowers exactly
// the user's own file.
//
// This is a regression floor for the warm path: native bumps 0,
// legacy bumps exactly 1 per call. The contrast is the migration
// signal — if the legacy number grows, something started
// re-lowering per call; if the native number becomes non-zero,
// something leaked an astbridge detour into the arena pipeline.
func TestRunCheckFileLegacyAstbridgeBaseline(t *testing.T) {
	src := []byte(`fn main() {
    let x = 1
    let y = x + 2
    y
}
`)
	path := filepath.Join(t.TempDir(), "main.osty")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	flags := cliFlags{noColor: true}
	warmStdlibCachesForBaseline(t, path, src, flags)

	formatter := newFormatter(path, src, flags)
	selfhost.ResetAstbridgeLowerCount()
	var exit int
	captureStdouterr(t, func() {
		exit = runCheckFileLegacy(path, src, formatter, flags)
	})
	if exit != 0 {
		t.Fatalf("runCheckFileLegacy exit = %d, want 0", exit)
	}
	const want = 1
	if got := selfhost.AstbridgeLowerCount(); got != want {
		t.Fatalf("AstbridgeLowerCount after warm runCheckFileLegacy = %d, want %d (legacy warm-path = one *ast.File lowering per user file via parser.ParseDetailed)", got, want)
	}
}

// TestRunTypecheckFileLegacyAstbridgeBaseline is the typecheck
// sibling. Same warm-cache expectation as the check baseline — one
// bump for the user file, zero for stdlib because those are
// lower-and-cached once per process.
func TestRunTypecheckFileLegacyAstbridgeBaseline(t *testing.T) {
	src := []byte(`fn main() {
    let x = 1
    let y = x + 2
    y
}
`)
	path := filepath.Join(t.TempDir(), "main.osty")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	flags := cliFlags{noColor: true}
	warmStdlibCachesForBaseline(t, path, src, flags)

	formatter := newFormatter(path, src, flags)
	selfhost.ResetAstbridgeLowerCount()
	var exit int
	captureStdouterr(t, func() {
		exit = runTypecheckFileLegacy(path, src, formatter, flags)
	})
	if exit != 0 {
		t.Fatalf("runTypecheckFileLegacy exit = %d, want 0", exit)
	}
	const want = 1
	if got := selfhost.AstbridgeLowerCount(); got != want {
		t.Fatalf("AstbridgeLowerCount after warm runTypecheckFileLegacy = %d, want %d", got, want)
	}
}
