package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckCLICleanSourceExitsZero is the subprocess-level smoke
// test: `osty check FILE` on well-typed input succeeds with
// exit 0 and produces no error output. Validates end-to-end
// invocation (flag parsing, dispatch, conversion, exit code) without
// depending on the in-process counter.
func TestCheckCLICleanSourceExitsZero(t *testing.T) {
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
		t.Fatalf("osty check exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.Contains(got.stderr, "error[") {
		t.Fatalf("stderr contained error output on clean source:\n%s", got.stderr)
	}
}

// TestCheckCLISurfacesIntrinsicViolation confirms the native
// CLI path actually surfaces the `#[intrinsic]` non-empty-body gate
// to stderr with the correct stable code (E0773). Exit code is 1.
func TestCheckCLISurfacesIntrinsicViolation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(`#[intrinsic]
fn bad() -> Int {
    42
}
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	got := runOstyCLI(t, "check", path)
	if got.exit != 1 {
		t.Fatalf("osty check exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stderr, "error[E0773]") {
		t.Fatalf("stderr missing E0773:\n%s", got.stderr)
	}
	if !strings.Contains(got.stderr, "intrinsic") {
		t.Fatalf("stderr missing `intrinsic` in message:\n%s", got.stderr)
	}
}

// TestCheckCLIInspectFlagUsesSelfhost pins that --inspect is served by
// the selfhost inspect pass on the native check path, not by the retired Go
// check.Result replay.
func TestCheckCLIInspectFlagUsesSelfhost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(`fn main() {
    let x = 1
    x
}
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	got := runOstyCLI(t, "--inspect", "check", path)
	if got.exit != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "BIND") || !strings.Contains(got.stdout, "Int") {
		t.Fatalf("stdout missing selfhost inspect rows:\n%s", got.stdout)
	}
}

func TestRunCheckFileDumpCheckDiagsPrintsSummary(t *testing.T) {
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
	flags := cliFlags{noColor: true, dumpCheckDiags: true}
	formatter := newFormatter(path, src, flags)

	origStderr := os.Stderr
	rerr, werr, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}
	os.Stderr = werr
	t.Cleanup(func() { os.Stderr = origStderr })

	exit := runCheckFile(path, src, formatter, flags)
	_ = werr.Close()
	stderrBytes, err := io.ReadAll(rerr)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	stderr := string(stderrBytes)

	if exit != 0 {
		t.Fatalf("runCheckFile exit = %d, want 0\nstderr:\n%s", exit, stderr)
	}
	if !strings.Contains(stderr, "checker telemetry: "+path) {
		t.Fatalf("stderr missing telemetry header:\n%s", stderr)
	}
	if !strings.Contains(stderr, "assignments:") {
		t.Fatalf("stderr missing assignments row:\n%s", stderr)
	}
}

// TestCheckCLIPackageCleanSourceExitsZero is the DIR sibling
// of the single-file happy-path test. A two-file package (no cross-
// file references) should pass `osty check DIR` with exit
// 0 and no stderr error output.
func TestCheckCLIPackageCleanSourceExitsZero(t *testing.T) {
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
	got := runOstyCLI(t, "check", dir)
	if got.exit != 0 {
		t.Fatalf("osty check DIR exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.Contains(got.stderr, "error[") {
		t.Fatalf("stderr contained error output on clean package:\n%s", got.stderr)
	}
}

func TestCheckCLIPackageLoadsStdlibImportSurfaces(t *testing.T) {
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
	got := runOstyCLI(t, "check", dir)
	if got.exit != 0 {
		t.Fatalf("osty check DIR exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.Contains(got.stderr, "error[E0703]") {
		t.Fatalf("stderr retained missing-method E0703 for std.strings surface:\n%s", got.stderr)
	}
}

// TestCheckCLIPackageSurfacesPerFileIntrinsic confirms the
// per-file diagnostic bucketing: an `#[intrinsic]` violation in the
// second file must surface with E0773 and a span whose rendered path
// points at b.osty (not the first file and not the bundled buffer).
func TestCheckCLIPackageSurfacesPerFileIntrinsic(t *testing.T) {
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
	got := runOstyCLI(t, "check", dir)
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

func TestCheckCLIWorkspaceCrossPackageExitsZero(t *testing.T) {
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
	got := runOstyCLI(t, "check", dir)
	if got.exit != 0 {
		t.Fatalf("osty check WORKSPACE exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.Contains(got.stderr, "error[") {
		t.Fatalf("stderr contained error output on clean workspace:\n%s", got.stderr)
	}
}

// TestRunCheckPackageDirHappyPath exercises the DIR check
// path end-to-end (LoadPackageForNative + CheckPackageStructured +
// packageCheckDiags + CheckDiagnosticsAsDiag) on a clean
// multi-file package and asserts exit 0.
func TestRunCheckPackageDirHappyPath(t *testing.T) {
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

	exit := runCheckPackageDir(dir, cliFlags{noColor: true})
	_ = wout.Close()
	_ = werr.Close()
	<-drained
	<-drained

	if exit != 0 {
		t.Fatalf("runCheckPackageDir exit = %d, want 0", exit)
	}
}

func TestRunCheckWorkspaceHappyPath(t *testing.T) {
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

	exit := runCheckWorkspace(dir, cliFlags{noColor: true})
	_ = wout.Close()
	_ = werr.Close()
	<-drained
	<-drained

	if exit != 0 {
		t.Fatalf("runCheckWorkspace exit = %d, want 0", exit)
	}
}

func TestCheckWorkspaceMemberDirLoadsEnclosingWorkspace(t *testing.T) {
	dir := t.TempDir()
	depDir := filepath.Join(dir, "dep")
	appDir := filepath.Join(dir, "app")
	if err := os.MkdirAll(depDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "osty.toml"), []byte(`[workspace]
members = ["dep", "app"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(depDir, "osty.toml"), []byte(`[package]
name = "dep"
version = "0.1.0"
edition = "0.5"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(depDir, "dep.osty"), []byte(`pub fn helper() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "osty.toml"), []byte(`[package]
name = "app"
version = "0.1.0"
edition = "0.5"
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

	got := runOstyCLI(t, "check", appDir)
	if got.exit != 0 {
		t.Fatalf("osty check member dir exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.Contains(got.stderr, "error[") {
		t.Fatalf("stderr contained error output on clean workspace member:\n%s", got.stderr)
	}
}

func TestCheckStdlibSupplementalSurface(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(`use std.testing
use std.testing.gen as gen

fn main() {
    let n: Int = 7
    let f: Float64 = n.toFloat64()
    let b: Byte = n.toByte()
    let rounded: Result<Int8, Error> = n.toInt8()
    let checked: Int? = n.checkedAdd(1)
    let wrapped: Int = n.wrappingAdd(1)
    let mut lines: List<String> = ["value"]
    lines.push(f.toString())
    let joined: String = lines.join("\n")
    let err: Error = Error.new(joined)
    let _ = err.message()
    let _ = rounded
    let _ = b
    let _ = checked
    let _ = wrapped
    testing.property("small ints", gen.intRange(0, 10), |x: Int| -> Bool { x >= 0 })
}
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	got := runOstyCLI(t, "check", "--no-airepair", path)
	if got.exit != 0 {
		t.Fatalf("osty check stdlib supplemental surface exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.Contains(got.stderr, "error[") {
		t.Fatalf("stderr contained error output on stdlib supplemental surface:\n%s", got.stderr)
	}
}

// TestRunCheckFileDumpTelemetry exercises the native check CLI
// path end-to-end (CheckStructuredFromRun + CheckDiagnosticsAsDiag)
// and asserts the dump-check-diags telemetry header lands on stderr.
func TestRunCheckFileDumpTelemetry(t *testing.T) {
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

	exit := runCheckFile(path, src, formatter, flags)
	_ = w.Close()
	_ = we.Close()
	<-drained
	<-drained

	if exit != 0 {
		t.Fatalf("runCheckFile exit = %d, want 0", exit)
	}
}

// TestCheckCLIDefaultPathExitsZero is the production-default companion
// to TestRunCheckFileDumpTelemetry: `osty check FILE` routes through
// the self-host arena pipeline. Run the subprocess CLI so dispatch
// actually goes through clicmd.ParseArgs, then verify exit 0 on a
// well-typed input — the self-host path is the only path.
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

// TestRunCheckFileDefaultPathHappyPath exercises the production
// default-path single-file check (runCheckFile) and asserts
// exit 0 on clean input.
func TestRunCheckFileDefaultPathHappyPath(t *testing.T) {
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

	exit := runCheckFile(path, src, formatter, flags)
	_ = w.Close()
	_ = we.Close()
	<-drained
	<-drained

	if exit != 0 {
		t.Fatalf("runCheckFile (default) exit = %d, want 0", exit)
	}
}
