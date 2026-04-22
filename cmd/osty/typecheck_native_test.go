package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/selfhost"
)

// TestTypecheckCLINativeCleanSourcePrintsTypes confirms `osty
// typecheck --native FILE` on clean input exits 0 AND emits at least
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
	got := runOstyCLI(t, "typecheck", "--native", path)
	if got.exit != 0 {
		t.Fatalf("osty typecheck --native exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
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
// typed-check diagnostic on stderr under --native.
func TestTypecheckCLINativeSurfacesTypeError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(`fn main() {
    let n: Int = "not an int"
}
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	got := runOstyCLI(t, "typecheck", "--native", path)
	if got.exit != 1 {
		t.Fatalf("osty typecheck --native exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stderr, "error[") {
		t.Fatalf("stderr missing error[ prefix:\n%s", got.stderr)
	}
}

// TestTypecheckCLINativeRejectsInspectAndDumpFlags mirrors the check
// path incompatibility contract: --inspect / --dump-native-diags both
// probe the Go check.Result shape, which --native never materializes.
func TestTypecheckCLINativeRejectsInspectAndDumpFlags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(`fn main() {}
`), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	for _, incompatible := range []string{"--inspect", "--dump-native-diags"} {
		incompatible := incompatible
		t.Run(incompatible, func(t *testing.T) {
			got := runOstyCLI(t, incompatible, "typecheck", "--native", path)
			if got.exit != 2 {
				t.Fatalf("exit = %d, want 2\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
			}
			if !strings.Contains(got.stderr, "incompatible") {
				t.Fatalf("stderr missing `incompatible` explanation:\n%s", got.stderr)
			}
		})
	}
}

// TestRunTypecheckFileNativeIsAstbridgeFree is the in-process counter
// test. Verifies the typecheck --native CLI body leaves
// AstbridgeLowerCount at zero — including the type dump (which
// iterates TypedNodes, not AST nodes, and should not trigger any
// *ast.File lowering).
func TestRunTypecheckFileNativeIsAstbridgeFree(t *testing.T) {
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
	exit := runTypecheckFileNative(path, src, formatter, flags)
	_ = wout.Close()
	_ = werr.Close()
	<-drained
	<-drained

	if exit != 0 {
		t.Fatalf("runTypecheckFileNative exit = %d, want 0", exit)
	}
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("AstbridgeLowerCount after runTypecheckFileNative = %d, want 0 (typecheck --native regressed into *ast.File detour)", got)
	}
}
