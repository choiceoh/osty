package resolve

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/selfhost"
)

// TestLoadPackageForNativeMultiFileIsAstbridgeFree pins the PR6 wedge:
// running the package resolve path via LoadPackageForNative +
// NativeResolutionRows / NativeDiagnostics over a multi-file package
// must not trigger FrontendRun.File public lowering. The counter stays at zero
// throughout; calling EnsureFiles afterwards uses the explicit compatibility
// adapter and still does not bump FrontendRun.File.
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
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("after EnsureFiles: AstbridgeLowerCount = %d, want 0 (explicit compatibility adapter must not call FrontendRun.File)", got)
	}
	for _, pf := range pkg.Files {
		if pf.File == nil {
			t.Fatalf("EnsureFiles did not materialize pf.File for %s", pf.Path)
		}
	}

	pkg.EnsureFiles()
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("second EnsureFiles re-lowered through FrontendRun.File: AstbridgeLowerCount = %d, want 0", got)
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
	if _, err := ws.LoadPackage("beta"); err != nil {
		t.Fatalf("LoadPackage beta: %v", err)
	}
	if ws.Packages["alpha"] == nil {
		t.Fatalf("package import did not load package alpha")
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

func TestResolvePackageViaNativePlainUseStaysFileLocal(t *testing.T) {
	root := t.TempDir()
	alphaDir := filepath.Join(root, "alpha")
	betaDir := filepath.Join(root, "beta")
	if err := os.MkdirAll(alphaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(betaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alphaDir, "lib.osty"), []byte(`pub fn helper() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	betaPath := filepath.Join(betaDir, "lib.osty")
	if err := os.WriteFile(betaPath, []byte(`use alpha

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
	if _, err := ws.LoadPackage("beta"); err != nil {
		t.Fatalf("LoadPackage beta: %v", err)
	}
	if ws.Packages["alpha"] == nil {
		t.Fatalf("scoped member import did not load base package alpha")
	}
	results := ws.ResolveAll()
	if got := results["beta"]; got == nil || len(got.Diags) != 0 {
		t.Fatalf("beta diagnostics = %#v, want none", got)
	}
	beta := ws.Packages["beta"]
	if beta.PkgScope.LookupLocal("alpha") != nil {
		t.Fatalf("plain use leaked into package scope: %#v", beta.PkgScope.LookupLocal("alpha"))
	}
	if sym := beta.Files[0].FileScope.Lookup("alpha"); sym == nil || sym.Kind != SymPackage {
		t.Fatalf("file-scope alpha = %#v, want imported package alias", sym)
	}
}

func TestResolvePackageViaNativePubUseExportsPackageAlias(t *testing.T) {
	root := t.TempDir()
	alphaDir := filepath.Join(root, "alpha")
	betaDir := filepath.Join(root, "beta")
	if err := os.MkdirAll(alphaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(betaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alphaDir, "lib.osty"), []byte(`pub fn helper() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(betaDir, "lib.osty"), []byte(`pub use alpha
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
	if got := results["beta"]; got == nil || len(got.Diags) != 0 {
		t.Fatalf("beta diagnostics = %#v, want none", got)
	}
	beta := ws.Packages["beta"]
	sym := beta.PkgScope.LookupLocal("alpha")
	if sym == nil || sym.Kind != SymPackage || !sym.Pub {
		t.Fatalf("pub use alpha = %#v, want exported package alias", sym)
	}
	if sym.Package == nil || sym.Package.PkgScope == nil {
		t.Fatalf("pub use alpha package = %#v, want linked package", sym.Package)
	}
}

func TestResolvePackageViaNativeScopedUseBindsExportedMember(t *testing.T) {
	root := t.TempDir()
	alphaDir := filepath.Join(root, "alpha")
	betaDir := filepath.Join(root, "beta")
	if err := os.MkdirAll(alphaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(betaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alphaDir, "lib.osty"), []byte(`pub fn helper() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(betaDir, "lib.osty"), []byte(`use alpha::{helper as callHelper}

fn main() {
    let _ = callHelper()
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
	if got := results["beta"]; got == nil || len(got.Diags) != 0 {
		t.Fatalf("beta diagnostics = %#v, want none", got)
	}
	beta := ws.Packages["beta"]
	if beta.PkgScope.LookupLocal("callHelper") != nil {
		t.Fatalf("plain scoped use leaked into package scope: %#v", beta.PkgScope.LookupLocal("callHelper"))
	}
	sym := beta.Files[0].FileScope.Lookup("callHelper")
	if sym == nil || sym.Kind != SymFn || !sym.Pub {
		t.Fatalf("callHelper = %#v, want imported public function", sym)
	}
}

func TestResolvePackageViaNativePubScopedUseReexportsMember(t *testing.T) {
	root := t.TempDir()
	alphaDir := filepath.Join(root, "alpha")
	betaDir := filepath.Join(root, "beta")
	if err := os.MkdirAll(alphaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(betaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alphaDir, "lib.osty"), []byte(`pub fn helper() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(betaDir, "lib.osty"), []byte(`pub use alpha::{helper as exposed}
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
	if got := results["beta"]; got == nil || len(got.Diags) != 0 {
		t.Fatalf("beta diagnostics = %#v, want none", got)
	}
	beta := ws.Packages["beta"]
	sym := beta.PkgScope.LookupLocal("exposed")
	if sym == nil || sym.Kind != SymFn || !sym.Pub {
		t.Fatalf("pub scoped use exposed = %#v, want exported function alias", sym)
	}
}

func TestResolvePackageViaNativePubScopedUsePrivateMemberEmitsE0553(t *testing.T) {
	root := t.TempDir()
	alphaDir := filepath.Join(root, "alpha")
	betaDir := filepath.Join(root, "beta")
	if err := os.MkdirAll(alphaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(betaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(alphaDir, "lib.osty"), []byte(`fn hidden() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(betaDir, "lib.osty"), []byte(`pub use alpha::{hidden}
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
	betaResult := results["beta"]
	if betaResult == nil {
		t.Fatalf("missing beta result")
	}
	found := false
	for _, d := range betaResult.Diags {
		if d.Code == diag.CodeReexportPrivate {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s, got %#v", diag.CodeReexportPrivate, betaResult.Diags)
	}
	if sym := ws.Packages["beta"].PkgScope.LookupLocal("hidden"); sym != nil {
		t.Fatalf("private pub use should not export hidden, got %#v", sym)
	}
}

func TestResolveFileSourceDefaultDuplicateUseEmitsE0554(t *testing.T) {
	src := []byte(`use std.fs
use std.io as fs
`)
	file, parseDiags := parser.ParseDiagnostics(src)
	if len(parseDiags) != 0 {
		t.Fatalf("parse diagnostics = %#v, want none", parseDiags)
	}
	res := ResolveFileSourceDefault(src, file, nil)
	found := false
	for _, d := range res.Diags {
		if d.Code == diag.CodeUseDuplicateName {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s in diagnostics, got %#v", diag.CodeUseDuplicateName, res.Diags)
	}
}
