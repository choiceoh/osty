package ir

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
)

func TestLowerClassifiesTopLevelLetRefsAsGlobal(t *testing.T) {
	global := &ast.LetDecl{
		Name:  "g",
		Value: &ast.IntLit{Text: "1"},
	}
	ref := &ast.Ident{Name: "g"}
	closure := &ast.ClosureExpr{Body: ref}
	file := &ast.File{
		Decls: []ast.Decl{global},
		Stmts: []ast.Stmt{&ast.ExprStmt{X: closure}},
	}
	res := &resolve.Result{
		Refs: map[*ast.Ident]*resolve.Symbol{
			ref: {Name: "g", Kind: resolve.SymLet, Decl: global},
		},
	}

	mod, issues := Lower("main", file, res, nil)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}

	stmt, ok := mod.Script[0].(*ExprStmt)
	if !ok {
		t.Fatalf("script[0] = %T, want *ExprStmt", mod.Script[0])
	}
	cl, ok := stmt.X.(*Closure)
	if !ok {
		t.Fatalf("stmt.X = %T, want *Closure", stmt.X)
	}
	if len(cl.Captures) != 1 {
		t.Fatalf("closure captures = %+v, want 1 global capture", cl.Captures)
	}
	if cl.Captures[0].Kind != CaptureGlobal {
		t.Fatalf("capture kind = %v, want %v", cl.Captures[0].Kind, CaptureGlobal)
	}
	id, ok := cl.Body.Result.(*Ident)
	if !ok {
		t.Fatalf("closure body result = %T, want *Ident", cl.Body.Result)
	}
	if id.Kind != IdentGlobal {
		t.Fatalf("ident kind = %v, want %v", id.Kind, IdentGlobal)
	}
}

func TestLowerPreludeVariantCallBecomesVariantLit(t *testing.T) {
	some := &ast.Ident{Name: "Some"}
	call := &ast.CallExpr{
		Fn: some,
		Args: []*ast.Arg{{
			Value: &ast.IntLit{Text: "1"},
		}},
	}
	file := &ast.File{
		Stmts: []ast.Stmt{&ast.ExprStmt{X: call}},
	}
	res := &resolve.Result{
		Refs: map[*ast.Ident]*resolve.Symbol{
			some: {Name: "Some", Kind: resolve.SymBuiltin, Pub: true},
		},
	}

	mod, issues := Lower("main", file, res, nil)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}

	stmt, ok := mod.Script[0].(*ExprStmt)
	if !ok {
		t.Fatalf("script[0] = %T, want *ExprStmt", mod.Script[0])
	}
	lit, ok := stmt.X.(*VariantLit)
	if !ok {
		t.Fatalf("stmt.X = %T, want *VariantLit", stmt.X)
	}
	if lit.Enum != "" {
		t.Fatalf("variant enum = %q, want empty prelude enum", lit.Enum)
	}
	if lit.Variant != "Some" {
		t.Fatalf("variant name = %q, want %q", lit.Variant, "Some")
	}
	if got := len(lit.Args); got != 1 {
		t.Fatalf("variant args = %d, want 1", got)
	}
}

func TestLowerStatementIfBecomesIfStmt(t *testing.T) {
	cond := &ast.BoolLit{Value: true}
	ifExpr := &ast.IfExpr{
		Cond: cond,
		Then: &ast.Block{
			Stmts: []ast.Stmt{&ast.ExprStmt{X: &ast.IntLit{Text: "1"}}},
		},
	}
	file := &ast.File{
		Stmts: []ast.Stmt{&ast.ExprStmt{X: ifExpr}},
	}

	mod, issues := Lower("main", file, nil, nil)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}

	if _, ok := mod.Script[0].(*IfStmt); !ok {
		t.Fatalf("script[0] = %T, want *IfStmt", mod.Script[0])
	}
}

func TestLowerStatementMatchBecomesMatchStmt(t *testing.T) {
	match := &ast.MatchExpr{
		Scrutinee: &ast.IntLit{Text: "1"},
		Arms: []*ast.MatchArm{
			{
				Pattern: &ast.LiteralPat{Literal: &ast.IntLit{Text: "1"}},
				Body:    &ast.IntLit{Text: "10"},
			},
			{
				Pattern: &ast.WildcardPat{},
				Body:    &ast.IntLit{Text: "0"},
			},
		},
	}
	file := &ast.File{
		Stmts: []ast.Stmt{&ast.ExprStmt{X: match}},
	}

	mod, issues := Lower("main", file, nil, nil)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}

	stmt, ok := mod.Script[0].(*MatchStmt)
	if !ok {
		t.Fatalf("script[0] = %T, want *MatchStmt", mod.Script[0])
	}
	if stmt.Tree == nil {
		t.Fatal("MatchStmt.Tree = nil, want compiled decision tree")
	}
	if got := len(stmt.Arms); got != 2 {
		t.Fatalf("match arms = %d, want 2", got)
	}
}
