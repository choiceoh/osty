package resolve

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
)

func TestMaterializePublicCompatibilityIsLazyAndIdempotent(t *testing.T) {
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

	pkg, err := LoadPackageForNative(dir)
	if err != nil {
		t.Fatalf("LoadPackageForNative: %v", err)
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

	pkg.MaterializePublicCompatibility()
	for _, pf := range pkg.Files {
		if pf.File == nil {
			t.Fatalf("MaterializePublicCompatibility left File nil for %s", pf.Path)
		}
	}

	pkg.MaterializePublicCompatibility()
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

func TestLoadPackageForNativeIncludesManifestBinPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "osty.toml"), []byte(`[package]
name = "desk"
version = "0.1.0"
edition = "0.5"

[bin]
path = "src/main.osty"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(dir, "src", "main.osty")
	if err := os.WriteFile(mainPath, []byte("pub fn main() { }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := LoadPackageForNative(dir)
	if err != nil {
		t.Fatalf("LoadPackageForNative: %v", err)
	}
	if len(pkg.Files) != 1 {
		t.Fatalf("files = %d, want 1 (%v)", len(pkg.Files), pkg.Files)
	}
	if got := filepath.Clean(pkg.Files[0].Path); got != filepath.Clean(mainPath) {
		t.Fatalf("file path = %q, want %q", got, mainPath)
	}
}

func TestWorkspacePackagePathsIncludesManifestBinPath(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "osty.toml"), []byte(`[package]
name = "desk"
version = "0.1.0"
edition = "0.5"

[bin]
path = "src/main.osty"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.osty"), []byte("pub fn main() { }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	paths := WorkspacePackagePaths(root)
	if len(paths) != 1 || paths[0] != "" {
		t.Fatalf("WorkspacePackagePaths = %v, want root package", paths)
	}
}

func TestLoadPackageForNativeWithTransformKeepsOriginalDiagnosticSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	original := []byte("func main() {\n    let value = 1\n}\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := LoadPackageForNativeWithTransform(dir, func(_ string, src []byte) []byte {
		return []byte("fn main() {\n    let value = 1\n}\n")
	})
	if err != nil {
		t.Fatalf("LoadPackageForNativeWithTransform: %v", err)
	}
	if len(pkg.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(pkg.Files))
	}
	pf := pkg.Files[0]
	if got := string(pf.Source); got[:2] != "fn" {
		t.Fatalf("Source prefix = %q, want transformed fn", got[:2])
	}
	if got := string(pf.DiagnosticSource()); got[:4] != "func" {
		t.Fatalf("DiagnosticSource prefix = %q, want original func", got[:4])
	}
	if pf.TransformMap == nil {
		t.Fatal("TransformMap is nil, want remap for changed source")
	}
}

func TestLoadPackageForNativeWithTransformerCanOptOutOfOriginalRemap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	if err := os.WriteFile(path, []byte("fn main() {\n    let value = 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := LoadPackageForNativeWithOptions(dir, LoadOptions{
		Transformer: func(_ string, _ []byte) SourceTransformResult {
			return SourceTransformResult{
				Source:   []byte("fn main() {\n    let value = 2\n}\n"),
				MapKnown: true,
			}
		},
	})
	if err != nil {
		t.Fatalf("LoadPackageForNativeWithOptions: %v", err)
	}
	pf := pkg.Files[0]
	if got := string(pf.DiagnosticSource()); got != string(pf.Source) {
		t.Fatalf("DiagnosticSource = %q, want transformed Source %q", got, string(pf.Source))
	}
	if pf.TransformMap != nil {
		t.Fatal("TransformMap is non-nil, want explicit opt-out")
	}
}

func TestWorkspacePackageGraphExposesLoadedEdges(t *testing.T) {
	root := t.TempDir()
	depDir := filepath.Join(root, "dep")
	if err := os.MkdirAll(depDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.osty"), []byte("pub use dep\nfn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(depDir, "lib.osty"), []byte("pub fn value() -> Int { 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	if _, err := ws.LoadPackageNative(""); err != nil {
		t.Fatalf("LoadPackageNative: %v", err)
	}
	graph := NewPackageGraph(ws)
	if len(graph.Packages) != 2 {
		t.Fatalf("graph packages = %d, want 2", len(graph.Packages))
	}
	if len(graph.Edges) != 1 {
		t.Fatalf("graph edges = %d, want 1", len(graph.Edges))
	}
	rootNode := graph.Packages[""]
	depNode := graph.Packages["dep"]
	if rootNode == nil || depNode == nil {
		t.Fatalf("graph packages = %#v, want root and dep", graph.Packages)
	}
	if rootNode.IsStdlib || rootNode.IsExternalDep || depNode.IsStdlib || depNode.IsExternalDep {
		t.Fatalf("graph package classifications = root %#v dep %#v, want workspace packages", rootNode, depNode)
	}
	edge := graph.Edges[0]
	if edge.From != "" || edge.To != "dep" || !edge.IsPub || edge.Kind != PackageGraphEdgeWorkspace {
		t.Fatalf("edge = %#v, want root pub edge to dep", edge)
	}
	if graph.Package("dep") != depNode.Package {
		t.Fatalf("graph.Package(\"dep\") = %#v, want dep package %#v", graph.Package("dep"), depNode.Package)
	}
}

func TestNativeResolveBridgeIndexesScriptScopeBindings(t *testing.T) {
	src := []byte("let x = 1\nlet y = x\n")
	file, parseDiags := parser.ParseDiagnostics(src)
	if len(parseDiags) != 0 {
		t.Fatalf("parse diagnostics: %v", parseDiags)
	}
	if len(file.Stmts) != 2 {
		t.Fatalf("script stmt count = %d, want 2", len(file.Stmts))
	}
	letY, ok := file.Stmts[1].(*ast.LetStmt)
	if !ok {
		t.Fatalf("second stmt = %T, want *ast.LetStmt", file.Stmts[1])
	}
	refX, ok := letY.Value.(*ast.Ident)
	if !ok {
		t.Fatalf("second let value = %T, want *ast.Ident", letY.Value)
	}

	res := ResolveFileSourceDefault(src, file, nil)
	for _, d := range res.Diags {
		if d != nil && d.Severity == diag.Error {
			t.Fatalf("resolve diagnostic: %s: %s", d.Code, d.Message)
		}
	}

	sym := res.RefsByID[refX.ID]
	if sym == nil {
		t.Fatalf("script ref %q was not bridged", refX.Name)
	}
	if sym.Kind != SymLet {
		t.Fatalf("script ref kind = %s, want %s", sym.Kind, SymLet)
	}
	binding, ok := sym.Decl.(*ast.IdentPat)
	if !ok {
		t.Fatalf("script ref decl = %T, want *ast.IdentPat", sym.Decl)
	}
	if binding.Name != "x" {
		t.Fatalf("script ref decl name = %q, want x", binding.Name)
	}
	if sym.Pos.Offset != binding.Pos().Offset {
		t.Fatalf("script ref pos offset = %d, want binding offset %d", sym.Pos.Offset, binding.Pos().Offset)
	}

	scopeSym := res.FileScope.Lookup("x")
	if scopeSym == nil {
		t.Fatal("script binding x missing from bridged file scope chain")
	}
	if _, ok := scopeSym.Decl.(*ast.IdentPat); !ok {
		t.Fatalf("script scope decl = %T, want *ast.IdentPat", scopeSym.Decl)
	}
}
