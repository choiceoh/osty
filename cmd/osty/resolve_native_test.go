package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/selfhost"
)

// TestRunResolveFileHappyPathIsAstbridgeFree pins the end-to-end CLI
// invariant: the extracted runResolveFile body that powers `osty
// resolve FILE` must not trigger a single astLowerPublicFile call on
// clean input. This catches CLI-layer regressions that the library
// primitive tests (ResolveStructuredFromRun, LoadPackageForNative)
// cannot see on their own — any accidentally-added run.File() inside
// the subcommand body would fail this test.
//
// Stdout is redirected to discard so the subcommand's normal ref/diag
// rendering doesn't pollute go test output; the counter assertion is
// the actual invariant under test.
func TestRunResolveFileHappyPathIsAstbridgeFree(t *testing.T) {
	src := []byte(`fn helper(x: Int) -> Int {
    x
}

fn main() {
    let value = helper(1)
}
`)
	path := filepath.Join(t.TempDir(), "main.osty")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	flags := cliFlags{noColor: true}
	formatter := newFormatter(path, src, flags)

	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = origStdout })
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(io.Discard, r)
	}()

	selfhost.ResetAstbridgeLowerCount()
	exit := runResolveFile(path, src, formatter, flags)
	_ = w.Close()
	<-drained

	if exit != 0 {
		t.Fatalf("runResolveFile exit = %d, want 0", exit)
	}
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("AstbridgeLowerCount after runResolveFile happy path = %d, want 0 (any non-zero means the CLI body regressed into a *ast.File detour)", got)
	}
}

// TestRunResolvePackageHappyPathIsAstbridgeFree is the DIR sibling of
// TestRunResolveFileHappyPathIsAstbridgeFree. A two-file clean
// package should round-trip through runResolvePackageInner with zero
// astbridge lowerings — LoadPackageForNative gives us FrontendRuns
// per file, native diagnostics + native rows don't need *ast.File,
// and the EnsureFiles fallback should never fire on this input
// (native rows are always available for resolvable refs).
//
// Any non-zero count means the DIR CLI body regressed: either a
// fallback triggered unnecessarily (ensureGoResolve ran when it
// shouldn't), or a new call site leaked run.File() in. Pairs with
// the library-level TestLoadPackageForNativeMultiFileIsAstbridgeFree
// to cover the entire stack from loader to printer.
func TestRunResolvePackageHappyPathIsAstbridgeFree(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.osty")
	bPath := filepath.Join(dir, "b.osty")
	if err := os.WriteFile(aPath, []byte(`pub fn helper() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte(`fn main() {
    let value = helper()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

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
	go func() { _, _ = io.Copy(io.Discard, rerr); drained <- struct{}{} }()

	selfhost.ResetAstbridgeLowerCount()
	exit := runResolvePackageInner(dir, cliFlags{noColor: true})
	_ = wout.Close()
	_ = werr.Close()
	<-drained
	<-drained

	if exit != 0 {
		t.Fatalf("runResolvePackageInner exit = %d, want 0", exit)
	}
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("AstbridgeLowerCount after runResolvePackageInner = %d, want 0 (DIR resolve CLI body regressed into a *ast.File fallback)", got)
	}
}

func TestResolveCLISingleFilePrintsNativeResolutionRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(`fn helper() -> Int { 1 }

fn main() {
    let value = helper()
}
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	got := runOstyCLI(t, "resolve", path)
	if got.exit != 0 {
		t.Fatalf("osty resolve exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "helper") {
		t.Fatalf("stdout missing helper ref:\n%s", got.stdout)
	}
	if !strings.Contains(got.stdout, "function") {
		t.Fatalf("stdout missing function kind:\n%s", got.stdout)
	}
	if !strings.Contains(got.stdout, "->1:1") {
		t.Fatalf("stdout missing helper def position:\n%s", got.stdout)
	}
}

func TestResolveCLIPackagePrintsNativeCrossFileResolutionRows(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.osty")
	bPath := filepath.Join(dir, "b.osty")
	if err := os.WriteFile(aPath, []byte(`pub fn helper() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatalf("write a.osty: %v", err)
	}
	if err := os.WriteFile(bPath, []byte(`fn main() {
    let value = helper()
}
`), 0o644); err != nil {
		t.Fatalf("write b.osty: %v", err)
	}

	got := runOstyCLI(t, "resolve", dir)
	if got.exit != 0 {
		t.Fatalf("osty resolve exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "# "+bPath) {
		t.Fatalf("stdout missing b.osty header:\n%s", got.stdout)
	}
	if !strings.Contains(got.stdout, "helper") {
		t.Fatalf("stdout missing helper ref:\n%s", got.stdout)
	}
	if !strings.Contains(got.stdout, "->1:5") {
		t.Fatalf("stdout missing cross-file helper def position:\n%s", got.stdout)
	}
}

func TestResolveCLISingleFilePrintsNativeDiagnostics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(`fn main() {
    missing()
}
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	got := runOstyCLI(t, "resolve", path)
	if got.exit != 1 {
		t.Fatalf("osty resolve exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stderr, "error[E0500]") {
		t.Fatalf("stderr missing native resolve code:\n%s", got.stderr)
	}
	if !strings.Contains(got.stderr, "undefined name") {
		t.Fatalf("stderr missing native resolve message:\n%s", got.stderr)
	}
}
