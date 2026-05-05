package parser

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/selfhost"
)

func TestParseDetailedKeepsFrontendRunAndUsesExplicitPublicCompatibility(t *testing.T) {
	src := []byte(`fn main() {
    let items = [1]
    let count = len(items)
}
`)

	selfhost.ResetAstbridgeLowerCount()
	result := ParseDetailed(src)
	if len(result.Diagnostics) > 0 {
		t.Fatalf("ParseDetailed diagnostics = %#v, want none", result.Diagnostics)
	}
	if result.Run == nil {
		t.Fatal("ParseDetailed Run = nil, want retained frontend run")
	}
	if result.File == nil {
		t.Fatal("ParseDetailed File = nil, want public compatibility AST")
	}
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("ParseDetailed astbridge count = %d, want 0", got)
	}
}

func TestParseMatchArmAllowsBareAssignmentBody(t *testing.T) {
	src := []byte(`struct Cookie { path: String }

fn update(value: String?) {
    let mut out = Cookie { path: "" }
    match value {
        Some(v) -> out.path = v,
        None -> {},
    }
}
`)

	result := ParseDetailed(src)
	if len(result.Diagnostics) > 0 {
		t.Fatalf("ParseDetailed diagnostics = %#v, want none", result.Diagnostics)
	}
	if result.File == nil || len(result.File.Decls) < 2 {
		t.Fatalf("parsed file = %#v, want struct and function declarations", result.File)
	}
	fn, ok := result.File.Decls[1].(*ast.FnDecl)
	if !ok || fn.Body == nil || len(fn.Body.Stmts) < 2 {
		t.Fatalf("decl[1] = %#v, want function with match statement", result.File.Decls[1])
	}
	stmt, ok := fn.Body.Stmts[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("stmt[1] = %#v, want expression statement", fn.Body.Stmts[1])
	}
	match, ok := stmt.X.(*ast.MatchExpr)
	if !ok || len(match.Arms) == 0 {
		t.Fatalf("stmt[1].X = %#v, want match expression with arms", stmt.X)
	}
	body, ok := match.Arms[0].Body.(*ast.Block)
	if !ok || len(body.Stmts) != 1 {
		t.Fatalf("arm body = %#v, want single-statement block", match.Arms[0].Body)
	}
	assign, ok := body.Stmts[0].(*ast.AssignStmt)
	if !ok || len(assign.Targets) != 1 {
		t.Fatalf("arm body stmt = %#v, want assignment statement", body.Stmts[0])
	}
	target, ok := assign.Targets[0].(*ast.FieldExpr)
	if !ok || target.Name != "path" {
		t.Fatalf("assignment target = %#v, want .path field assignment", assign.Targets[0])
	}
}
