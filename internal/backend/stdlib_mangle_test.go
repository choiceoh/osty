package backend

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/ir"
)

func TestStdlibSymbolFormat(t *testing.T) {
	cases := []struct {
		module, name, want string
	}{
		{"strings", "compare", "osty_std_strings__compare"},
		{"collections", "groupBy", "osty_std_collections__groupBy"},
	}
	for _, tc := range cases {
		if got := StdlibSymbol(tc.module, tc.name); got != tc.want {
			t.Errorf("StdlibSymbol(%q, %q) = %q, want %q", tc.module, tc.name, got, tc.want)
		}
	}
}

func TestRewriteStdlibCallsitesEmpty(t *testing.T) {
	if got := RewriteStdlibCallsites(nil, nil); got != 0 {
		t.Fatalf("nil inputs = %d, want 0", got)
	}
	mod := &ir.Module{Package: "main"}
	if got := RewriteStdlibCallsites(mod, nil); got != 0 {
		t.Fatalf("empty reached = %d, want 0", got)
	}
}

func TestRewriteStdlibCallsitesRewritesMatch(t *testing.T) {
	call := &ir.CallExpr{
		Callee: &ir.FieldExpr{X: &ir.Ident{Name: "strings"}, Name: "compare"},
		Args: []ir.Arg{
			{Value: &ir.Ident{Name: "a"}},
			{Value: &ir.Ident{Name: "b"}},
		},
	}
	mod := &ir.Module{
		Package: "main",
		Script:  []ir.Stmt{&ir.ExprStmt{X: call}},
	}
	reached := []ReachableStdlibFn{
		{Module: "strings", Fn: &ast.FnDecl{Name: "compare"}},
	}
	got := RewriteStdlibCallsites(mod, reached)
	if got != 1 {
		t.Fatalf("rewrite count = %d, want 1", got)
	}
	ident, ok := call.Callee.(*ir.Ident)
	if !ok {
		t.Fatalf("callee = %T, want *ir.Ident after rewrite", call.Callee)
	}
	if ident.Name != "osty_std_strings__compare" {
		t.Fatalf("callee name = %q, want osty_std_strings__compare", ident.Name)
	}
	if ident.Kind != ir.IdentFn {
		t.Fatalf("callee kind = %v, want IdentFn", ident.Kind)
	}
}

func TestRewriteStdlibCallsitesLeavesNonMatchesAlone(t *testing.T) {
	// `userAlias.compare(x)` must not be rewritten even if "compare" is reached
	// under a different module.
	call := &ir.CallExpr{
		Callee: &ir.FieldExpr{X: &ir.Ident{Name: "userAlias"}, Name: "compare"},
		Args:   []ir.Arg{{Value: &ir.Ident{Name: "x"}}},
	}
	mod := &ir.Module{
		Package: "main",
		Script:  []ir.Stmt{&ir.ExprStmt{X: call}},
	}
	reached := []ReachableStdlibFn{
		{Module: "strings", Fn: &ast.FnDecl{Name: "compare"}},
	}
	if got := RewriteStdlibCallsites(mod, reached); got != 0 {
		t.Fatalf("rewrite count = %d, want 0 (qualifier mismatch)", got)
	}
	if _, ok := call.Callee.(*ir.FieldExpr); !ok {
		t.Fatalf("callee = %T, want unchanged *ir.FieldExpr", call.Callee)
	}
}

func TestRewriteStdlibCallsitesRewritesNested(t *testing.T) {
	// Inside a fn body: `fn f() { strings.compare(a, b) }`
	inner := &ir.CallExpr{
		Callee: &ir.FieldExpr{X: &ir.Ident{Name: "strings"}, Name: "compare"},
	}
	fn := &ir.FnDecl{
		Name: "f",
		Body: &ir.Block{Stmts: []ir.Stmt{&ir.ExprStmt{X: inner}}},
	}
	mod := &ir.Module{
		Package: "main",
		Decls:   []ir.Decl{fn},
	}
	reached := []ReachableStdlibFn{
		{Module: "strings", Fn: &ast.FnDecl{Name: "compare"}},
	}
	if got := RewriteStdlibCallsites(mod, reached); got != 1 {
		t.Fatalf("rewrite count = %d, want 1", got)
	}
	if _, ok := inner.Callee.(*ir.Ident); !ok {
		t.Fatalf("nested callee not rewritten: %T", inner.Callee)
	}
}
