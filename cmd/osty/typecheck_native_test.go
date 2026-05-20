package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTypecheckCLINativeCleanSourcePrintsTypes confirms `osty
// typecheck FILE` on clean input exits 0 AND emits at least
// one `line:col-line:col\tType` row per printNativeTypes. The exact
// node set depends on the native checker's recording, so we only
// assert the presence of the `Int` type for the scalar bindings
// instead of an exact snapshot.
func TestTypecheckCLINativeCleanSourcePrintsTypes(t *testing.T) {
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
	got := runOstyCLI(t, "typecheck", path)
	if got.exit != 0 {
		t.Fatalf("osty typecheck exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.Contains(got.stderr, "error[") {
		t.Fatalf("stderr contained error output on clean source:\n%s", got.stderr)
	}
	if !strings.Contains(got.stdout, "Int") {
		t.Fatalf("stdout missing `Int` type row — printNativeTypes silent or empty\nstdout:\n%s", got.stdout)
	}
}

// TestTypecheckCLINativeSurfacesTypeError pins that a clear type
// mismatch (Int binding initialized with String) still surfaces a
// typed-check diagnostic on stderr.
func TestTypecheckCLINativeSurfacesTypeError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(`fn main() {
    let n: Int = "not an int"
}
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	got := runOstyCLI(t, "typecheck", path)
	if got.exit != 1 {
		t.Fatalf("osty typecheck exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stderr, "error[") {
		t.Fatalf("stderr missing error[ prefix:\n%s", got.stderr)
	}
}

// TestTypecheckCLINativeInspectFlagUsesSelfhost mirrors the check-path
// contract: --inspect is served by the selfhost inspect pass on the native
// typecheck path.
func TestTypecheckCLINativeInspectFlagUsesSelfhost(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(`fn main() {
    let x = 1
    x
}
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	got := runOstyCLI(t, "--inspect", "typecheck", path)
	if got.exit != 0 {
		t.Fatalf("exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "BIND") || !strings.Contains(got.stdout, "Int") {
		t.Fatalf("stdout missing selfhost inspect rows:\n%s", got.stdout)
	}
}

func TestRunTypecheckFileNativeDumpNativeDiagsPrintsSummary(t *testing.T) {
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
	flags := cliFlags{noColor: true, dumpNativeDiags: true}
	formatter := newFormatter(path, src, flags)

	origStdout := os.Stdout
	rout, wout, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stdout: %v", err)
	}
	os.Stdout = wout
	t.Cleanup(func() { os.Stdout = origStdout })
	origStderr := os.Stderr
	rerr, werr, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe stderr: %v", err)
	}
	os.Stderr = werr
	t.Cleanup(func() { os.Stderr = origStderr })

	exit := runTypecheckFileNative(path, src, formatter, flags)
	_ = wout.Close()
	_ = werr.Close()
	stdoutBytes, err := io.ReadAll(rout)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	stderrBytes, err := io.ReadAll(rerr)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	stdout := string(stdoutBytes)
	stderr := string(stderrBytes)

	if exit != 0 {
		t.Fatalf("runTypecheckFileNative exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	if !strings.Contains(stderr, "native checker telemetry: "+path) {
		t.Fatalf("stderr missing telemetry header:\n%s", stderr)
	}
	if !strings.Contains(stderr, "assignments:") {
		t.Fatalf("stderr missing assignments row:\n%s", stderr)
	}
	if !strings.Contains(stdout, "Int") {
		t.Fatalf("stdout missing Int type row:\n%s", stdout)
	}
}

// TestTypecheckCLINativePackageCleanSourcePrintsPerFileTypes confirms
// the DIR rendering: each file's type dump is prefixed with a
// `# <path>` header so downstream consumers can split by file. A
// clean two-file package exits 0 and emits both file headers.
func TestTypecheckCLINativePackageCleanSourcePrintsPerFileTypes(t *testing.T) {
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
	got := runOstyCLI(t, "typecheck", dir)
	if got.exit != 0 {
		t.Fatalf("osty typecheck DIR exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "# "+bPath) {
		t.Fatalf("stdout missing `# %s` header:\n%s", bPath, got.stdout)
	}
	// b.osty has scalar Int expressions that should show up in the
	// typed-node dump.
	if !strings.Contains(got.stdout, "Int") {
		t.Fatalf("stdout missing Int type row:\n%s", got.stdout)
	}
}

func TestTypecheckCLINativePackageLoadsStdlibImportSurfaces(t *testing.T) {
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
	got := runOstyCLI(t, "typecheck", dir)
	if got.exit != 0 {
		t.Fatalf("osty typecheck DIR exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.Contains(got.stderr, "error[E0703]") {
		t.Fatalf("stderr retained missing-method E0703 for std.strings surface:\n%s", got.stderr)
	}
	if !strings.Contains(got.stdout, "List<String>") {
		t.Fatalf("stdout missing List<String> type row:\n%s", got.stdout)
	}
}

func TestTypecheckCLINativeWorkspaceCrossPackagePrintsTypes(t *testing.T) {
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
	appPath := filepath.Join(appDir, "main.osty")
	if err := os.WriteFile(appPath, []byte(`use dep

fn main() {
    let x = dep.helper()
    x
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runOstyCLI(t, "typecheck", dir)
	if got.exit != 0 {
		t.Fatalf("osty typecheck WORKSPACE exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, "# "+appPath) {
		t.Fatalf("stdout missing `# %s` header:\n%s", appPath, got.stdout)
	}
	if !strings.Contains(got.stdout, "Int") {
		t.Fatalf("stdout missing Int type row:\n%s", got.stdout)
	}
}

// TestRunTypecheckPackageNativeHappyPath exercises the DIR
// typecheck path end-to-end (LoadPackageForNative →
// CheckPackageStructured → nativePackageCheckDiags →
// printNativePackageTypes) on a clean two-file package.
func TestRunTypecheckPackageNativeHappyPath(t *testing.T) {
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

	exit := runTypecheckPackageNative(dir, cliFlags{noColor: true})
	_ = wout.Close()
	_ = werr.Close()
	<-drained
	<-drained

	if exit != 0 {
		t.Fatalf("runTypecheckPackageNative exit = %d, want 0", exit)
	}
}

func TestRunTypecheckWorkspaceNativeHappyPath(t *testing.T) {
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

	exit := runTypecheckWorkspaceNative(dir, cliFlags{noColor: true})
	_ = wout.Close()
	_ = werr.Close()
	<-drained
	<-drained

	if exit != 0 {
		t.Fatalf("runTypecheckWorkspaceNative exit = %d, want 0", exit)
	}
}

// TestRunTypecheckFileNativeDumpTelemetry exercises the native
// typecheck CLI body end-to-end (including the type dump which
// iterates TypedNodes) and asserts the dump-native-diags telemetry
// header lands on stderr.
func TestRunTypecheckFileNativeDumpTelemetry(t *testing.T) {
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

	exit := runTypecheckFileNative(path, src, formatter, flags)
	_ = wout.Close()
	_ = werr.Close()
	<-drained
	<-drained

	if exit != 0 {
		t.Fatalf("runTypecheckFileNative exit = %d, want 0", exit)
	}
}
