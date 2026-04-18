package backend

import (
	"testing"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/stdlib"
)

// TestInjectReachableStdlibBodiesLowersAndRewrites drives the full inject
// helper on a user module that calls `strings.compare(a, b)`. Asserts:
//  1. injection returns one lowered fn with the mangled symbol name
//  2. the user callsite is rewritten to a bare Ident referencing that symbol
//
// Checker output is not plumbed yet (chk=nil in injectReachableStdlibBodies
// for stdlib decls), so we do not assert types on the lowered fn body.
func TestInjectReachableStdlibBodiesLowersAndRewrites(t *testing.T) {
	reg := stdlib.LoadCached()
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
	injected, issues := injectReachableStdlibBodies(mod, reg)
	for _, issue := range issues {
		t.Logf("non-fatal issue: %v", issue)
	}
	if len(injected) != 1 {
		t.Fatalf("injected = %d decls, want 1", len(injected))
	}
	fn, ok := injected[0].(*ir.FnDecl)
	if !ok {
		t.Fatalf("injected[0] = %T, want *ir.FnDecl", injected[0])
	}
	wantName := "osty_std_strings__compare"
	if fn.Name != wantName {
		t.Errorf("fn.Name = %q, want %q", fn.Name, wantName)
	}
	if fn.Body == nil {
		t.Errorf("fn.Body = nil, want lowered block")
	}
	// Callsite rewritten?
	ident, ok := call.Callee.(*ir.Ident)
	if !ok {
		t.Fatalf("callsite not rewritten: callee = %T, want *ir.Ident", call.Callee)
	}
	if ident.Name != wantName {
		t.Errorf("callsite ident = %q, want %q", ident.Name, wantName)
	}
}

func TestInjectReachableStdlibBodiesEmptyModule(t *testing.T) {
	reg := stdlib.LoadCached()
	mod := &ir.Module{Package: "main"}
	injected, issues := injectReachableStdlibBodies(mod, reg)
	if len(injected) != 0 {
		t.Fatalf("injected = %v, want none", injected)
	}
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issues)
	}
}
