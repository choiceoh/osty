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

func TestStdlibMethodSymbolFormat(t *testing.T) {
	cases := []struct {
		module, typeName, method, want string
	}{
		{"encoding", "Hex", "encode", "osty_std_encoding__Hex__encode"},
		{"encoding", "Base64Url", "decode", "osty_std_encoding__Base64Url__decode"},
		{"option", "Option", "isSome", "osty_std_option__Option__isSome"},
	}
	for _, tc := range cases {
		got := StdlibMethodSymbol(tc.module, tc.typeName, tc.method)
		if got != tc.want {
			t.Errorf("StdlibMethodSymbol(%q, %q, %q) = %q, want %q",
				tc.module, tc.typeName, tc.method, got, tc.want)
		}
	}
}

func TestStdlibMethodSymbolDistinctFromStdlibSymbol(t *testing.T) {
	// Lock the no-collision property: a free fn `f` and a method
	// `T.f` in the same module must mangle to different symbols. The
	// `__<type>__` segment provides the gap.
	free := StdlibSymbol("encoding", "encode")
	method := StdlibMethodSymbol("encoding", "Hex", "encode")
	if free == method {
		t.Fatalf("StdlibSymbol(%q,%q)==StdlibMethodSymbol(%q,%q,%q)=%q — must differ",
			"encoding", "encode", "encoding", "Hex", "encode", free)
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

func TestRewriteStdlibMethodCallsitesEmpty(t *testing.T) {
	if got := RewriteStdlibMethodCallsites(nil, nil); got != 0 {
		t.Fatalf("nil inputs = %d, want 0", got)
	}
	mod := &ir.Module{Package: "main"}
	if got := RewriteStdlibMethodCallsites(mod, nil); got != 0 {
		t.Fatalf("empty reached = %d, want 0", got)
	}
}

func TestRewriteStdlibMethodCallsitesRewritesMatch(t *testing.T) {
	hexT := &ir.NamedType{Package: "encoding", Name: "Hex"}
	stringT := ir.TString
	bytesT := ir.TBytes
	receiver := &ir.Ident{Name: "h", T: hexT}
	mc := &ir.MethodCall{
		Receiver: receiver,
		Name:     "encode",
		Args:     []ir.Arg{{Value: &ir.Ident{Name: "data", T: bytesT}}},
		T:        stringT,
	}
	stmt := &ir.ExprStmt{X: mc}
	mod := &ir.Module{
		Package: "main",
		Script:  []ir.Stmt{stmt},
	}
	reached := []ReachableStdlibMethod{
		{Module: "encoding", Type: "Hex", Method: "encode", Fn: &ast.FnDecl{Name: "encode"}},
	}
	got := RewriteStdlibMethodCallsites(mod, reached)
	if got != 1 {
		t.Fatalf("rewrite count = %d, want 1", got)
	}
	// stmt.X should now be a CallExpr against the mangled symbol with
	// the receiver prepended as the first arg.
	call, ok := stmt.X.(*ir.CallExpr)
	if !ok {
		t.Fatalf("stmt.X = %T, want *ir.CallExpr after rewrite", stmt.X)
	}
	ident, ok := call.Callee.(*ir.Ident)
	if !ok {
		t.Fatalf("callee = %T, want *ir.Ident", call.Callee)
	}
	if ident.Name != "osty_std_encoding__Hex__encode" {
		t.Fatalf("callee name = %q, want osty_std_encoding__Hex__encode", ident.Name)
	}
	if ident.Kind != ir.IdentFn {
		t.Fatalf("callee kind = %v, want IdentFn", ident.Kind)
	}
	if len(call.Args) != 2 {
		t.Fatalf("args len = %d, want 2 (receiver + original)", len(call.Args))
	}
	if call.Args[0].Value != receiver {
		t.Fatalf("args[0] = %v, want original receiver pointer", call.Args[0].Value)
	}
	if call.T != stringT {
		t.Fatalf("call return T = %v, want preserved (%v)", call.T, stringT)
	}
}

func TestRewriteStdlibMethodCallsitesSkipsUserType(t *testing.T) {
	userT := &ir.NamedType{Package: "", Name: "MyStruct"}
	mc := &ir.MethodCall{
		Receiver: &ir.Ident{Name: "v", T: userT},
		Name:     "encode",
	}
	stmt := &ir.ExprStmt{X: mc}
	mod := &ir.Module{
		Package: "main",
		Script:  []ir.Stmt{stmt},
	}
	reached := []ReachableStdlibMethod{
		{Module: "encoding", Type: "Hex", Method: "encode", Fn: &ast.FnDecl{Name: "encode"}},
	}
	if got := RewriteStdlibMethodCallsites(mod, reached); got != 0 {
		t.Fatalf("rewrite count = %d, want 0 (user type)", got)
	}
	if _, ok := stmt.X.(*ir.MethodCall); !ok {
		t.Fatalf("stmt.X = %T, want unchanged *ir.MethodCall", stmt.X)
	}
}

func TestRewriteStdlibMethodCallsitesSkipsUnreachedMethod(t *testing.T) {
	// Receiver is a stdlib NamedType, but the (module, type, method)
	// triple isn't in `reached` → no rewrite.
	hexT := &ir.NamedType{Package: "encoding", Name: "Hex"}
	mc := &ir.MethodCall{
		Receiver: &ir.Ident{Name: "h", T: hexT},
		Name:     "decode", // reached set only contains encode
	}
	stmt := &ir.ExprStmt{X: mc}
	mod := &ir.Module{
		Package: "main",
		Script:  []ir.Stmt{stmt},
	}
	reached := []ReachableStdlibMethod{
		{Module: "encoding", Type: "Hex", Method: "encode", Fn: &ast.FnDecl{Name: "encode"}},
	}
	if got := RewriteStdlibMethodCallsites(mod, reached); got != 0 {
		t.Fatalf("rewrite count = %d, want 0 (method not in reached)", got)
	}
}

func TestRewriteStdlibMethodCallsitesRewritesInsideFnBody(t *testing.T) {
	// fn f() { h.encode(data) } — inside a fn body, with h having a
	// stdlib NamedType.
	hexT := &ir.NamedType{Package: "encoding", Name: "Hex"}
	mc := &ir.MethodCall{
		Receiver: &ir.Ident{Name: "h", T: hexT},
		Name:     "encode",
		Args:     []ir.Arg{{Value: &ir.Ident{Name: "data"}}},
	}
	stmt := &ir.ExprStmt{X: mc}
	fn := &ir.FnDecl{
		Name: "f",
		Body: &ir.Block{Stmts: []ir.Stmt{stmt}},
	}
	mod := &ir.Module{
		Package: "main",
		Decls:   []ir.Decl{fn},
	}
	reached := []ReachableStdlibMethod{
		{Module: "encoding", Type: "Hex", Method: "encode", Fn: &ast.FnDecl{Name: "encode"}},
	}
	if got := RewriteStdlibMethodCallsites(mod, reached); got != 1 {
		t.Fatalf("rewrite count = %d, want 1", got)
	}
	if _, ok := stmt.X.(*ir.CallExpr); !ok {
		t.Fatalf("nested stmt.X = %T, want *ir.CallExpr after rewrite", stmt.X)
	}
}

func TestRewriteStdlibMethodCallsitesRewritesNestedReceiver(t *testing.T) {
	// `outerHex.encode(innerB64.decode(text))` where the inner
	// argument is itself a stdlib method call.
	hexT := &ir.NamedType{Package: "encoding", Name: "Hex"}
	b64T := &ir.NamedType{Package: "encoding", Name: "Base64"}
	innerStmt := &ir.MethodCall{
		Receiver: &ir.Ident{Name: "innerB64", T: b64T},
		Name:     "decode",
		Args:     []ir.Arg{{Value: &ir.Ident{Name: "text"}}},
	}
	outerStmt := &ir.MethodCall{
		Receiver: &ir.Ident{Name: "outerHex", T: hexT},
		Name:     "encode",
		Args:     []ir.Arg{{Value: innerStmt}},
	}
	stmt := &ir.ExprStmt{X: outerStmt}
	mod := &ir.Module{
		Package: "main",
		Script:  []ir.Stmt{stmt},
	}
	reached := []ReachableStdlibMethod{
		{Module: "encoding", Type: "Hex", Method: "encode", Fn: &ast.FnDecl{Name: "encode"}},
		{Module: "encoding", Type: "Base64", Method: "decode", Fn: &ast.FnDecl{Name: "decode"}},
	}
	if got := RewriteStdlibMethodCallsites(mod, reached); got != 2 {
		t.Fatalf("rewrite count = %d, want 2 (outer + inner)", got)
	}
	outerCall, ok := stmt.X.(*ir.CallExpr)
	if !ok {
		t.Fatalf("outer stmt.X = %T, want *ir.CallExpr", stmt.X)
	}
	if len(outerCall.Args) != 2 {
		t.Fatalf("outer args = %d, want 2 (receiver + 1 orig arg)", len(outerCall.Args))
	}
	innerCall, ok := outerCall.Args[1].Value.(*ir.CallExpr)
	if !ok {
		t.Fatalf("inner outerCall.Args[1].Value = %T, want *ir.CallExpr (rewritten inner)", outerCall.Args[1].Value)
	}
	innerIdent, ok := innerCall.Callee.(*ir.Ident)
	if !ok || innerIdent.Name != "osty_std_encoding__Base64__decode" {
		t.Fatalf("inner callee = %v, want osty_std_encoding__Base64__decode", innerCall.Callee)
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
