package resolve

import (
	"bytes"
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
	bSrc := []byte(`fn main() {
    let value = helper()
}
`)
	if err := os.WriteFile(bPath, bSrc, 0o644); err != nil {
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
	idx, err := NativeIdentKindIndex(pkg, bPath)
	if err != nil {
		t.Fatalf("NativeIdentKindIndex: %v", err)
	}
	helperOff := bytes.Index(bSrc, []byte("helper"))
	if helperOff < 0 {
		t.Fatal("test source missing helper call")
	}
	if got := idx[helperOff]; got != "function" {
		t.Fatalf("NativeIdentKindIndex[%d] = %q, want function (idx=%#v)", helperOff, got, idx)
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

func TestWorkspaceLoadPackageNativeDiscoversDepsWithoutPublicAST(t *testing.T) {
	root := t.TempDir()
	depDir := filepath.Join(root, "dep")
	if err := os.MkdirAll(depDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.osty"), []byte(`use dep

fn main() {
    dep.value()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(depDir, "lib.osty"), []byte(`pub fn value() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	selfhost.ResetAstbridgeLowerCount()

	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	if _, err := ws.LoadPackageNative(""); err != nil {
		t.Fatalf("LoadPackageNative: %v", err)
	}
	if ws.Packages["dep"] == nil {
		t.Fatal("LoadPackageNative did not discover dep package from selfhost use refs")
	}
	for key, pkg := range ws.Packages {
		for _, pf := range pkg.Files {
			if pf.Run == nil {
				t.Fatalf("package %q file %s has nil Run", key, pf.Path)
			}
			if pf.File != nil {
				t.Fatalf("package %q file %s materialized public AST during native workspace load", key, pf.Path)
			}
		}
	}
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("after workspace LoadPackageNative: astbridge count = %d, want 0", got)
	}
}

func TestLoadPackageForNativeReadsRuntimeCapability(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "osty.toml"), []byte(`[package]
name = "toolchain"
version = "1.0.0"
edition = "0.5"

[capabilities]
runtime = true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.osty"), []byte(`use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg, err := LoadPackageForNative(dir)
	if err != nil {
		t.Fatalf("LoadPackageForNative: %v", err)
	}
	if !pkg.RuntimeCapability {
		t.Fatal("RuntimeCapability = false, want true")
	}
}
