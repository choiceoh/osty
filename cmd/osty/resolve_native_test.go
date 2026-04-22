package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
