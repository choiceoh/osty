package ir

import (
	"testing"

	"github.com/osty/osty/internal/ast"
)

func TestLowerFnDeclNilInput(t *testing.T) {
	fn, issues := LowerFnDecl("main", nil, nil, nil)
	if fn != nil {
		t.Fatalf("fn = %v, want nil", fn)
	}
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}

func TestLowerFnDeclMinimalBody(t *testing.T) {
	// fn answer() -> Int { 42 }
	body := &ast.Block{
		Stmts: []ast.Stmt{&ast.ExprStmt{X: &ast.IntLit{Text: "42"}}},
	}
	fn := &ast.FnDecl{
		Name:       "answer",
		Pub:        true,
		ReturnType: &ast.NamedType{Path: []string{"Int"}},
		Body:       body,
	}
	out, issues := LowerFnDecl("main", fn, nil, nil)
	if out == nil {
		t.Fatalf("LowerFnDecl returned nil fn")
	}
	if out.Name != "answer" {
		t.Fatalf("out.Name = %q, want answer", out.Name)
	}
	if !out.Exported {
		t.Fatalf("out.Exported = false, want true (pub)")
	}
	if out.Body == nil {
		t.Fatalf("out.Body = nil, want lowered block")
	}
	for _, issue := range issues {
		t.Logf("issue: %v", issue)
	}
}

func TestLowerFnDeclMethodSkipsReceiverAtTopLevel(t *testing.T) {
	// A fn with Recv (method) still lowers through LowerFnDecl because the
	// caller explicitly asked for it — top-level skip logic lives in
	// lowerDecl, not lowerFnDecl. This matters for stdlib injection where
	// a single method body may need standalone lowering.
	fn := &ast.FnDecl{
		Name: "method",
		Recv: &ast.Receiver{Mut: true},
		Body: &ast.Block{Stmts: []ast.Stmt{&ast.ExprStmt{X: &ast.IntLit{Text: "1"}}}},
	}
	out, _ := LowerFnDecl("main", fn, nil, nil)
	if out == nil {
		t.Fatalf("LowerFnDecl(method) = nil, want non-nil (receiver handled by caller)")
	}
	if !out.ReceiverMut {
		t.Fatalf("out.ReceiverMut = false, want true")
	}
}
