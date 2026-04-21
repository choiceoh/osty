package resolve

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/diag"
)

// TestPackageDiagsCarryOwningFilePath ensures ResolvePackage stamps
// d.File on every diagnostic it emits. Without this, multi-file package
// walkers render resolver errors against the wrong file's source (see
// cmd/osty/pickFile).
func TestPackageDiagsCarryOwningFilePath(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.osty")
	bPath := filepath.Join(dir, "b.osty")
	if err := os.WriteFile(aPath, []byte(`pub fn defined() -> Int { 0 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte(`pub fn caller() -> Int { notDefinedInEitherFile() }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := LoadPackage(dir)
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	res := ResolvePackage(pkg, NewPrelude())

	var undef *diag.Diagnostic
	for _, d := range res.Diags {
		if d.Code == diag.CodeUndefinedName {
			undef = d
			break
		}
	}
	if undef == nil {
		t.Fatalf("expected E0500 for `notDefinedInEitherFile`; got diags:\n%+v", res.Diags)
	}
	if undef.File != bPath {
		t.Fatalf("undefined-name diag attributed to %q, want %q", undef.File, bPath)
	}
}
