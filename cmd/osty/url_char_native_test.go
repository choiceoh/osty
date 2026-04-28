package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckCLIHandlesStdUrlAndCharImports(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	source := `use std.char as ascii
use std.url as urls

fn main() {
    let _ = urls
    let _ = ascii
}
`
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	got := runOstyCLI(t, "check", "--no-airepair", path)
	if got.exit != 0 {
		t.Fatalf("osty check exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.Contains(got.stderr, "code generation is not implemented yet") {
		t.Fatalf("stderr = %q, did not want backend-lowering failure during check", got.stderr)
	}
}
