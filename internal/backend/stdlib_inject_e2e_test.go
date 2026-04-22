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

// TestInjectReachableStdlibBodiesLowersStdlibMethod drives the inject
// helper on a user module that calls `option.Option.isSome` (via a
// receiver typed as the prelude Option). Asserts:
//  1. one method body is injected, named `osty_std_option__Option__isSome`
//  2. its first param is `self: option.Option`
//  3. the user callsite is rewritten from MethodCall to CallExpr
//     against the same mangled symbol, with the receiver prepended
//     as the first positional arg
//
// option.Option is the chosen target because (a) it's a real stdlib
// type with a bodied method, and (b) its Package qualifier
// ("option") flows through `NamedType.Package` exactly as the
// production lowerer would emit, so the method-reach path runs
// against realistic shape.
func TestInjectReachableStdlibBodiesLowersStdlibMethod(t *testing.T) {
	reg := stdlib.LoadCached()
	optTy := &ir.NamedType{Package: "option", Name: "Option", Args: []ir.Type{ir.TInt}}
	receiver := &ir.Ident{Name: "opt", T: optTy}
	mc := &ir.MethodCall{
		Receiver: receiver,
		Name:     "isSome",
		T:        ir.TBool,
	}
	stmt := &ir.ExprStmt{X: mc}
	mod := &ir.Module{
		Package: "main",
		Script:  []ir.Stmt{stmt},
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
	wantName := "osty_std_option__Option__isSome"
	if fn.Name != wantName {
		t.Errorf("fn.Name = %q, want %q", fn.Name, wantName)
	}
	if len(fn.Params) < 1 || fn.Params[0].Name != "self" {
		t.Fatalf("fn.Params[0] = %+v, want first param named self", fn.Params)
	}
	selfTy, ok := fn.Params[0].Type.(*ir.NamedType)
	if !ok || selfTy.Package != "option" || selfTy.Name != "Option" {
		t.Fatalf("self.Type = %v, want NamedType{Package:option, Name:Option}", fn.Params[0].Type)
	}
	if fn.ReceiverMut {
		t.Errorf("ReceiverMut = true, want false (free fn now owns self as a regular param)")
	}
	if fn.Body == nil {
		t.Errorf("fn.Body = nil, want lowered method block")
	}

	// Callsite rewritten to CallExpr against mangled name with the
	// receiver prepended as the first positional argument.
	call, ok := stmt.X.(*ir.CallExpr)
	if !ok {
		t.Fatalf("callsite not rewritten: stmt.X = %T, want *ir.CallExpr", stmt.X)
	}
	ident, ok := call.Callee.(*ir.Ident)
	if !ok {
		t.Fatalf("rewritten callee = %T, want *ir.Ident", call.Callee)
	}
	if ident.Name != wantName {
		t.Errorf("rewritten callee name = %q, want %q", ident.Name, wantName)
	}
	if len(call.Args) != 1 {
		t.Fatalf("rewritten args = %d, want 1 (receiver only)", len(call.Args))
	}
	if call.Args[0].Value != receiver {
		t.Fatalf("rewritten args[0] = %v, want original receiver pointer", call.Args[0].Value)
	}
}

// TestInjectReachableStdlibBodiesMixesFnsAndMethods ensures the
// injector handles a mod that calls both a stdlib free fn AND a
// stdlib method in the same compilation unit — they must be lowered
// independently and both rewriters must fire.
func TestInjectReachableStdlibBodiesMixesFnsAndMethods(t *testing.T) {
	reg := stdlib.LoadCached()
	freeCall := &ir.CallExpr{
		Callee: &ir.FieldExpr{X: &ir.Ident{Name: "strings"}, Name: "compare"},
		Args:   []ir.Arg{{Value: &ir.Ident{Name: "a"}}, {Value: &ir.Ident{Name: "b"}}},
	}
	optTy := &ir.NamedType{Package: "option", Name: "Option", Args: []ir.Type{ir.TInt}}
	mc := &ir.MethodCall{
		Receiver: &ir.Ident{Name: "opt", T: optTy},
		Name:     "isSome",
		T:        ir.TBool,
	}
	freeStmt := &ir.ExprStmt{X: freeCall}
	methodStmt := &ir.ExprStmt{X: mc}
	mod := &ir.Module{
		Package: "main",
		Script:  []ir.Stmt{freeStmt, methodStmt},
	}
	injected, _ := injectReachableStdlibBodies(mod, reg)
	if len(injected) != 2 {
		t.Fatalf("injected = %d, want 2 (one free + one method)", len(injected))
	}
	names := []string{}
	for _, d := range injected {
		if fn, ok := d.(*ir.FnDecl); ok {
			names = append(names, fn.Name)
		}
	}
	hasFree, hasMethod := false, false
	for _, n := range names {
		if n == "osty_std_strings__compare" {
			hasFree = true
		}
		if n == "osty_std_option__Option__isSome" {
			hasMethod = true
		}
	}
	if !hasFree || !hasMethod {
		t.Fatalf("injected names = %v, want both free and method symbols", names)
	}
	// Both call sites rewritten?
	if _, ok := freeCall.Callee.(*ir.Ident); !ok {
		t.Errorf("free callsite not rewritten: callee = %T", freeCall.Callee)
	}
	if _, ok := methodStmt.X.(*ir.CallExpr); !ok {
		t.Errorf("method callsite not rewritten: stmt.X = %T", methodStmt.X)
	}
}
