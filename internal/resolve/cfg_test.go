package resolve

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
)

func parseSingleDeclForCfgTest(t *testing.T, src string) ast.Decl {
	t.Helper()
	file, diags := parser.ParseCanonical([]byte(src))
	if len(diags) != 0 {
		t.Fatalf("ParseCanonical diagnostics = %#v, want none", diags)
	}
	if file == nil || len(file.Decls) != 1 {
		t.Fatalf("parsed file = %#v, want one declaration", file)
	}
	return file.Decls[0]
}

func testCfgEnv() *CfgEnv {
	return &CfgEnv{
		OS:       "linux",
		Arch:     "amd64",
		Target:   "linux",
		Features: map[string]bool{},
	}
}

func TestEvaluateCfgOnDeclSuggestsNearestSupportedKey(t *testing.T) {
	decl := parseSingleDeclForCfgTest(t, `#[cfg(taret = "linux")]
fn main() {}
`)
	pass, ds := evaluateCfgOnDecl(decl, testCfgEnv())
	if pass {
		t.Fatal("evaluateCfgOnDecl passed, want false for unknown cfg key")
	}
	if len(ds) != 1 {
		t.Fatalf("diagnostics = %#v, want 1", ds)
	}
	if got := ds[0].Code; got != diag.CodeCfgUnknownKey {
		t.Fatalf("diag code = %q, want %q", got, diag.CodeCfgUnknownKey)
	}
	if got := ds[0].Hint; got != "did you mean `target`?" {
		t.Fatalf("diag hint = %q, want did-you-mean target", got)
	}
}

func TestCfgCompositionAllPasses(t *testing.T) {
	env := testCfgEnv()
	env.Features["debug"] = true

	decl := parseSingleDeclForCfgTest(t, `#[cfg(all(os = "linux", arch = "amd64"))]
fn guarded() {}
`)
	pass, ds := evaluateCfgOnDecl(decl, env)
	if len(ds) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", ds)
	}
	if !pass {
		t.Fatal("all(os=linux, arch=amd64) should pass on linux/amd64")
	}
}

func TestCfgCompositionAllFailsOnOne(t *testing.T) {
	env := testCfgEnv() // linux/amd64

	decl := parseSingleDeclForCfgTest(t, `#[cfg(all(os = "linux", arch = "arm64"))]
fn guarded() {}
`)
	pass, ds := evaluateCfgOnDecl(decl, env)
	if len(ds) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", ds)
	}
	if pass {
		t.Fatal("all(os=linux, arch=arm64) should fail on amd64")
	}
}

func TestCfgCompositionAnyPasses(t *testing.T) {
	env := testCfgEnv() // linux/amd64

	decl := parseSingleDeclForCfgTest(t, `#[cfg(any(os = "windows", arch = "amd64"))]
fn guarded() {}
`)
	pass, ds := evaluateCfgOnDecl(decl, env)
	if len(ds) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", ds)
	}
	if !pass {
		t.Fatal("any(os=windows, arch=amd64) should pass on linux/amd64")
	}
}

func TestCfgCompositionAnyFails(t *testing.T) {
	env := testCfgEnv() // linux/amd64

	decl := parseSingleDeclForCfgTest(t, `#[cfg(any(os = "windows", os = "darwin"))]
fn guarded() {}
`)
	pass, ds := evaluateCfgOnDecl(decl, env)
	if len(ds) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", ds)
	}
	if pass {
		t.Fatal("any(os=windows, os=darwin) should fail on linux")
	}
}

func TestCfgCompositionNot(t *testing.T) {
	env := testCfgEnv() // linux/amd64

	decl := parseSingleDeclForCfgTest(t, `#[cfg(not(os = "windows"))]
fn guarded() {}
`)
	pass, ds := evaluateCfgOnDecl(decl, env)
	if len(ds) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", ds)
	}
	if !pass {
		t.Fatal("not(os=windows) should pass on linux")
	}
}

func TestCfgCompositionNotFails(t *testing.T) {
	env := testCfgEnv() // linux/amd64

	decl := parseSingleDeclForCfgTest(t, `#[cfg(not(os = "linux"))]
fn guarded() {}
`)
	pass, ds := evaluateCfgOnDecl(decl, env)
	if len(ds) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", ds)
	}
	if pass {
		t.Fatal("not(os=linux) should fail on linux")
	}
}

func TestCfgCompositionNestedAllNot(t *testing.T) {
	env := testCfgEnv() // linux/amd64

	decl := parseSingleDeclForCfgTest(t, `#[cfg(all(os = "linux", not(arch = "arm64")))]
fn guarded() {}
`)
	pass, ds := evaluateCfgOnDecl(decl, env)
	if len(ds) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", ds)
	}
	if !pass {
		t.Fatal("all(os=linux, not(arch=arm64)) should pass on linux/amd64")
	}
}

func TestCfgCompositionFeature(t *testing.T) {
	env := testCfgEnv()
	env.Features["simd"] = true

	decl := parseSingleDeclForCfgTest(t, `#[cfg(all(os = "linux", feature = "simd"))]
fn guarded() {}
`)
	pass, ds := evaluateCfgOnDecl(decl, env)
	if len(ds) != 0 {
		t.Fatalf("unexpected diagnostics: %#v", ds)
	}
	if !pass {
		t.Fatal("all(os=linux, feature=simd) should pass when simd is enabled")
	}
}

func TestEvaluateCfgOnDeclSuggestsNearestSupportedComposition(t *testing.T) {
	decl := &ast.FnDecl{Annotations: []*ast.Annotation{{
		Name: "cfg",
		Args: []*ast.AnnotationArg{{
			Key: "al",
			Compose: []*ast.AnnotationArg{{
				Key:   "os",
				Value: &ast.StringLit{Parts: []ast.StringPart{{IsLit: true, Lit: "linux"}}},
			}},
		}},
	}}}
	pass, ds := evaluateCfgOnDecl(decl, testCfgEnv())
	if pass {
		t.Fatal("evaluateCfgOnDecl passed, want false for unknown cfg composition")
	}
	if len(ds) != 1 {
		t.Fatalf("diagnostics = %#v, want 1", ds)
	}
	if got := ds[0].Code; got != diag.CodeAnnotationBadArg {
		t.Fatalf("diag code = %q, want %q", got, diag.CodeAnnotationBadArg)
	}
	if got := ds[0].Hint; got != "did you mean `all`?" {
		t.Fatalf("diag hint = %q, want did-you-mean all", got)
	}
}
