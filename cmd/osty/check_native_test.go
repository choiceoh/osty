package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/selfhost"
)

// TestCheckCLINativeCleanSourceExitsZero is the subprocess-level smoke
// test: `osty check --native FILE` on well-typed input succeeds with
// exit 0 and produces no error output. Validates end-to-end
// invocation (flag parsing, dispatch, conversion, exit code) without
// depending on the in-process counter.
func TestCheckCLINativeCleanSourceExitsZero(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(`fn main() {
    let x = 1
    let y = x + 2
    y
}
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	got := runOstyCLI(t, "check", "--native", path)
	if got.exit != 0 {
		t.Fatalf("osty check --native exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.Contains(got.stderr, "error[") {
		t.Fatalf("stderr contained error output on clean source:\n%s", got.stderr)
	}
}

// TestCheckCLINativeSurfacesIntrinsicViolation confirms the native
// CLI path actually surfaces the `#[intrinsic]` non-empty-body gate
// to stderr with the correct stable code (E0773). Exit code is 1.
func TestCheckCLINativeSurfacesIntrinsicViolation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(`#[intrinsic]
fn bad() -> Int {
    42
}
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	got := runOstyCLI(t, "check", "--native", path)
	if got.exit != 1 {
		t.Fatalf("osty check --native exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stderr, "error[E0773]") {
		t.Fatalf("stderr missing E0773:\n%s", got.stderr)
	}
	if !strings.Contains(got.stderr, "intrinsic") {
		t.Fatalf("stderr missing `intrinsic` in message:\n%s", got.stderr)
	}
}

// TestCheckCLINativeRejectsInspectAndDumpFlags pins the flag
// compatibility contract. --inspect / --dump-native-diags both probe
// the Go check.Result shape, which --native never materializes, so
// combining them is explicitly rejected at the dispatch site with
// exit code 2.
func TestCheckCLINativeRejectsInspectAndDumpFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(`fn main() {}
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	// --inspect and --dump-native-diags are declared as global flags
	// in parseFlags(), so Go's flag package only recognizes them
	// before the subcommand. Pre-subcommand placement is the normal
	// user ergonomic for those flags; combining them with --native
	// must still be rejected at the dispatch site.
	for _, incompatible := range []string{"--inspect", "--dump-native-diags"} {
		incompatible := incompatible
		t.Run(incompatible, func(t *testing.T) {
			got := runOstyCLI(t, incompatible, "check", "--native", path)
			if got.exit != 2 {
				t.Fatalf("exit = %d, want 2 (flag-incompatibility rejection)\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
			}
			if !strings.Contains(got.stderr, "incompatible") {
				t.Fatalf("stderr missing `incompatible` explanation:\n%s", got.stderr)
			}
		})
	}
}

// TestRunCheckFileNativeIsAstbridgeFree is the in-process counter
// test analog of TestRunResolveFileHappyPathIsAstbridgeFree. Proves
// the --native CLI path produces zero astLowerPublicFile calls
// end-to-end, including through CheckStructuredFromRun and the
// CheckDiagnosticsAsDiag converter. Stdout redirected to discard so
// printDiags output doesn't pollute test logs; the counter assertion
// is the actual invariant.
func TestRunCheckFileNativeIsAstbridgeFree(t *testing.T) {
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
	flags := cliFlags{noColor: true, native: true}
	formatter := newFormatter(path, src, flags)

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = origStdout })
	origStderr := os.Stderr
	re, we, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}
	os.Stderr = we
	t.Cleanup(func() { os.Stderr = origStderr })
	drained := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(io.Discard, r); drained <- struct{}{} }()
	go func() { _, _ = io.Copy(io.Discard, re); drained <- struct{}{} }()

	selfhost.ResetAstbridgeLowerCount()
	exit := runCheckFileNative(path, src, formatter, flags)
	_ = w.Close()
	_ = we.Close()
	<-drained
	<-drained

	if exit != 0 {
		t.Fatalf("runCheckFileNative exit = %d, want 0", exit)
	}
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("AstbridgeLowerCount after runCheckFileNative = %d, want 0 (--native CLI path regressed into a *ast.File detour)", got)
	}
}
