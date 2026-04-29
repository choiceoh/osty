package lsp

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
)

func TestAnalyzePackageContainingUsesNativeCompatibilityPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	helperPath := filepath.Join(dir, "helper.osty")
	if err := os.WriteFile(path, []byte("pub fn stale() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.osty: %v", err)
	}
	if err := os.WriteFile(helperPath, []byte("pub fn helper() -> Int { 1 }\n"), 0o644); err != nil {
		t.Fatalf("write helper.osty: %v", err)
	}

	src := []byte("pub fn fresh() {}\n")
	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	selfhost.ResetAstbridgeLowerCount()
	a := s.analyzePackageContaining(path, src)
	if a == nil {
		t.Fatal("analyzePackageContaining returned nil")
	}
	// The engine-based package path may still materialize public ASTs for
	// compatibility consumers, but parser.ParseDetailed must use the explicit
	// adapter rather than FrontendRun.File's legacy astbridge entry point.
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("AstbridgeLowerCount after package analyze = %d, want 0", got)
	}
	if len(a.packages) != 1 {
		t.Fatalf("packages = %d, want 1", len(a.packages))
	}
	if got := lspFirstFnName(a.file); got != "fresh" {
		t.Fatalf("first function = %q, want %q", got, "fresh")
	}
}

func TestAnalyzePackageDiagnosticsUseNativeResolveResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	helperPath := filepath.Join(dir, "helper.osty")
	if err := os.WriteFile(path, []byte("pub fn stale() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.osty: %v", err)
	}
	if err := os.WriteFile(helperPath, []byte("pub fn helper() -> Int { 1 }\n"), 0o644); err != nil {
		t.Fatalf("write helper.osty: %v", err)
	}

	src := []byte("fn main() { missing() }\n")
	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	selfhost.ResetAstbridgeLowerCount()
	a := s.analyzePackageContaining(path, src)
	if a == nil {
		t.Fatal("analyzePackageContaining returned nil")
	}
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("AstbridgeLowerCount after package diagnostics = %d, want 0", got)
	}
	var sawUndefined bool
	for _, d := range a.diags {
		if d != nil && d.Code == diag.CodeUndefinedName {
			sawUndefined = true
			if d.File != path {
				t.Fatalf("diagnostic file = %q, want %q", d.File, path)
			}
		}
	}
	if !sawUndefined {
		t.Fatalf("diagnostics = %#v, want undefined-name diagnostic", a.diags)
	}
}

func TestAnalyzePackageIdentIndexUsesNativeStructuredResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	helperPath := filepath.Join(dir, "helper.osty")
	if err := os.WriteFile(path, []byte("pub fn stale() {}\n"), 0o644); err != nil {
		t.Fatalf("write main.osty: %v", err)
	}
	if err := os.WriteFile(helperPath, []byte("pub fn helper() -> Int { 1 }\n"), 0o644); err != nil {
		t.Fatalf("write helper.osty: %v", err)
	}

	src := []byte("fn main() { helper() }\n")
	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	selfhost.ResetAstbridgeLowerCount()
	a := s.analyzePackageContaining(path, src)
	if a == nil {
		t.Fatal("analyzePackageContaining returned nil")
	}
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("AstbridgeLowerCount after package ident index = %d, want 0", got)
	}
	helperOff := bytes.Index(src, []byte("helper"))
	if helperOff < 0 {
		t.Fatal("test source missing helper call")
	}
	if got := a.identIndex[helperOff]; got != "function" {
		t.Fatalf("identIndex[%d] = %q, want function (idx=%#v)", helperOff, got, a.identIndex)
	}
}

func TestAnalyzeSingleFileUsesNativeCompatibilityPath(t *testing.T) {
	src := []byte("pub fn fresh() {}\n")
	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	selfhost.ResetAstbridgeLowerCount()
	a := s.analyzeSingleFileViaEngine("untitled:NativeCompat.osty", src)
	if a == nil {
		t.Fatal("analyzeSingleFileViaEngine returned nil")
	}
	// The public-AST compatibility surface stays explicit; no LSP single-file
	// analysis path should call FrontendRun.File.
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("AstbridgeLowerCount after single-file analyze = %d, want 0", got)
	}
	if got := lspFirstFnName(a.file); got != "fresh" {
		t.Fatalf("first function = %q, want %q", got, "fresh")
	}
}

func TestAnalyzeWorkspaceUsesNativeCompatibilityPath(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "app")
	libDir := filepath.Join(root, "lib")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("mkdir app: %v", err)
	}
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatalf("mkdir lib: %v", err)
	}

	path := filepath.Join(appDir, "main.osty")
	libPath := filepath.Join(libDir, "util.osty")
	if err := os.WriteFile(path, []byte("pub fn stale() {}\n"), 0o644); err != nil {
		t.Fatalf("write app/main.osty: %v", err)
	}
	if err := os.WriteFile(libPath, []byte("pub fn helper() -> Int { 1 }\n"), 0o644); err != nil {
		t.Fatalf("write lib/util.osty: %v", err)
	}

	src := []byte("pub fn fresh() {}\n")
	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	selfhost.ResetAstbridgeLowerCount()
	a := s.analyzePackageContaining(path, src)
	if a == nil {
		t.Fatal("analyzePackageContaining returned nil")
	}
	// Workspace analysis can still expose public ASTs to compatibility
	// consumers, but it must not use FrontendRun.File as the authority path.
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("AstbridgeLowerCount after workspace analyze = %d, want 0", got)
	}
	if len(a.packages) != 2 {
		t.Fatalf("packages = %d, want 2", len(a.packages))
	}
	if got := lspFirstFnName(a.file); got != "fresh" {
		t.Fatalf("first function = %q, want %q", got, "fresh")
	}
}

func lspFirstFnName(file *ast.File) string {
	if file == nil {
		return ""
	}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FnDecl); ok {
			return fn.Name
		}
	}
	return ""
}
