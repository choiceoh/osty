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
