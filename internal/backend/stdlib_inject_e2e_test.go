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
// Checker output is plumbed through the cached stdlibCheckResult helper.
// Type propagation into the lowered body is verified by
// TestInjectReachableStdlibBodiesPropagatesTypes below.
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

// TestInjectReachableStdlibBodiesPropagatesTypes verifies the injected
// stdlib fn carries real types in its IR (not ErrTypeVal fallback). We
// walk the lowered body for any Expr whose Type() is non-nil and not
// ErrTypeVal, and assert at least one such expression exists. A precise
// shape check would brittle-test the strings.compare body; this loose
// assertion catches the regression where chk drops out of the plumbing.
func TestInjectReachableStdlibBodiesPropagatesTypes(t *testing.T) {
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
	injected, _ := injectReachableStdlibBodies(mod, reg)
	if len(injected) != 1 {
		t.Fatalf("injected = %d, want 1", len(injected))
	}
	fn := injected[0].(*ir.FnDecl)

	typedCount := 0
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		if expr, ok := n.(ir.Expr); ok {
			t := expr.Type()
			if t != nil && t != ir.ErrTypeVal {
				typedCount++
			}
		}
		return true
	}), fn)
	if typedCount == 0 {
		t.Fatalf("lowered strings.compare body has no typed expressions; checker context missing")
	}
	t.Logf("typed expressions in lowered body: %d", typedCount)
}
