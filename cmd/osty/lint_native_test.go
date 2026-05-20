package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/lint"
	"github.com/osty/osty/internal/resolve"
)

func TestRunLintFileIsAstbridgeFree(t *testing.T) {
	src := []byte(`fn main() {
    let x = 1
    x
}
`)
	path := filepath.Join(t.TempDir(), "main.osty")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	flags := cliFlags{noColor: true}
	formatter := newFormatter(path, src, flags)

	restore := redirectStdoutStderr(t)
	exit := runLintFile(path, src, formatter, flags, lint.Config{}, false)
	restore()

	if exit != 0 {
		t.Fatalf("runLintFile exit = %d, want 0", exit)
	}
}

func TestLintPackageNativeDiagnosticsIsAstbridgeFree(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.osty"), []byte(`pub fn helper() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.osty"), []byte(`fn main() {
    let value = helper()
    value
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := resolve.LoadPackageForNative(dir)
	if err != nil {
		t.Fatalf("LoadPackageForNative: %v", err)
	}
	frontendDiags, err := lintNativePackageDiagnostics(pkg, nil)
	if err != nil {
		t.Fatalf("lintNativePackageDiagnostics: %v", err)
	}
	lintDiags := lint.Package(pkg).Diags
	if hasError(append(frontendDiags, lintDiags...)) {
		t.Fatalf("clean package produced diagnostics: frontend=%#v lint=%#v", frontendDiags, lintDiags)
	}
}

func redirectStdoutStderr(t *testing.T) func() {
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
	drained := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(io.Discard, rout); drained <- struct{}{} }()
	go func() { _, _ = io.Copy(io.Discard, rerr); drained <- struct{}{} }()
	return func() {
		_ = wout.Close()
		_ = werr.Close()
		<-drained
		<-drained
		os.Stdout = origStdout
		os.Stderr = origStderr
	}
}
