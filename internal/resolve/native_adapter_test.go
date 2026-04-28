package resolve

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/selfhost"
)

// TestLoadPackageForNativeMultiFileIsAstbridgeFree pins the PR6 wedge:
// running the package resolve path via LoadPackageForNative +
// NativeResolutionRows / NativeDiagnostics over a multi-file package
// must not trigger the astbridge-based *ast.File lowering. The counter
// stays at zero throughout; calling EnsureFiles afterwards bumps it by
// exactly one per file, proving the lazy lowering is wired correctly
// for the fallback paths (--show-scopes, printResolutionRefs).
func TestLoadPackageForNativeMultiFileIsAstbridgeFree(t *testing.T) {
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
		t.Fatalf("after LoadPackageForNative: AstbridgeLowerCount = %d, want 0", got)
	}
	for _, pf := range pkg.Files {
		if pf.File != nil {
			t.Fatalf("LoadPackageForNative populated pf.File for %s (expected nil until EnsureFile)", pf.Path)
		}
		if pf.Run == nil {
			t.Fatalf("LoadPackageForNative left pf.Run nil for %s", pf.Path)
		}
	}

	diags, err := NativeDiagnostics(pkg)
	if err != nil {
		t.Fatalf("NativeDiagnostics: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("clean package produced diagnostics: %#v", diags)
	}
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("after NativeDiagnostics: AstbridgeLowerCount = %d, want 0", got)
	}

	rows, err := NativeResolutionRows(pkg, bPath)
	if err != nil {
		t.Fatalf("NativeResolutionRows: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("expected helper ref rows from b.osty, got none")
	}
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("after NativeResolutionRows: AstbridgeLowerCount = %d, want 0", got)
	}

	pkg.EnsureFiles()
	if got := selfhost.AstbridgeLowerCount(); got != int64(len(pkg.Files)) {
		t.Fatalf("after EnsureFiles: AstbridgeLowerCount = %d, want %d (one lowering per file)", got, len(pkg.Files))
	}
	for _, pf := range pkg.Files {
		if pf.File == nil {
			t.Fatalf("EnsureFiles did not materialize pf.File for %s", pf.Path)
		}
	}

	pkg.EnsureFiles()
	if got := selfhost.AstbridgeLowerCount(); got != int64(len(pkg.Files)) {
		t.Fatalf("second EnsureFiles re-lowered: AstbridgeLowerCount = %d, want %d (cached)", got, len(pkg.Files))
	}
}

func TestNativeResolutionRowsCrossFile(t *testing.T) {
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

	pkg, err := LoadPackageArenaFirst(dir)
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	rows, err := NativeResolutionRows(pkg, bPath)
	if err != nil {
		t.Fatalf("NativeResolutionRows: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("rows = %#v, want non-empty helper ref rows", rows)
	}
	found := false
	for _, row := range rows {
		if row.Name == "helper" && row.Kind == "function" && row.Def == "1:5" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("rows = %#v, want helper -> 1:5", rows)
	}
}

func TestNativeResolutionRowsCachesResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(`fn helper() -> Int { 1 }

fn main() {
    let value = helper()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := LoadPackageArenaFirst(dir)
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	first, err := NativeResolutionRows(pkg, path)
	if err != nil {
		t.Fatalf("NativeResolutionRows first: %v", err)
	}
	pkg.Files[0].File = nil
	pkg.Files[0].CanonicalSource = []byte("fn broken(")
	second, err := NativeResolutionRows(pkg, path)
	if err != nil {
		t.Fatalf("NativeResolutionRows second: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("row count changed across cached call: first=%#v second=%#v", first, second)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("cached rows changed at %d: first=%#v second=%#v", i, first, second)
		}
	}
}

func TestNativeDiagnosticsSingleFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte(`fn main() {
    missing()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := LoadPackageArenaFirst(dir)
	if err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	diags, err := NativeDiagnostics(pkg)
	if err != nil {
		t.Fatalf("NativeDiagnostics: %v", err)
	}
	if len(diags) != 1 {
		t.Fatalf("diag count = %d, want 1 (%#v)", len(diags), diags)
	}
	got := diags[0]
	if got.Code != "E0500" {
		t.Fatalf("code = %q, want E0500", got.Code)
	}
	if got.Message != "undefined name" {
		t.Fatalf("message = %q, want undefined name", got.Message)
	}
	if got.File != path {
		t.Fatalf("file = %q, want %q", got.File, path)
	}
	if pos := got.PrimaryPos(); pos.Line != 2 || pos.Column != 5 {
		t.Fatalf("primary pos = %v, want 2:5", pos)
	}
}

func TestResolveFileDefaultDefinesStdlibPackageAlias(t *testing.T) {
	src := []byte(`use std.fs

fn main() {
    let _ = fs.readToString("demo.txt")
}
`)
	file, diags := parser.ParseDiagnostics(src)
	if len(diags) != 0 {
		t.Fatalf("parse diagnostics = %#v, want none", diags)
	}
	pkgScope := NewScope(NewPrelude(), "package:std.fs")
	pkgScope.DefineForce(&Symbol{Name: "readToString", Kind: SymFn, Pub: true})
	reg := stubStdlibProvider{
		"std.fs": &Package{Name: "fs", PkgScope: pkgScope},
	}
	res := ResolveFileSourceDefault(src, file, reg)
	if res.FileScope == nil {
		t.Fatal("FileScope = nil, want populated scope")
	}
	sym := res.FileScope.Lookup("fs")
	if sym == nil {
		t.Fatal("FileScope.Lookup(\"fs\") = nil, want package alias")
	}
	if sym.Kind != SymPackage {
		t.Fatalf("fs kind = %v, want SymPackage", sym.Kind)
	}
	if sym.Package == nil || sym.Package.PkgScope == nil {
		t.Fatalf("fs package = %#v, want resolved stdlib package", sym.Package)
	}
}

type stubStdlibProvider map[string]*Package

func (s stubStdlibProvider) LookupPackage(dotPath string) *Package {
	return s[dotPath]
}

func TestWorkspaceResolveAllDefinesPackageAliases(t *testing.T) {
	root := t.TempDir()
	alphaDir := filepath.Join(root, "alpha")
	betaDir := filepath.Join(root, "beta")
	if err := os.MkdirAll(alphaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(betaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	alphaFile := filepath.Join(alphaDir, "lib.osty")
	betaFile := filepath.Join(betaDir, "lib.osty")
	if err := os.WriteFile(alphaFile, []byte(`pub fn helper() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(betaFile, []byte(`use alpha

fn main() {
    let _ = alpha.helper()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	for _, path := range WorkspacePackagePaths(root) {
		if _, err := ws.LoadPackage(path); err != nil {
			t.Fatalf("LoadPackage %s: %v", path, err)
		}
	}
	results := ws.ResolveAll()
	beta := ws.Packages["beta"]
	if beta == nil || len(beta.Files) == 0 {
		t.Fatalf("beta package = %#v, want loaded package with files", beta)
	}
	if got := results["beta"]; got == nil || len(got.Diags) != 0 {
		t.Fatalf("beta diagnostics = %#v, want none", got)
	}
	sym := beta.Files[0].FileScope.Lookup("alpha")
	if sym == nil {
		t.Fatal("FileScope.Lookup(\"alpha\") = nil, want imported package symbol")
	}
	if sym.Kind != SymPackage {
		t.Fatalf("alpha kind = %v, want SymPackage", sym.Kind)
	}
	if sym.Package == nil || sym.Package.PkgScope == nil {
		t.Fatalf("alpha package = %#v, want linked package scope", sym.Package)
	}
	if helper := sym.Package.PkgScope.LookupLocal("helper"); helper == nil || !helper.Pub {
		t.Fatalf("alpha helper = %#v, want exported function in target scope", helper)
	}
}
