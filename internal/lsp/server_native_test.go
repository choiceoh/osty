package lsp

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/ast"
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
	// The engine-based package path routes through parser.ParseDetailed,
	// which calls run.File() once per file. With 2 .osty files in this
	// package the count is 2. The legacy path (LowerPublicFileFromRun)
	// was astbridge-free; the engine path trades that for incremental
	// caching. Assert an upper bound to catch regressions without
	// pinning the exact count.
	if got := selfhost.AstbridgeLowerCount(); got > 4 {
		t.Fatalf("AstbridgeLowerCount after package analyze = %d, want <= 4", got)
	}
	if len(a.packages) != 1 {
		t.Fatalf("packages = %d, want 1", len(a.packages))
	}
	if got := lspFirstFnName(a.file); got != "fresh" {
		t.Fatalf("first function = %q, want %q", got, "fresh")
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
	// Phase 1c.4 retired the Go-legacy \`analyzeSingleFile\` this test
	// originally pinned to zero astbridge lowerings. The engine path
	// reuses selfhost.LowerPublicFileFromRun for public-AST lift and
	// still routes through a single astbridge step for the linter's
	// diagnostic-stamping pass, so the invariant relaxed to "at most
	// one" — the public-AST surface itself stays astbridge-free.
	if got := selfhost.AstbridgeLowerCount(); got > 1 {
		t.Fatalf("AstbridgeLowerCount after single-file analyze = %d, want <= 1", got)
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
	// The engine-based workspace path routes through parser.ParseDetailed,
	// which calls run.File() once per file. With 2 .osty files across
	// the workspace the count is 2. The legacy path was astbridge-free;
	// the engine path trades that for incremental caching. Assert an
	// upper bound to catch regressions without pinning the exact count.
	if got := selfhost.AstbridgeLowerCount(); got > 6 {
		t.Fatalf("AstbridgeLowerCount after workspace analyze = %d, want <= 6", got)
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
