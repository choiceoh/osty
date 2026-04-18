package resolve

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/parser"
)

// anyDeclName extracts the user-visible name of any top-level
// declaration kind that `filterCfgDecls` can retain. The package's
// internal `declName` only handles struct/enum for partial-merge
// tracking; the cfg test needs FnDecl coverage too.
func anyDeclName(d ast.Decl) string {
	switch n := d.(type) {
	case *ast.FnDecl:
		return n.Name
	case *ast.StructDecl:
		return n.Name
	case *ast.EnumDecl:
		return n.Name
	case *ast.InterfaceDecl:
		return n.Name
	case *ast.TypeAliasDecl:
		return n.Name
	case *ast.LetDecl:
		return n.Name
	}
	return ""
}

// CfgEnv wiring is tested end-to-end by parsing a file with a
// `#[cfg(...)]`-annotated declaration, running ResolveAll with a
// controlled environment, and asserting the declaration was either
// retained or dropped.

func TestCfgFilterDropsNonMatchingOS(t *testing.T) {
	src := []byte(`
#[cfg(os = "linux")]
fn linuxOnly() -> Int { 42 }

#[cfg(os = "darwin")]
fn darwinOnly() -> Int { 99 }

fn unconditional() -> Int { 1 }
`)
	file, diags := parser.ParseDiagnostics(src)
	if file == nil {
		t.Fatalf("parse failed: %v", diags)
	}
	env := &CfgEnv{OS: "linux", Arch: "amd64", Target: "linux", Features: map[string]bool{}}
	ds := filterCfgDecls(file, env)
	if len(ds) != 0 {
		t.Fatalf("unexpected cfg diagnostics: %v", ds)
	}
	if len(file.Decls) != 2 {
		t.Fatalf("expected 2 decls after cfg filter, got %d", len(file.Decls))
	}
	seen := map[string]bool{}
	for _, d := range file.Decls {
		seen[anyDeclName(d)] = true
	}
	if !seen["linuxOnly"] {
		t.Errorf("linuxOnly should survive cfg filter under os=linux")
	}
	if seen["darwinOnly"] {
		t.Errorf("darwinOnly should be dropped under os=linux")
	}
	if !seen["unconditional"] {
		t.Errorf("unannotated fn should always survive")
	}
}

func TestCfgFilterFeatureFlag(t *testing.T) {
	src := []byte(`
#[cfg(feature = "ssl")]
fn withSsl() -> Int { 1 }

#[cfg(feature = "tls")]
fn withTls() -> Int { 2 }
`)
	file, _ := parser.ParseDiagnostics(src)
	env := &CfgEnv{OS: "linux", Arch: "amd64", Target: "linux", Features: map[string]bool{"ssl": true}}
	_ = filterCfgDecls(file, env)
	if len(file.Decls) != 1 {
		t.Fatalf("expected 1 decl after feature filter, got %d", len(file.Decls))
	}
	if anyDeclName(file.Decls[0]) != "withSsl" {
		t.Errorf("expected withSsl, got %s", anyDeclName(file.Decls[0]))
	}
}

func TestCfgFilterMultipleCfgsConjoin(t *testing.T) {
	src := []byte(`
#[cfg(os = "linux")]
#[cfg(arch = "amd64")]
fn linuxAmd64() -> Int { 1 }

#[cfg(os = "linux")]
#[cfg(arch = "arm64")]
fn linuxArm64() -> Int { 2 }
`)
	file, _ := parser.ParseDiagnostics(src)
	env := &CfgEnv{OS: "linux", Arch: "amd64", Target: "linux", Features: map[string]bool{}}
	_ = filterCfgDecls(file, env)
	if len(file.Decls) != 1 {
		t.Fatalf("expected 1 decl under (linux && amd64), got %d", len(file.Decls))
	}
	if anyDeclName(file.Decls[0]) != "linuxAmd64" {
		t.Errorf("expected linuxAmd64, got %s", anyDeclName(file.Decls[0]))
	}
}

func TestCfgFilterUnknownKey(t *testing.T) {
	src := []byte(`
#[cfg(bogus = "value")]
fn bad() -> Int { 1 }
`)
	file, _ := parser.ParseDiagnostics(src)
	env := DefaultCfgEnv()
	ds := filterCfgDecls(file, env)
	if len(ds) != 1 {
		t.Fatalf("expected 1 diagnostic for unknown cfg key, got %d", len(ds))
	}
	if ds[0].Code != "E0405" {
		t.Errorf("expected E0405 for unknown cfg key, got %s", ds[0].Code)
	}
	// Declaration drops because unknown key evaluates to false.
	if len(file.Decls) != 0 {
		t.Errorf("declaration with unknown cfg key should be dropped, got %d decls", len(file.Decls))
	}
}

func TestCfgFilterTargetMismatch(t *testing.T) {
	src := []byte(`
#[cfg(target = "wasm")]
fn wasmOnly() -> Int { 1 }
`)
	file, _ := parser.ParseDiagnostics(src)
	env := &CfgEnv{OS: "linux", Arch: "amd64", Target: "linux", Features: map[string]bool{}}
	_ = filterCfgDecls(file, env)
	if len(file.Decls) != 0 {
		t.Errorf("wasmOnly should drop when target=linux, got %d decls", len(file.Decls))
	}
}

func TestCfgFilterPreservesUnannotatedDecls(t *testing.T) {
	src := []byte(`
fn plain() -> Int { 1 }
struct Empty {}
`)
	file, _ := parser.ParseDiagnostics(src)
	_ = filterCfgDecls(file, DefaultCfgEnv())
	if len(file.Decls) != 2 {
		t.Errorf("unannotated decls should all survive, got %d", len(file.Decls))
	}
}
