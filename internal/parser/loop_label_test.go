package parser

import (
	"testing"

	"github.com/osty/osty/internal/ast"
)

func TestParseLabeledForStmt(t *testing.T) {
	src := []byte(`fn main() {
    let items = [1]
    'outer: for item in items {
        continue 'outer
    }
}
`)

	file, diags := ParseDiagnostics(src)
	if len(diags) > 0 {
		t.Fatalf("ParseDiagnostics returned %d diagnostics: %v", len(diags), diags[0])
	}
	fn := file.Decls[0].(*ast.FnDecl)
	loop, ok := fn.Body.Stmts[1].(*ast.ForStmt)
	if !ok {
		t.Fatalf("stmt[1] type = %T, want *ast.ForStmt", fn.Body.Stmts[1])
	}
	if got, want := loop.Label, "outer"; got != want {
		t.Fatalf("for label = %q, want %q", got, want)
	}
	cont, ok := loop.Body.Stmts[0].(*ast.ContinueStmt)
	if !ok {
		t.Fatalf("loop body stmt type = %T, want *ast.ContinueStmt", loop.Body.Stmts[0])
	}
	if got, want := cont.Label, "outer"; got != want {
		t.Fatalf("continue label = %q, want %q", got, want)
	}
}

func TestParseLabeledLoopExpr(t *testing.T) {
	src := []byte(`fn main() {
    let value = 'retry: loop {
        break 'retry 1
    }
}
`)

	file, diags := ParseDiagnostics(src)
	if len(diags) > 0 {
		t.Fatalf("ParseDiagnostics returned %d diagnostics: %v", len(diags), diags[0])
	}
	fn := file.Decls[0].(*ast.FnDecl)
	bind, ok := fn.Body.Stmts[0].(*ast.LetStmt)
	if !ok {
		t.Fatalf("stmt[0] type = %T, want *ast.LetStmt", fn.Body.Stmts[0])
	}
	loop, ok := bind.Value.(*ast.LoopExpr)
	if !ok {
		t.Fatalf("binding value type = %T, want *ast.LoopExpr", bind.Value)
	}
	if got, want := loop.Label, "retry"; got != want {
		t.Fatalf("loop label = %q, want %q", got, want)
	}
	brk, ok := loop.Body.Stmts[0].(*ast.BreakStmt)
	if !ok {
		t.Fatalf("loop body stmt type = %T, want *ast.BreakStmt", loop.Body.Stmts[0])
	}
	if got, want := brk.Label, "retry"; got != want {
		t.Fatalf("break label = %q, want %q", got, want)
	}
	if _, ok := brk.Value.(*ast.IntLit); !ok {
		t.Fatalf("break value type = %T, want *ast.IntLit", brk.Value)
	}
}
