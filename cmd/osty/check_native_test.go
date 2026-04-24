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

// TestCheckCLINativeRejectsInspectFlag pins the remaining flag
// compatibility contract. --inspect still probes the Go check.Result
// shape, which --native never materializes, so combining them is
// explicitly rejected at the dispatch site with exit code 2.
func TestCheckCLINativeRejectsInspectFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(`fn main() {}
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	// --inspect is declared as a global flag in parseFlags(), so Go's
	// flag package only recognizes it
	// before the subcommand. Pre-subcommand placement is the normal
	// user ergonomic for those flags; combining them with --native
	// must still be rejected at the dispatch site.
	got := runOstyCLI(t, "--inspect", "check", "--native", path)
	if got.exit != 2 {
		t.Fatalf("exit = %d, want 2 (flag-incompatibility rejection)\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stderr, "incompatible") {
		t.Fatalf("stderr missing `incompatible` explanation:\n%s", got.stderr)
	}
}

func TestRunCheckFileNativeDumpNativeDiagsPrintsSummary(t *testing.T) {
	src := []byte(`fn id(n: Int) -> Int {
    n
}

fn main() {
    let y = id(1)
    y
}
`)
	path := filepath.Join(t.TempDir(), "main.osty")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	flags := cliFlags{noColor: true, native: true, dumpNativeDiags: true}
	formatter := newFormatter(path, src, flags)

	origStderr := os.Stderr
	rerr, werr, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}
	os.Stderr = werr
	t.Cleanup(func() { os.Stderr = origStderr })

	selfhost.ResetAstbridgeLowerCount()
	exit := runCheckFileNative(path, src, formatter, flags)
	_ = werr.Close()
	stderrBytes, err := io.ReadAll(rerr)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	stderr := string(stderrBytes)

	if exit != 0 {
		t.Fatalf("runCheckFileNative exit = %d, want 0\nstderr:\n%s", exit, stderr)
	}
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("AstbridgeLowerCount after dump-native-diags native check = %d, want 0", got)
	}
	if !strings.Contains(stderr, "native checker telemetry: "+path) {
		t.Fatalf("stderr missing telemetry header:\n%s", stderr)
	}
	if !strings.Contains(stderr, "assignments:") {
		t.Fatalf("stderr missing assignments row:\n%s", stderr)
	}
}

// TestCheckCLINativePackageCleanSourceExitsZero is the DIR sibling
// of the single-file happy-path test. A two-file package (no cross-
// file references) should pass `osty check --native DIR` with exit
// 0 and no stderr error output.
func TestCheckCLINativePackageCleanSourceExitsZero(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.osty")
	bPath := filepath.Join(dir, "b.osty")
	if err := os.WriteFile(aPath, []byte(`pub fn helper() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte(`fn main() {
    let x = 1
    let y = x + 2
    y
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runOstyCLI(t, "check", "--native", dir)
	if got.exit != 0 {
		t.Fatalf("osty check --native DIR exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.Contains(got.stderr, "error[") {
		t.Fatalf("stderr contained error output on clean package:\n%s", got.stderr)
	}
}

func TestCheckCLINativePackageLoadsStdlibImportSurfaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(`use std.strings as strings

fn main() {
    let parts = strings.fields("alpha beta")
    parts
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runOstyCLI(t, "check", "--native", dir)
	if got.exit != 0 {
		t.Fatalf("osty check --native DIR exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.Contains(got.stderr, "error[E0703]") {
		t.Fatalf("stderr retained missing-method E0703 for std.strings surface:\n%s", got.stderr)
	}
}

// TestCheckCLINativePackageSurfacesPerFileIntrinsic confirms the
// per-file diagnostic bucketing: an `#[intrinsic]` violation in the
// second file must surface with E0773 and a span whose rendered path
// points at b.osty (not the first file and not the bundled buffer).
func TestCheckCLINativePackageSurfacesPerFileIntrinsic(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.osty")
	bPath := filepath.Join(dir, "b.osty")
	if err := os.WriteFile(aPath, []byte(`pub fn helper() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte(`#[intrinsic]
fn bad() -> Int {
    42
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runOstyCLI(t, "check", "--native", dir)
	if got.exit != 1 {
		t.Fatalf("exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stderr, "error[E0773]") {
		t.Fatalf("stderr missing E0773:\n%s", got.stderr)
	}
	// printPackageDiags emits the file path in its rendered output.
	// The intrinsic lives in b.osty only; a.osty is clean.
	if !strings.Contains(got.stderr, "b.osty") {
		t.Fatalf("stderr missing b.osty in diagnostic:\n%s", got.stderr)
	}
	if strings.Contains(got.stderr, "error[E0773]") && strings.Contains(got.stderr, "a.osty:") {
		t.Fatalf("stderr incorrectly attributed E0773 to a.osty:\n%s", got.stderr)
	}
}

func TestCheckCLINativeWorkspaceCrossPackageExitsZero(t *testing.T) {
	dir := t.TempDir()
	depDir := filepath.Join(dir, "dep")
	appDir := filepath.Join(dir, "app")
	if err := os.MkdirAll(depDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(depDir, "dep.osty"), []byte(`pub fn helper() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "main.osty"), []byte(`use dep

fn main() {
    let x = dep.helper()
    x
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runOstyCLI(t, "check", "--native", dir)
	if got.exit != 0 {
		t.Fatalf("osty check --native WORKSPACE exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.Contains(got.stderr, "error[") {
		t.Fatalf("stderr contained error output on clean workspace:\n%s", got.stderr)
	}
}

// TestRunCheckPackageNativeIsAstbridgeFree is the DIR in-process
// counter test. LoadPackageForNative + CheckPackageStructured +
// nativePackageCheckDiags + CheckDiagnosticsAsDiag must all leave
// AstbridgeLowerCount at zero for a clean multi-file package. Pairs
// with TestLoadPackageForNativeMultiFileIsAstbridgeFree (which only
// covered the resolve side).
func TestRunCheckPackageNativeIsAstbridgeFree(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.osty")
	bPath := filepath.Join(dir, "b.osty")
	if err := os.WriteFile(aPath, []byte(`pub fn helper() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte(`fn main() {
    let x = 1
    let y = x + 2
    y
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
	exit := runCheckPackageNative(dir, cliFlags{noColor: true, native: true})
	_ = wout.Close()
	_ = werr.Close()
	<-drained
	<-drained

	if exit != 0 {
		t.Fatalf("runCheckPackageNative exit = %d, want 0", exit)
	}
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("AstbridgeLowerCount after runCheckPackageNative = %d, want 0 (DIR path regressed into *ast.File detour)", got)
	}
}

func TestRunCheckWorkspaceNativeIsAstbridgeFree(t *testing.T) {
	dir := t.TempDir()
	depDir := filepath.Join(dir, "dep")
	appDir := filepath.Join(dir, "app")
	if err := os.MkdirAll(depDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(depDir, "dep.osty"), []byte(`pub fn helper() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "main.osty"), []byte(`use dep

fn main() {
    let x = dep.helper()
    x
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
	exit := runCheckWorkspaceNative(dir, cliFlags{noColor: true, native: true})
	_ = wout.Close()
	_ = werr.Close()
	<-drained
	<-drained

	if exit != 0 {
		t.Fatalf("runCheckWorkspaceNative exit = %d, want 0", exit)
	}
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("AstbridgeLowerCount after runCheckWorkspaceNative = %d, want 0", got)
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

// TestCheckCLIDefaultPathUsesSelfhostArena is the production-default
// companion to TestRunCheckFileNativeIsAstbridgeFree: after the
// 1c.1 flip (SELFHOST_PORT_MATRIX.md), `osty check FILE` with no
// flag at all must route through the self-host arena pipeline the
// same way `--native` does. Run the subprocess CLI so the default
// actually goes through parseFlags/dispatch, then verify exit 0 on
// a well-typed input. Paired regression: any future attempt to
// re-route the default through runCheckFileLegacy must either
// update this test or add a --legacy-gated wrapper.
func TestCheckCLIDefaultPathExitsZero(t *testing.T) {
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
	got := runOstyCLI(t, "check", path)
	if got.exit != 0 {
		t.Fatalf("osty check (default) exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.Contains(got.stderr, "error[") {
		t.Fatalf("stderr contained error output on clean source:\n%s", got.stderr)
	}
}

// TestCheckCLILegacyOptOutStillWorks pins the 1c.1 escape hatch:
// even after the default flip, `osty check --legacy FILE` must
// still route through runCheckFileLegacy so the Go-hosted resolve +
// check.File pair remains a reachable fallback until Phase 1c.5
// removes both sides.
func TestCheckCLILegacyOptOutStillWorks(t *testing.T) {
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
	got := runOstyCLI(t, "check", "--legacy", path)
	if got.exit != 0 {
		t.Fatalf("osty check --legacy exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.Contains(got.stderr, "error[") {
		t.Fatalf("stderr contained error output on clean source:\n%s", got.stderr)
	}
}

// TestRunCheckFileDefaultPathIsAstbridgeFree is the in-process
// counter assertion for the default flip. A default-flag-set
// cliFlags{} now has native=true (set by parseFlags), so running
// runCheckFileNative from it is the production happy path; confirm
// zero astbridge lowerings end-to-end.
func TestRunCheckFileDefaultPathIsAstbridgeFree(t *testing.T) {
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
	// parseFlags sets native=true by default; mirror that here so the
	// test reflects the on-CLI behavior without touching the global
	// flag package.
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
		t.Fatalf("runCheckFileNative (default) exit = %d, want 0", exit)
	}
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("AstbridgeLowerCount after default runCheckFileNative = %d, want 0", got)
	}
}
