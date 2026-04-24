package check

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

func parseBuilderChainFile(t *testing.T, src string) *ast.File {
	t.Helper()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse diags: %v", parseDiags)
	}
	return file
}

func findBuilderChainLetValue(t *testing.T, f *ast.File, name string) ast.Expr {
	t.Helper()
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FnDecl)
		if !ok || fn.Body == nil {
			continue
		}
		for _, s := range fn.Body.Stmts {
			ls, ok := s.(*ast.LetStmt)
			if !ok {
				continue
			}
			id, ok := ls.Pattern.(*ast.IdentPat)
			if ok && id.Name == name {
				return ls.Value
			}
		}
	}
	t.Fatalf("let %q not found", name)
	return nil
}

// TestCheckFileLeavesBuilderChainIntact confirms that check.File no
// longer rewrites the public AST. The selfhost checker now types the
// original builder chain directly, and downstream IR lowering rewrites
// it to StructLit on demand.
func TestCheckFileLeavesBuilderChainIntact(t *testing.T) {
	src := `
pub struct Point {
    pub x: Int,
    pub y: Int,
}

fn main() {
    let p = Point.builder().x(3).y(4).build()
}
`
	f := parseBuilderChainFile(t, src)
	reg := stdlib.LoadCached()
	res := resolve.FileWithStdlib(f, resolve.NewPrelude(), reg)
	chk := File(f, res, Opts{
		Source:     []byte(src),
		Stdlib:     reg,
		Primitives: reg.Primitives,
	})
	for _, d := range chk.Diags {
		if d.Code == diag.CodeBuilderMissingRequiredField {
			t.Errorf("unexpected E0774 for valid chain: %v", d)
		}
	}
	val := findBuilderChainLetValue(t, f, "p")
	if _, ok := val.(*ast.CallExpr); !ok {
		t.Fatalf("check.File should leave the builder chain as a CallExpr, got %T", val)
	}
}

// TestCheckFileSurfacesE0774 verifies that the missing-required-field
// diagnostic produced by the selfhost builder checker reaches the
// top-level Result.Diags slice that callers consume.
func TestCheckFileSurfacesE0774(t *testing.T) {
	src := `
pub struct Point {
    pub x: Int,
    pub y: Int,
}

fn main() {
    let p = Point.builder().x(3).build()
}
`
	f := parseBuilderChainFile(t, src)
	reg := stdlib.LoadCached()
	res := resolve.FileWithStdlib(f, resolve.NewPrelude(), reg)
	chk := File(f, res, Opts{
		Source:     []byte(src),
		Stdlib:     reg,
		Primitives: reg.Primitives,
	})
	found := false
	for _, d := range chk.Diags {
		if d.Code != diag.CodeBuilderMissingRequiredField {
			continue
		}
		found = true
		if !strings.Contains(d.Message, "y") {
			t.Errorf("E0774 should name missing field `y`, got: %q", d.Message)
		}
	}
	if !found {
		t.Errorf("check.File did not surface E0774; diags=%v", chk.Diags)
	}
}

func TestCheckFileIgnoresBuilderMethodWithSelfForAutoDerive(t *testing.T) {
	src := `
pub struct Gadget {
    pub name: String,

    pub fn builder(self) -> Int { 0 }
}

fn main() {
    let g = Gadget.builder().name("ok").build()
}
`
	f := parseBuilderChainFile(t, src)
	reg := stdlib.LoadCached()
	res := resolve.FileWithStdlib(f, resolve.NewPrelude(), reg)
	chk := File(f, res, Opts{
		Source:     []byte(src),
		Stdlib:     reg,
		Primitives: reg.Primitives,
	})
	for _, d := range chk.Diags {
		t.Fatalf("unexpected diagnostic with builder(self) method present: %v", d)
	}
	val := findBuilderChainLetValue(t, f, "g")
	if _, ok := val.(*ast.CallExpr); !ok {
		t.Fatalf("check.File should leave the auto-derived builder chain intact, got %T", val)
	}
}
