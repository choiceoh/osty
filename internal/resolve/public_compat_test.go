package resolve

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/selfhost"
)

func TestMaterializePublicCompatibilityKeepsNativePackageAstbridgeFree(t *testing.T) {
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

	selfhost.ResetAstbridgeLowerCount()

	pkg, err := LoadPackageForNative(dir)
	if err != nil {
		t.Fatalf("LoadPackageForNative: %v", err)
	}
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("after LoadPackageForNative: astbridge count = %d, want 0", got)
	}
	for _, pf := range pkg.Files {
		if pf.Run == nil {
			t.Fatalf("LoadPackageForNative left Run nil for %s", pf.Path)
		}
		if pf.File != nil {
			t.Fatalf("LoadPackageForNative populated File for %s, want lazy public compatibility", pf.Path)
		}
	}

	diags, err := NativeDiagnostics(pkg)
	if err != nil {
		t.Fatalf("NativeDiagnostics: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("clean package diagnostics = %#v, want none", diags)
	}
	rows, err := NativeResolutionRows(pkg, bPath)
	if err != nil {
		t.Fatalf("NativeResolutionRows: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("NativeResolutionRows returned no rows for helper reference")
	}
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("after native package queries: astbridge count = %d, want 0", got)
	}

	pkg.MaterializePublicCompatibility()
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("after MaterializePublicCompatibility: astbridge count = %d, want 0", got)
	}
	for _, pf := range pkg.Files {
		if pf.File == nil {
			t.Fatalf("MaterializePublicCompatibility left File nil for %s", pf.Path)
		}
	}

	pkg.MaterializePublicCompatibility()
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("second MaterializePublicCompatibility: astbridge count = %d, want 0", got)
	}
}
