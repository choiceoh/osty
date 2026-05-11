package ir

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost/api"
	"github.com/osty/osty/internal/stdlib"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

func TestLowerClassifiesTopLevelLetRefsAsGlobal(t *testing.T) {
	global := &ast.LetDecl{
		Name:  "g",
		Value: &ast.IntLit{Text: "1"},
	}
	ref := &ast.Ident{ID: 1, Name: "g"}
	closure := &ast.ClosureExpr{Body: ref}
	file := &ast.File{
		Decls: []ast.Decl{global},
		Stmts: []ast.Stmt{&ast.ExprStmt{X: closure}},
	}
	res := &resolve.Result{
		RefsByID: map[ast.NodeID]*resolve.Symbol{
			ref.ID: {Name: "g", Kind: resolve.SymLet, Decl: global},
		},
		RefIdents: []*ast.Ident{ref},
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
	some := &ast.Ident{ID: 1, Name: "Some"}
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
		RefsByID: map[ast.NodeID]*resolve.Symbol{
			some.ID: {Name: "Some", Kind: resolve.SymBuiltin, Pub: true},
		},
		RefIdents: []*ast.Ident{some},
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

func TestLowerCallTypeUsesNativeIndexWithoutLegacyTypeMap(t *testing.T) {
	call := &ast.CallExpr{
		ID: 7,
		Fn: &ast.Ident{Name: "make_string"},
	}
	file := &ast.File{
		Stmts: []ast.Stmt{&ast.LetStmt{
			Pattern: &ast.IdentPat{Name: "s"},
			Value:   call,
		}},
	}
	chk := &check.Result{
		NativeCheckResult: &api.CheckResult{
			TypedNodes: []api.CheckedNode{{
				NodeID: int(call.ID),
				Kind:   "Call",
				Type:   &api.TypeRepr{Kind: "primitive", Name: "String"},
			}},
		},
	}

	mod, issues := Lower("main", file, nil, chk)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}
	let, ok := mod.Script[0].(*LetStmt)
	if !ok {
		t.Fatalf("script[0] = %T, want *LetStmt", mod.Script[0])
	}
	if let.Type != TString {
		t.Fatalf("let type = %#v, want TString", let.Type)
	}
	loweredCall, ok := let.Value.(*CallExpr)
	if !ok {
		t.Fatalf("let value = %T, want *CallExpr", let.Value)
	}
	if loweredCall.T != TString {
		t.Fatalf("call type = %#v, want TString", loweredCall.T)
	}
}

// Real selfhost output anchors instantiation records to the callee
// identifier's NodeID (not the CallExpr's). `instantiationArgs` must
// follow that anchor or generic monomorphization regresses to "arity
// mismatch" for every non-turbofish generic call site.
// Method calls on user-defined structs / enums lose their checker-
// assigned return type post-#1645 (the SemanticDB byID lookup misses
// because AST and selfhost arena ids diverge, and the legacy
// `chk.Types[e]` map is empty). The stdlib intrinsic table covers
// builtin containers but not user methods, so without an AST-side
// fallback `b.capacity()` lowers as `*ir.MethodCall{T:<error>}` and
// poisons every enclosing expression. This test asserts that the
// MethodCall IR node carries the declared return type for both struct
// methods and enum methods.
func TestLowerMethodCallRecoversUserDefinedReturnType(t *testing.T) {
	src := `pub struct Buf {
    capacity: Int,

    pub fn capacity(self) -> Int { self.capacity }
}

pub enum Tag {
    A, B,

    pub fn label(self) -> String { "tag" }
}

fn double(b: Buf) -> Int { b.capacity() + b.capacity() }
fn show(t: Tag) -> String { t.label() }
fn main() {}
`
	file, _ := parser.ParseDiagnostics([]byte(src))
	res := resolve.ResolveFileSourceDefault([]byte(src), file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
		Privileged:    true,
	})
	mod, _ := Lower("main", file, res, chk)

	want := map[string]string{"double": "Int", "show": "String"}
	got := map[string]string{}
	for _, decl := range mod.Decls {
		fn, ok := decl.(*FnDecl)
		if !ok {
			continue
		}
		if _, expect := want[fn.Name]; !expect {
			continue
		}
		if fn.Body == nil {
			continue
		}
		var mc *MethodCall
		switch r := fn.Body.Result.(type) {
		case *MethodCall:
			mc = r
		case *BinaryExpr:
			if leftCall, ok := r.Left.(*MethodCall); ok {
				mc = leftCall
			}
		}
		if mc == nil {
			t.Errorf("fn %s: body result missing MethodCall: %T", fn.Name, fn.Body.Result)
			continue
		}
		got[fn.Name] = typeString(mc.T)
	}
	for name, expect := range want {
		if got[name] != expect {
			t.Errorf("fn %s: MethodCall.T = %q, want %q (user-method return recovery)", name, got[name], expect)
		}
	}
}

// `expressionYieldsValue` falls back to syntactic shape when
// `chk.Types` is empty (post-#1645). The fallback must recognize that
// `BinaryExpr`, `FieldExpr`, `MatchExpr`, etc. always yield values, or
// the trailing expression in `fn f() -> Int { p.x + p.y }` lowers as
// an ExprStmt with no Result and MIR emits `UnreachableTerm` — which
// breaks every stage0 P15-P19 pattern and drops `toolchain/` audit
// coverage by ~1000 functions.
func TestLowerTrailingBinaryExprYieldsValue(t *testing.T) {
	src := `struct Point { x: Int, y: Int }
fn pointSum(p: Point) -> Int { p.x + p.y }
fn main() {}`
	file, _ := parser.ParseDiagnostics([]byte(src))
	res := resolve.ResolveFileSourceDefault([]byte(src), file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
		Privileged:    true,
	})
	mod, _ := Lower("main", file, res, chk)
	var pointSum *FnDecl
	for _, decl := range mod.Decls {
		if fn, ok := decl.(*FnDecl); ok && fn.Name == "pointSum" {
			pointSum = fn
			break
		}
	}
	if pointSum == nil {
		t.Fatal("pointSum not lowered")
	}
	if pointSum.Body == nil || pointSum.Body.Result == nil {
		t.Fatalf("pointSum.Body.Result = nil, want BinaryExpr (trailing expr regression)")
	}
	if _, ok := pointSum.Body.Result.(*BinaryExpr); !ok {
		t.Fatalf("pointSum.Body.Result = %T, want *BinaryExpr", pointSum.Body.Result)
	}
}

func TestLowerTrailingMatchExprYieldsValue(t *testing.T) {
	src := "fn find(text: String, needle: String) -> Bool {\n" +
		"    let idx = text.indexOf(needle)\n" +
		"    match idx {\n" +
		"        Some(i) -> i < 10,\n" +
		"        None -> false,\n" +
		"    }\n" +
		"}\n\nfn main() {}\n"
	file, _ := parser.ParseDiagnostics([]byte(src))
	res := resolve.ResolveFileSourceDefault([]byte(src), file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
		Privileged:    true,
	})
	mod, _ := Lower("main", file, res, chk)
	var find *FnDecl
	for _, decl := range mod.Decls {
		if fn, ok := decl.(*FnDecl); ok && fn.Name == "find" {
			find = fn
			break
		}
	}
	if find == nil {
		t.Fatal("find not lowered")
	}
	if find.Body == nil || find.Body.Result == nil {
		t.Fatalf("find.Body.Result = nil, want *MatchExpr (trailing match regression)")
	}
}

// Real selfhost output anchors CheckedBinding records by byte range,
// with a NodeID that does not match the AST IdentPat's ID (they live in
// separate namespaces). `nativeBindingType` must look up by byte range
// + name when byID misses, or every let binding whose RHS type can only
// be recovered from SemanticDB (method call result, struct field
// access) regresses to `Type=<error>` after #1645 zeroed the legacy
// Result.Types map.
func TestLowerBindingTypeAnchorsOnByteRange(t *testing.T) {
	src := `pub struct Buf {
    items: List<Int>,

    pub fn sum(self) -> Int {
        let mut acc = 0
        acc
    }
}

fn main() {
    let b = Buf { items: [1, 2, 3] }
    let total = b.sum()
    let items = b.items
}
`
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse: %v", parseDiags)
	}
	res := resolve.ResolveFileSourceDefault([]byte(src), file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
		Privileged:    true,
	})
	mod, _ := Lower("main", file, res, chk)

	want := map[string]string{
		"b":     "Buf",
		"total": "Int",
		"items": "List<Int>",
	}
	got := map[string]string{}
	Inspect(mod, func(n Node) bool {
		fn, ok := n.(*FnDecl)
		if !ok || fn.Name != "main" {
			return true
		}
		Inspect(fn.Body, func(n Node) bool {
			if ls, ok := n.(*LetStmt); ok {
				got[ls.Name] = typeString(ls.Type)
			}
			return true
		})
		return true
	})
	for name, expect := range want {
		if got[name] != expect {
			t.Errorf("let %s: Type=%q, want %q (byte-range fallback regression)", name, got[name], expect)
		}
	}
}

func TestLowerInstantiationArgsAnchorsOnCalleeIdentNodeID(t *testing.T) {
	fnIdent := &ast.Ident{ID: 11, Name: "id"}
	call := &ast.CallExpr{
		ID: 13, // CallExpr.ID intentionally different from fnIdent.ID
		Fn: fnIdent,
	}
	file := &ast.File{Stmts: []ast.Stmt{&ast.ExprStmt{X: call}}}
	chk := &check.Result{
		NativeCheckResult: &api.CheckResult{
			Instantiations: []api.CheckInstantiation{{
				NodeID:   int(fnIdent.ID),
				Callee:   "id",
				TypeArgs: []api.TypeRepr{{Kind: "primitive", Name: "Int"}},
			}},
		},
	}

	mod, _ := Lower("main", file, nil, chk)
	stmt := mod.Script[0].(*ExprStmt)
	loweredCall := stmt.X.(*CallExpr)
	if got := loweredCall.TypeArgs; len(got) != 1 || got[0] != TInt {
		t.Fatalf("call type args = %#v, want [TInt] (callee-Ident-anchored lookup)", got)
	}
}

func TestLowerInstantiationArgsUseNativeIndexWithoutLegacyMap(t *testing.T) {
	call := &ast.CallExpr{
		ID: 11,
		Fn: &ast.Ident{Name: "id"},
	}
	file := &ast.File{Stmts: []ast.Stmt{&ast.ExprStmt{X: call}}}
	chk := &check.Result{
		NativeCheckResult: &api.CheckResult{
			Instantiations: []api.CheckInstantiation{{
				NodeID:   int(call.ID),
				Callee:   "id",
				TypeArgs: []api.TypeRepr{{Kind: "primitive", Name: "Int"}},
			}},
		},
	}

	mod, issues := Lower("main", file, nil, chk)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}
	stmt, ok := mod.Script[0].(*ExprStmt)
	if !ok {
		t.Fatalf("script[0] = %T, want *ExprStmt", mod.Script[0])
	}
	loweredCall, ok := stmt.X.(*CallExpr)
	if !ok {
		t.Fatalf("stmt.X = %T, want *CallExpr", stmt.X)
	}
	if got := loweredCall.TypeArgs; len(got) != 1 || got[0] != TInt {
		t.Fatalf("call type args = %#v, want [TInt]", got)
	}
}

func TestLowerNativeDuplicateNodeIDsFallBackToLegacyTypeMap(t *testing.T) {
	call := &ast.CallExpr{
		ID: 13,
		Fn: &ast.Ident{Name: "ambiguous"},
	}
	file := &ast.File{Stmts: []ast.Stmt{&ast.ExprStmt{X: call}}}
	chk := &check.Result{
		Types: map[ast.Expr]types.Type{
			call: types.Bool,
		},
		NativeCheckResult: &api.CheckResult{
			TypedNodes: []api.CheckedNode{
				{NodeID: int(call.ID), Kind: "Call", Type: &api.TypeRepr{Kind: "primitive", Name: "String"}},
				{NodeID: int(call.ID), Kind: "Call", Type: &api.TypeRepr{Kind: "primitive", Name: "Int"}},
			},
		},
	}

	mod, issues := Lower("main", file, nil, chk)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}
	stmt, ok := mod.Script[0].(*ExprStmt)
	if !ok {
		t.Fatalf("script[0] = %T, want *ExprStmt", mod.Script[0])
	}
	loweredCall, ok := stmt.X.(*CallExpr)
	if !ok {
		t.Fatalf("stmt.X = %T, want *CallExpr", stmt.X)
	}
	if loweredCall.T != TBool {
		t.Fatalf("call type = %#v, want TBool from legacy map fallback", loweredCall.T)
	}
}

func TestLowerNativeDuplicateNodeIDsUseSpanDisambiguation(t *testing.T) {
	call := &ast.CallExpr{
		ID:   15,
		PosV: token.Pos{Line: 1, Column: 5, Offset: 4},
		EndV: token.Pos{Line: 1, Column: 14, Offset: 13},
		Fn:   &ast.Ident{Name: "current"},
	}
	file := &ast.File{Stmts: []ast.Stmt{&ast.ExprStmt{X: call}}}
	chk := &check.Result{
		NativeCheckResult: &api.CheckResult{
			TypedNodes: []api.CheckedNode{
				{NodeID: int(call.ID), Kind: "Call", Start: 40, End: 50, Type: &api.TypeRepr{Kind: "primitive", Name: "String"}},
				{NodeID: int(call.ID), Kind: "Call", Start: 4, End: 13, Type: &api.TypeRepr{Kind: "primitive", Name: "Int"}},
			},
		},
	}

	mod, issues := Lower("main", file, nil, chk)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}
	stmt, ok := mod.Script[0].(*ExprStmt)
	if !ok {
		t.Fatalf("script[0] = %T, want *ExprStmt", mod.Script[0])
	}
	loweredCall, ok := stmt.X.(*CallExpr)
	if !ok {
		t.Fatalf("stmt.X = %T, want *CallExpr", stmt.X)
	}
	if loweredCall.T != TInt {
		t.Fatalf("call type = %#v, want TInt from span-disambiguated native record", loweredCall.T)
	}
}

func TestLowerNativeNodeIDSpanMismatchFallsBackToLegacyTypeMap(t *testing.T) {
	call := &ast.CallExpr{
		ID:   17,
		PosV: token.Pos{Line: 1, Column: 11, Offset: 10},
		EndV: token.Pos{Line: 1, Column: 22, Offset: 21},
		Fn:   &ast.Ident{Name: "span_checked"},
	}
	file := &ast.File{Stmts: []ast.Stmt{&ast.ExprStmt{X: call}}}
	chk := &check.Result{
		Types: map[ast.Expr]types.Type{
			call: types.Bool,
		},
		NativeCheckResult: &api.CheckResult{
			TypedNodes: []api.CheckedNode{{
				NodeID: int(call.ID),
				Kind:   "Call",
				Start:  0,
				End:    4,
				Type:   &api.TypeRepr{Kind: "primitive", Name: "String"},
			}},
		},
	}

	mod, issues := Lower("main", file, nil, chk)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}
	stmt, ok := mod.Script[0].(*ExprStmt)
	if !ok {
		t.Fatalf("script[0] = %T, want *ExprStmt", mod.Script[0])
	}
	loweredCall, ok := stmt.X.(*CallExpr)
	if !ok {
		t.Fatalf("stmt.X = %T, want *CallExpr", stmt.X)
	}
	if loweredCall.T != TBool {
		t.Fatalf("call type = %#v, want TBool from legacy map fallback", loweredCall.T)
	}
}

func TestLowerLetStmtUsesNativeBindingTypeWithoutLegacyMap(t *testing.T) {
	pat := &ast.IdentPat{
		ID:   21,
		PosV: token.Pos{Line: 1, Column: 5, Offset: 4},
		EndV: token.Pos{Line: 1, Column: 6, Offset: 5},
		Name: "x",
	}
	file := &ast.File{Stmts: []ast.Stmt{&ast.LetStmt{
		Pattern: pat,
		Value:   &ast.CallExpr{ID: 22, Fn: &ast.Ident{Name: "unknown"}},
	}}}
	chk := &check.Result{
		NativeCheckResult: &api.CheckResult{
			Bindings: []api.CheckedBinding{{
				NodeID: int(pat.ID),
				Name:   "x",
				Start:  4,
				End:    5,
				Type:   &api.TypeRepr{Kind: "primitive", Name: "Int"},
			}},
		},
	}

	mod, issues := Lower("main", file, nil, chk)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}
	let, ok := mod.Script[0].(*LetStmt)
	if !ok {
		t.Fatalf("script[0] = %T, want *LetStmt", mod.Script[0])
	}
	if let.Type != TInt {
		t.Fatalf("let type = %#v, want TInt from native binding", let.Type)
	}
}

func TestLowerLetStmtUsesByteRangeBindingTypeWhenNodeIDsDiverge(t *testing.T) {
	pat := &ast.IdentPat{
		ID:   21,
		PosV: token.Pos{Line: 1, Column: 5, Offset: 4},
		EndV: token.Pos{Line: 1, Column: 6, Offset: 5},
		Name: "x",
	}
	file := &ast.File{Stmts: []ast.Stmt{&ast.LetStmt{
		Pattern: pat,
		Value:   &ast.CallExpr{ID: 22, Fn: &ast.Ident{Name: "unknown"}},
	}}}
	chk := &check.Result{
		NativeCheckResult: &api.CheckResult{
			Bindings: []api.CheckedBinding{
				{
					// NodeID 901 shares x's span but carries a different
					// binding name, so the fallback has to match on both byte
					// range and binding name before accepting a record. This
					// guards the IR switchover against JSON-boundary
					// SemanticDB mismatches that drop the byID hit.
					NodeID: 901,
					Name:   "other",
					Start:  4,
					End:    5,
					Type:   &api.TypeRepr{Kind: "primitive", Name: "String"},
				},
				{
					NodeID: 902,
					Name:   "x",
					Start:  4,
					End:    5,
					Type:   &api.TypeRepr{Kind: "primitive", Name: "Int"},
				},
			},
		},
	}

	mod, issues := Lower("main", file, nil, chk)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}
	let, ok := mod.Script[0].(*LetStmt)
	if !ok {
		t.Fatalf("script[0] = %T, want *LetStmt", mod.Script[0])
	}
	if let.Type != TInt {
		t.Fatalf("let type = %#v, want TInt from byte-range binding fallback", let.Type)
	}
}

func TestLowerIdentUsesNativeSymbolTypeWithoutLegacyMap(t *testing.T) {
	global := &ast.LetDecl{ID: 31, Name: "g"}
	ref := &ast.Ident{ID: 32, Name: "g"}
	file := &ast.File{
		Decls: []ast.Decl{global},
		Stmts: []ast.Stmt{&ast.LetStmt{
			Pattern: &ast.IdentPat{Name: "y"},
			Value:   ref,
		}},
	}
	res := &resolve.Result{
		RefsByID: map[ast.NodeID]*resolve.Symbol{
			ref.ID: {Name: "g", Kind: resolve.SymLet, Decl: global},
		},
		RefIdents: []*ast.Ident{ref},
	}
	chk := &check.Result{
		NativeCheckResult: &api.CheckResult{
			Symbols: []api.CheckedSymbol{{
				NodeID: int(global.ID),
				Kind:   "let",
				Name:   "g",
				Type:   &api.TypeRepr{Kind: "primitive", Name: "String"},
			}},
		},
	}

	mod, issues := Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}
	let, ok := mod.Script[0].(*LetStmt)
	if !ok {
		t.Fatalf("script[0] = %T, want *LetStmt", mod.Script[0])
	}
	if let.Type != TString {
		t.Fatalf("let type = %#v, want TString from native symbol", let.Type)
	}
	id, ok := let.Value.(*Ident)
	if !ok {
		t.Fatalf("let value = %T, want *Ident", let.Value)
	}
	if id.T != TString {
		t.Fatalf("ident type = %#v, want TString from native symbol", id.T)
	}
}

func TestLowerIdentUsesByteRangeSymbolTypeWhenNodeIDsDiverge(t *testing.T) {
	global := &ast.LetDecl{
		ID:   31,
		PosV: token.Pos{Line: 1, Column: 1, Offset: 0},
		EndV: token.Pos{Line: 1, Column: 2, Offset: 1},
		Name: "g",
		Value: &ast.CallExpr{
			ID: 33,
			Fn: &ast.Ident{Name: "unknown"},
		},
	}
	ref := &ast.Ident{ID: 32, Name: "g"}
	file := &ast.File{
		Decls: []ast.Decl{global},
		Stmts: []ast.Stmt{&ast.LetStmt{
			Pattern: &ast.IdentPat{Name: "y"},
			Value:   ref,
		}},
	}
	res := &resolve.Result{
		RefsByID: map[ast.NodeID]*resolve.Symbol{
			ref.ID: {Name: "g", Kind: resolve.SymLet, Decl: global},
		},
		RefIdents: []*ast.Ident{ref},
	}
	chk := &check.Result{
		NativeCheckResult: &api.CheckResult{
			Symbols: []api.CheckedSymbol{
				{
					// NodeID 901 shares g's span but names a different
					// symbol, so lowering must skip it and match NodeID 902's
					// `g` record via byte range + symbol name. This guards the
					// IR switchover against JSON-boundary SemanticDB
					// mismatches when Go and selfhost NodeIDs diverge.
					NodeID: 901,
					Kind:   "let",
					Name:   "other",
					Start:  0,
					End:    1,
					Type:   &api.TypeRepr{Kind: "primitive", Name: "Int"},
				},
				{
					NodeID: 902,
					Kind:   "let",
					Name:   "g",
					Start:  0,
					End:    1,
					Type:   &api.TypeRepr{Kind: "primitive", Name: "String"},
				},
			},
		},
	}

	mod, issues := Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}
	decl, ok := mod.Decls[0].(*LetDecl)
	if !ok {
		t.Fatalf("decls[0] = %T, want *LetDecl", mod.Decls[0])
	}
	if decl.Type != TString {
		t.Fatalf("decl type = %#v, want TString from byte-range symbol fallback", decl.Type)
	}
	let, ok := mod.Script[0].(*LetStmt)
	if !ok {
		t.Fatalf("script[0] = %T, want *LetStmt", mod.Script[0])
	}
	if let.Type != TString {
		t.Fatalf("let type = %#v, want TString from byte-range symbol fallback", let.Type)
	}
	id, ok := let.Value.(*Ident)
	if !ok {
		t.Fatalf("let value = %T, want *Ident", let.Value)
	}
	if id.T != TString {
		t.Fatalf("ident type = %#v, want TString from byte-range symbol fallback", id.T)
	}
}

func TestLowerBareVariantIdentRecoversEnumType(t *testing.T) {
	variant := &ast.Variant{Name: "HirSwitchUnknown"}
	enum := &ast.EnumDecl{
		Name:     "HirSwitchKind",
		Variants: []*ast.Variant{variant},
	}
	ref := &ast.Ident{ID: 1, Name: "HirSwitchUnknown"}
	file := &ast.File{
		Decls: []ast.Decl{enum},
		Stmts: []ast.Stmt{&ast.LetStmt{
			Pattern: &ast.IdentPat{Name: "kind"},
			Value:   ref,
		}},
	}
	res := &resolve.Result{
		RefsByID: map[ast.NodeID]*resolve.Symbol{
			ref.ID: {Name: "HirSwitchUnknown", Kind: resolve.SymVariant, Decl: variant},
		},
		RefIdents: []*ast.Ident{ref},
	}

	mod, issues := Lower("main", file, res, nil)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}
	let, ok := mod.Script[0].(*LetStmt)
	if !ok {
		t.Fatalf("script[0] = %T, want *LetStmt", mod.Script[0])
	}
	named, ok := let.Type.(*NamedType)
	if !ok || named.Name != "HirSwitchKind" {
		t.Fatalf("let type = %#v, want HirSwitchKind", let.Type)
	}
	id, ok := let.Value.(*Ident)
	if !ok {
		t.Fatalf("let value = %T, want *Ident", let.Value)
	}
	named, ok = id.T.(*NamedType)
	if !ok || named.Name != "HirSwitchKind" {
		t.Fatalf("ident type = %#v, want HirSwitchKind", id.T)
	}
}

func TestLowerIfExprRecoversSyntacticBranchType(t *testing.T) {
	pointLit := func() *ast.StructLit {
		return &ast.StructLit{Type: &ast.Ident{Name: "Point"}}
	}
	ifExpr := &ast.IfExpr{
		Cond: &ast.BoolLit{Value: true},
		Then: &ast.Block{Stmts: []ast.Stmt{
			&ast.ExprStmt{X: pointLit()},
		}},
		Else: &ast.Block{Stmts: []ast.Stmt{
			&ast.ExprStmt{X: pointLit()},
		}},
	}
	file := &ast.File{
		Decls: []ast.Decl{&ast.StructDecl{Name: "Point"}},
		Stmts: []ast.Stmt{&ast.LetStmt{
			Pattern: &ast.IdentPat{Name: "p"},
			Value:   ifExpr,
		}},
	}

	mod, issues := Lower("main", file, nil, nil)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}
	let, ok := mod.Script[0].(*LetStmt)
	if !ok {
		t.Fatalf("script[0] = %T, want *LetStmt", mod.Script[0])
	}
	named, ok := let.Type.(*NamedType)
	if !ok || named.Name != "Point" {
		t.Fatalf("let type = %#v, want Point", let.Type)
	}
}

// A trailing bare ident (`xs`, `x`) at the end of a block must be
// promoted to the block's `Result` so the function's return path is
// wired up. Pre-fix, `astIdentLooksLikeValueConstructor` only matched
// uppercase constructor-style idents (`Some`, `Color`), and the
// type-driven branch missed for inferred-type bindings (`let mut xs =
// []` → `xs.Type()` is `<error>` post-#1645). Net effect:
//
//	fn build() -> List<Int> {
//	    let mut xs = []
//	    xs.push(1)
//	    xs              // <- dropped to ExprStmt, MIR UnreachableTerm
//	}
//
// The fix consults the resolver: trailing idents that resolve to a
// `SymLet` or `SymParam` are always values, regardless of whether
// their inferred type made it into the per-node Types map.
func TestLowerTrailingBareIdentYieldsValue(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			"inferred_list",
			`fn build() -> List<Int> {
    let mut xs = []
    xs.push(1)
    xs
}
fn main() {}`,
		},
		{
			"param_passthrough",
			`fn id(x: Int) -> Int { x }
fn main() {}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, _ := parser.ParseDiagnostics([]byte(tt.src))
			res := resolve.ResolveFileSourceDefault([]byte(tt.src), file, stdlib.LoadCached())
			reg := stdlib.LoadCached()
			chk := check.SelfhostFile(file, res, check.Opts{
				Stdlib:        reg,
				Primitives:    reg.Primitives,
				ResultMethods: reg.ResultMethods,
				Source:        []byte(tt.src),
				Privileged:    true,
			})
			mod, _ := Lower("main", file, res, chk)
			for _, decl := range mod.Decls {
				fn, ok := decl.(*FnDecl)
				if !ok || fn.Name == "main" {
					continue
				}
				if fn.Body == nil || fn.Body.Result == nil {
					t.Errorf("fn %s: body.Result = nil (trailing bare-ident regression)", fn.Name)
					continue
				}
				if _, ok := fn.Body.Result.(*Ident); !ok {
					t.Errorf("fn %s: body.Result = %T, want *Ident", fn.Name, fn.Body.Result)
				}
			}
		})
	}
}

// `self` inside a struct method body must resolve to the owner
// struct's nominal type. The resolver records the `self` ident with
// `Decl = *ast.Receiver`, which carries no type info on its own. The
// lowerer now matches the receiver against each StructDecl/EnumDecl's
// Methods to find the owner, then returns its NamedType. Without
// this, every `self` reference inside a method body lowers as
// `Ident.T = <error>` and cascades through `self.field`, `self.method()`,
// `self.method().unwrap()` etc.
func TestLowerSelfReceiverType(t *testing.T) {
	src := `pub struct Buf {
    capacity: Int,
    pub fn use_(self) -> Int { self.capacity }
    pub fn dbl(self) -> Int { self.capacity * 2 }
}
fn main() {}`
	file, _ := parser.ParseDiagnostics([]byte(src))
	res := resolve.ResolveFileSourceDefault([]byte(src), file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
		Privileged:    true,
	})
	mod, _ := Lower("main", file, res, chk)
	for _, decl := range mod.Decls {
		sd, ok := decl.(*StructDecl)
		if !ok {
			continue
		}
		for _, m := range sd.Methods {
			if m.Body == nil || m.Body.Result == nil {
				t.Errorf("method %s: body.Result missing (self regression)", m.Name)
				continue
			}
			if got := typeString(m.Body.Result.Type()); got != "Int" {
				t.Errorf("method %s: body.Result.Type = %q, want Int", m.Name, got)
			}
		}
	}
}

// Bundle 6: small incremental fixes after #1693. Two additions:
//
//  1. `lowerIdent` consults `resolveExprStaticType` as a final
//     fallback, catching idents whose binding type only resolves via
//     AST-side helpers (e.g. let bindings whose initialiser is a
//     chained method call).
//
//  2. `lowerMethodCall` now retries `useAliasFnReturnTypeFromAST`
//     when the existing recovery chain fails, so `strings.ToUpper(s)`
//     inside `use go "strings" as strings { fn ToUpper(s: String)
//     -> String }` lowers with `MethodCall.T = String` instead of
//     `<error>`.
//
// Both changes are conservative — they only fire when the existing
// recovery path returns `<error>`, so they never overwrite a checker-
// supplied type.
func TestLowerIncrementalRecovery(t *testing.T) {
	tests := []struct {
		name, src, fnName, wantType string
	}{
		{
			"use_alias_method_call_type",
			`use go "strings" as strings {
    fn ToUpper(s: String) -> String
}
fn make(s: String) -> String {
    let upper = strings.ToUpper(s)
    upper
}
fn main() {}`,
			"make", "String",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, _ := parser.ParseDiagnostics([]byte(tt.src))
			res := resolve.ResolveFileSourceDefault([]byte(tt.src), file, stdlib.LoadCached())
			reg := stdlib.LoadCached()
			chk := check.SelfhostFile(file, res, check.Opts{
				Stdlib:        reg,
				Primitives:    reg.Primitives,
				ResultMethods: reg.ResultMethods,
				Source:        []byte(tt.src),
				Privileged:    true,
			})
			mod, _ := Lower("main", file, res, chk)
			for _, decl := range mod.Decls {
				fn, ok := decl.(*FnDecl)
				if !ok || fn.Name != tt.fnName {
					continue
				}
				if fn.Body == nil || fn.Body.Result == nil {
					t.Errorf("fn %s: body.Result missing", tt.fnName)
					continue
				}
				if got := typeString(fn.Body.Result.Type()); got != tt.wantType {
					t.Errorf("fn %s: body.Result.Type = %q, want %q", tt.fnName, got, tt.wantType)
				}
			}
		})
	}
}

// Bundle 5: real-world regressions found by auditing examples/ for
// IR `<error>` types. Three independent cases:
//
//  1. **Unary minus** (`if n < 0 { -n } else { n }` in calc/lib.osty):
//     `UnaryExpr.T` was `<error>` post-#1645 because the checker
//     didn't populate per-node types. The lowerer now falls back to
//     the operand's type for negation / bit-not / plus, and TBool for
//     logical not.
//
//  2. **Chained method calls** (`self.tryAllocate(k, n).unwrap()` in
//     examples/gc/lib.osty): the outer `.unwrap()` needs the inner
//     call's return type to look up `unwrap`. `resolveExprStaticType`
//     now handles `*ast.CallExpr` and `*ast.FieldExpr` receivers,
//     delegating to userMethod / builtinMethod /
//     closureDependentMethod / useAliasFn return-type recovery.
//
//  3. **`use go` FFI alias calls** (`strings.ToUpper(...)` in
//     examples/ffi/main.osty): trailing FFI call lost promotion
//     because `userMethodReturnTypeFromAST` filtered out builtin
//     types and never looked at use-decl bodies. New
//     `useAliasFnReturnTypeFromAST` helper bridges that gap.
//
// Validation tied to `OSTY_STAGE0_AUDIT=1 TestStage0ToolchainAudit`:
// 7363 → 7411 covered (+48 toolchain helpers newly pass through
// stage0 because their nested-method-chain shapes promote correctly).
func TestLowerRealWorldRegressions(t *testing.T) {
	tests := []struct {
		name, src, fnName, wantType string
	}{
		{
			"unary_neg_in_if",
			`fn abs(n: Int) -> Int { if n < 0 { -n } else { n } } fn main() {}`,
			"abs", "Int",
		},
		{
			"chained_builtin_unwrap",
			`fn at(xs: List<Int>) -> Int { xs.first().unwrap() }
fn main() {}`,
			"at", "Int",
		},
		{
			"use_go_ffi_call",
			`use go "strings" as strings {
    fn ToUpper(s: String) -> String
}
fn banner(name: String) -> String { strings.ToUpper(name) }
fn main() {}`,
			"banner", "String",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, _ := parser.ParseDiagnostics([]byte(tt.src))
			res := resolve.ResolveFileSourceDefault([]byte(tt.src), file, stdlib.LoadCached())
			reg := stdlib.LoadCached()
			chk := check.SelfhostFile(file, res, check.Opts{
				Stdlib:        reg,
				Primitives:    reg.Primitives,
				ResultMethods: reg.ResultMethods,
				Source:        []byte(tt.src),
				Privileged:    true,
			})
			mod, _ := Lower("main", file, res, chk)
			var target *FnDecl
			for _, decl := range mod.Decls {
				if fn, ok := decl.(*FnDecl); ok && fn.Name == tt.fnName {
					target = fn
					break
				}
			}
			if target == nil {
				// Method may be inside a struct; walk decls.
				for _, decl := range mod.Decls {
					if sd, ok := decl.(*StructDecl); ok {
						for _, m := range sd.Methods {
							if m != nil && m.Name == tt.fnName {
								target = m
								break
							}
						}
					}
					if target != nil {
						break
					}
				}
			}
			if target == nil || target.Body == nil || target.Body.Result == nil {
				t.Fatalf("fn %s: body.Result missing", tt.fnName)
			}
			if got := typeString(target.Body.Result.Type()); got != tt.wantType {
				t.Errorf("fn %s: body.Result.Type = %q, want %q", tt.fnName, got, tt.wantType)
			}
		})
	}
}

// Bundle 4: 40+ more trailing-call shapes — Math/Float (log, sin,
// exp, etc.), Int bit ops (gcd, sign, countOnes, leadingZeros),
// List<T> functional helpers (reduce, flatten, distinct, partition),
// String byte/char access (byteAt, charAt, splitFirst, byteSize),
// Option/Result extras (unwrapOrElse, or, and), Map.getOr,
// Bytes.toString/toHex/concat.
func TestLowerTrailingMethodCallBundle4(t *testing.T) {
	tests := []struct {
		name, src, wantType string
	}{
		{"math_log", `fn lg(x: Float) -> Float { x.log() } fn main() {}`, "Float"},
		{"math_sin", `fn s(x: Float) -> Float { x.sin() } fn main() {}`, "Float"},
		{"math_exp", `fn e(x: Float) -> Float { x.exp() } fn main() {}`, "Float"},
		{"int_gcd", `fn g(a: Int, b: Int) -> Int { a.gcd(b) } fn main() {}`, "Int"},
		{"int_sign", `fn sg(n: Int) -> Int { n.sign() } fn main() {}`, "Int"},
		{"int_countOnes", `fn co(n: Int) -> Int { n.countOnes() } fn main() {}`, "Int"},
		{"int_leadingZeros", `fn lz(n: Int) -> Int { n.leadingZeros() } fn main() {}`, "Int"},
		{"list_reduce", `fn r(xs: List<Int>) -> Int? { xs.reduce(|a, b| a + b) } fn main() {}`, "Int?"},
		{"list_flatten", `fn f(xs: List<List<Int>>) -> List<Int> { xs.flatten() } fn main() {}`, "List<Int>"},
		{"list_distinct", `fn d(xs: List<Int>) -> List<Int> { xs.distinct() } fn main() {}`, "List<Int>"},
		{"list_partition", `fn p(xs: List<Int>) -> (List<Int>, List<Int>) { xs.partition(|x| x > 0) } fn main() {}`, "(List<Int>, List<Int>)"},
		{"string_splitFirst", `fn sf(s: String, c: Char) -> (String, String)? { s.splitFirst(c) } fn main() {}`, "(String, String)?"},
		{"string_byteAt", `fn b(s: String, i: Int) -> Byte? { s.byteAt(i) } fn main() {}`, "Byte?"},
		{"string_charAt", `fn c(s: String, i: Int) -> Char? { s.charAt(i) } fn main() {}`, "Char?"},
		{"string_byteSize", `fn bs(s: String) -> Int { s.byteSize() } fn main() {}`, "Int"},
		{"option_or", `fn o(a: Int?, b: Int?) -> Int? { a.or(b) } fn main() {}`, "Int?"},
		{"option_and", `fn a_(a: Int?, b: Int?) -> Int? { a.and(b) } fn main() {}`, "Int?"},
		{"option_unwrapOrElse", `fn ue(o: Int?, d: Int) -> Int { o.unwrapOrElse(|| d) } fn main() {}`, "Int"},
		{"result_unwrapOrElse", `fn oe(r: Result<Int, Error>, d: Int) -> Int { r.unwrapOrElse(|_| d) } fn main() {}`, "Int"},
		{"map_getOr", `fn go_(m: Map<String, Int>, k: String, d: Int) -> Int { m.getOr(k, d) } fn main() {}`, "Int"},
		{"bytes_toString", `fn b2s(b: Bytes) -> Result<String, Error> { b.toString() } fn main() {}`, "Result<String, Error>"},
		{"bytes_toHex", `fn h(b: Bytes) -> String { b.toHex() } fn main() {}`, "String"},
		{"bytes_concat", `fn bc(a: Bytes, b: Bytes) -> Bytes { a.concat(b) } fn main() {}`, "Bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, _ := parser.ParseDiagnostics([]byte(tt.src))
			res := resolve.ResolveFileSourceDefault([]byte(tt.src), file, stdlib.LoadCached())
			reg := stdlib.LoadCached()
			chk := check.SelfhostFile(file, res, check.Opts{
				Stdlib:        reg,
				Primitives:    reg.Primitives,
				ResultMethods: reg.ResultMethods,
				Source:        []byte(tt.src),
				Privileged:    true,
			})
			mod, _ := Lower("main", file, res, chk)
			for _, decl := range mod.Decls {
				fn, ok := decl.(*FnDecl)
				if !ok || fn.Name == "main" {
					continue
				}
				if fn.Body == nil || fn.Body.Result == nil {
					t.Errorf("fn %s: body.Result = nil", fn.Name)
					continue
				}
				if got := typeString(fn.Body.Result.Type()); got != tt.wantType {
					t.Errorf("fn %s: body.Result.Type = %q, want %q", fn.Name, got, tt.wantType)
				}
			}
		})
	}
}

// Bundle 3: 40+ more trailing-call shapes covering Int/Float
// predicates+formatting, String mutation/predicate helpers, List<T>
// mutation operations (pop/remove/indexOf/iter/extend), Iterator<T>
// methods, Channel<T> send/recv, Duration unit conversions, and
// closure-arg-dependent recovery for `mapOr` / `mapErr`.
func TestLowerTrailingMethodCallBundle3(t *testing.T) {
	tests := []struct {
		name, src, wantType string
	}{
		{"int_pow", `fn pw(a: Int, b: Int) -> Int { a.pow(b) } fn main() {}`, "Int"},
		{"int_toFloat", `fn tf(n: Int) -> Float { n.toFloat() } fn main() {}`, "Float"},
		{"int_clamp", `fn cl(n: Int, lo: Int, hi: Int) -> Int { n.clamp(lo, hi) } fn main() {}`, "Int"},
		{"int_isPositive", `fn p(n: Int) -> Bool { n.isPositive() } fn main() {}`, "Bool"},
		{"int_isZero", `fn z(n: Int) -> Bool { n.isZero() } fn main() {}`, "Bool"},
		{"int_toHex", `fn hx(n: Int) -> String { n.toHex() } fn main() {}`, "String"},
		{"float_pow", `fn pw(a: Float, b: Float) -> Float { a.pow(b) } fn main() {}`, "Float"},
		{"float_isNan", `fn n(f: Float) -> Bool { f.isNan() } fn main() {}`, "Bool"},
		{"float_isFinite", `fn fi(f: Float) -> Bool { f.isFinite() } fn main() {}`, "Bool"},
		{"string_concat", `fn cc(a: String, b: String) -> String { a.concat(b) } fn main() {}`, "String"},
		{"string_repeat", `fn rp(s: String, n: Int) -> String { s.repeat(n) } fn main() {}`, "String"},
		{"string_reverse", `fn rv(s: String) -> String { s.reverse() } fn main() {}`, "String"},
		{"string_count", `fn ct(s: String, p: String) -> Int { s.count(p) } fn main() {}`, "Int"},
		{"string_replaceAll", `fn rp(s: String, o: String, n: String) -> String { s.replaceAll(o, n) } fn main() {}`, "String"},
		{"string_first", `fn fi(s: String) -> Char? { s.first() } fn main() {}`, "Char?"},
		{"string_stripPrefix", `fn sp(s: String, p: String) -> String? { s.stripPrefix(p) } fn main() {}`, "String?"},
		{"string_toUpperCase", `fn uc(s: String) -> String { s.toUpperCase() } fn main() {}`, "String"},
		{"list_pop", `fn pp(mut xs: List<Int>) -> Int? { xs.pop() } fn main() {}`, "Int?"},
		{"list_remove", `fn rm(mut xs: List<Int>, i: Int) -> Int { xs.remove(i) } fn main() {}`, "Int"},
		{"list_indexOf", `fn ix(xs: List<Int>, n: Int) -> Int? { xs.indexOf(n) } fn main() {}`, "Int?"},
		{"list_iter", `fn it(xs: List<Int>) -> Iterator<Int> { xs.iter() } fn main() {}`, "Iterator<Int>"},
		{"iter_collect", `fn cl(it: Iterator<Int>) -> List<Int> { it.collect() } fn main() {}`, "List<Int>"},
		{"chan_recv", `fn rc(ch: Channel<Int>) -> Int? { ch.recv() } fn main() {}`, "Int?"},
		{"duration_toMillis", `fn dm(d: Duration) -> Int { d.toMillis() } fn main() {}`, "Int"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, _ := parser.ParseDiagnostics([]byte(tt.src))
			res := resolve.ResolveFileSourceDefault([]byte(tt.src), file, stdlib.LoadCached())
			reg := stdlib.LoadCached()
			chk := check.SelfhostFile(file, res, check.Opts{
				Stdlib:        reg,
				Primitives:    reg.Primitives,
				ResultMethods: reg.ResultMethods,
				Source:        []byte(tt.src),
				Privileged:    true,
			})
			mod, _ := Lower("main", file, res, chk)
			for _, decl := range mod.Decls {
				fn, ok := decl.(*FnDecl)
				if !ok || fn.Name == "main" {
					continue
				}
				if fn.Body == nil || fn.Body.Result == nil {
					t.Errorf("fn %s: body.Result = nil", fn.Name)
					continue
				}
				if got := typeString(fn.Body.Result.Type()); got != tt.wantType {
					t.Errorf("fn %s: body.Result.Type = %q, want %q", fn.Name, got, tt.wantType)
				}
			}
		})
	}
}

// Bundle 2: 40+ more trailing-call shapes covering Int/Float/Char/Bool
// primitive methods, Option/Result accessors, Map/Set predicates,
// List<T> functional helpers (slice, take, drop, concat, append, zip,
// min, max, sum, count, any, all), Range methods, and String extras
// (padLeft, padRight, lines, fields, indexOf).
func TestLowerTrailingMethodCallBundle2(t *testing.T) {
	tests := []struct {
		name, src, wantType string
	}{
		{"int_abs", `fn ab(n: Int) -> Int { n.abs() }
fn main() {}`, "Int"},
		{"int_max", `fn mx(a: Int, b: Int) -> Int { a.max(b) }
fn main() {}`, "Int"},
		{"float_sqrt", `fn sq(f: Float) -> Float { f.sqrt() }
fn main() {}`, "Float"},
		{"float_toInt", `fn cnv(f: Float) -> Int { f.toInt() }
fn main() {}`, "Int"},
		{"list_take", `fn t(xs: List<Int>) -> List<Int> { xs.take(3) }
fn main() {}`, "List<Int>"},
		{"list_concat", `fn cc(xs: List<Int>, ys: List<Int>) -> List<Int> { xs.concat(ys) }
fn main() {}`, "List<Int>"},
		{"list_min", `fn mn(xs: List<Int>) -> Int? { xs.min() }
fn main() {}`, "Int?"},
		{"list_sum", `fn sm(xs: List<Int>) -> Int { xs.sum() }
fn main() {}`, "Int"},
		{"map_isEmpty", `fn em(m: Map<String, Int>) -> Bool { m.isEmpty() }
fn main() {}`, "Bool"},
		{"map_len", `fn le(m: Map<String, Int>) -> Int { m.len() }
fn main() {}`, "Int"},
		{"set_len", `fn le(s: Set<Int>) -> Int { s.len() }
fn main() {}`, "Int"},
		{"option_orElse", `fn oe(a: Int?, b: Int?) -> Int? { a.orElse(b) }
fn main() {}`, "Int?"},
		{"option_filter", `fn ft(o: Int?) -> Int? { o.filter(|n| n > 0) }
fn main() {}`, "Int?"},
		{"option_isSome", `fn has(o: Int?) -> Bool { o.isSome() }
fn main() {}`, "Bool"},
		{"option_unwrapOr", `fn pick(o: Int?, d: Int) -> Int { o.unwrapOr(d) }
fn main() {}`, "Int"},
		{"result_isOk", `fn ok(r: Result<Int, Error>) -> Bool { r.isOk() }
fn main() {}`, "Bool"},
		{"result_unwrapOr", `fn pick(r: Result<Int, Error>, d: Int) -> Int { r.unwrapOr(d) }
fn main() {}`, "Int"},
		{"closure_all", `fn allpos(xs: List<Int>) -> Bool { xs.all(|x| x > 0) }
fn main() {}`, "Bool"},
		{"closure_any", `fn anyneg(xs: List<Int>) -> Bool { xs.any(|x| x < 0) }
fn main() {}`, "Bool"},
		{"char_isDigit", `fn dg(c: Char) -> Bool { c.isDigit() }
fn main() {}`, "Bool"},
		{"char_isAlpha", `fn al(c: Char) -> Bool { c.isAlpha() }
fn main() {}`, "Bool"},
		{"char_isWhitespace", `fn sp(c: Char) -> Bool { c.isWhitespace() }
fn main() {}`, "Bool"},
		{"string_padLeft", `fn pad(s: String, n: Int) -> String { s.padLeft(n, ' ') }
fn main() {}`, "String"},
		{"string_indexOf", `fn idx(s: String, n: String) -> Int? { s.indexOf(n) }
fn main() {}`, "Int?"},
		{"string_lines", `fn ln(s: String) -> List<String> { s.lines() }
fn main() {}`, "List<String>"},
		{"tuple_in_let", `fn split(s: String) -> (String, String) { (s, s) }
fn main() {}`, "(String, String)"},
		{"closure_returning_struct", `pub struct P { pub n: Int }
fn make() -> P {
    let f = || P { n: 1 }
    f()
}
fn main() {}`, "P"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, _ := parser.ParseDiagnostics([]byte(tt.src))
			res := resolve.ResolveFileSourceDefault([]byte(tt.src), file, stdlib.LoadCached())
			reg := stdlib.LoadCached()
			chk := check.SelfhostFile(file, res, check.Opts{
				Stdlib:        reg,
				Primitives:    reg.Primitives,
				ResultMethods: reg.ResultMethods,
				Source:        []byte(tt.src),
				Privileged:    true,
			})
			mod, _ := Lower("main", file, res, chk)
			for _, decl := range mod.Decls {
				fn, ok := decl.(*FnDecl)
				if !ok || fn.Name == "main" {
					continue
				}
				if fn.Body == nil || fn.Body.Result == nil {
					t.Errorf("fn %s: body.Result = nil (trailing call regression)", fn.Name)
					continue
				}
				if tt.wantType != "" {
					if got := typeString(fn.Body.Result.Type()); got != tt.wantType {
						t.Errorf("fn %s: body.Result.Type = %q, want %q", fn.Name, got, tt.wantType)
					}
				}
			}
		})
	}
}

// Bundle: ten trailing-call shapes that the post-#1645 `expression-
// YieldsValue` path dropped to ExprStmt → MIR UnreachableTerm. Each
// case is a real user-code pattern that the stdlib intrinsic table
// previously didn't cover, plus the `f(x)` fn-typed-parameter call.
func TestLowerTrailingMethodCallBundle(t *testing.T) {
	tests := []struct {
		name, src, wantType string
	}{
		{"list_fold_promote", `fn total(xs: List<Int>) -> Int { xs.fold(0, |a, x| a + x) }
fn main() {}`, ""},
		{"map_entries", `fn ents(m: Map<String, Int>) -> List<(String, Int)> { m.entries() }
fn main() {}`, "List<(String, Int)>"},
		{"set_to_list", `fn arr(s: Set<Int>) -> List<Int> { s.toList() }
fn main() {}`, "List<Int>"},
		{"set_contains", `fn has(s: Set<Int>, n: Int) -> Bool { s.contains(n) }
fn main() {}`, "Bool"},
		{"string_chars", `fn ch(s: String) -> List<Char> { s.chars() }
fn main() {}`, "List<Char>"},
		{"string_bytes", `fn bs(s: String) -> List<Byte> { s.bytes() }
fn main() {}`, "List<Byte>"},
		{"char_to_int", `fn ord(c: Char) -> Int { c.toInt() }
fn main() {}`, "Int"},
		{"byte_to_int", `fn b2i(b: Byte) -> Int { b.toInt() }
fn main() {}`, "Int"},
		{"closure_param_call", `fn apply(f: fn(Int) -> Int, x: Int) -> Int { f(x) }
fn main() {}`, "Int"},
		{"list_entries", `fn ents(xs: List<Int>) -> List<(Int, Int)> { xs.entries() }
fn main() {}`, "List<(Int, Int)>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, _ := parser.ParseDiagnostics([]byte(tt.src))
			res := resolve.ResolveFileSourceDefault([]byte(tt.src), file, stdlib.LoadCached())
			reg := stdlib.LoadCached()
			chk := check.SelfhostFile(file, res, check.Opts{
				Stdlib:        reg,
				Primitives:    reg.Primitives,
				ResultMethods: reg.ResultMethods,
				Source:        []byte(tt.src),
				Privileged:    true,
			})
			mod, _ := Lower("main", file, res, chk)
			for _, decl := range mod.Decls {
				fn, ok := decl.(*FnDecl)
				if !ok || fn.Name == "main" {
					continue
				}
				if fn.Body == nil || fn.Body.Result == nil {
					t.Errorf("fn %s: body.Result = nil (trailing call promotion regression)", fn.Name)
					continue
				}
				if tt.wantType != "" {
					if got := typeString(fn.Body.Result.Type()); got != tt.wantType {
						t.Errorf("fn %s: body.Result.Type = %q, want %q", fn.Name, got, tt.wantType)
					}
				}
			}
		})
	}
}

// `if let Some(x) = opt { x } else { default }` is a value expression
// just like a regular `if ... else`. The pre-fix
// `astIfLooksLikeValueExpr` short-circuited on `IsIfLet → false`,
// dropping the trailing if-let expression to ExprStmt → MIR
// UnreachableTerm. Also covers the case where a then-branch's tail
// is a bare ident reference (`{ x }`) which previously failed
// `astBlockTailLooksLikeValueConstructor`'s syntactic switch.
func TestLowerTrailingIfLetYieldsValue(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			"ident_tail",
			`fn unwrap(o: Int?) -> Int {
    if let Some(x) = o { x } else { -1 }
}
fn main() {}`,
		},
		{
			"expr_tail",
			`fn plusOne(o: Int?) -> Int {
    if let Some(x) = o { x + 1 } else { 0 }
}
fn main() {}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, _ := parser.ParseDiagnostics([]byte(tt.src))
			res := resolve.ResolveFileSourceDefault([]byte(tt.src), file, stdlib.LoadCached())
			reg := stdlib.LoadCached()
			chk := check.SelfhostFile(file, res, check.Opts{
				Stdlib:        reg,
				Primitives:    reg.Primitives,
				ResultMethods: reg.ResultMethods,
				Source:        []byte(tt.src),
				Privileged:    true,
			})
			mod, _ := Lower("main", file, res, chk)
			for _, decl := range mod.Decls {
				fn, ok := decl.(*FnDecl)
				if !ok || fn.Name == "main" {
					continue
				}
				if fn.Body == nil || fn.Body.Result == nil {
					t.Errorf("fn %s: body.Result = nil (trailing if-let regression)", fn.Name)
				}
			}
		})
	}
}

// Trailing stdlib container-method calls (`xs.filter(...)`,
// `xs.sorted()`) must promote to the block's `Result` so the function
// returns the new list. The previous CallExpr branch in
// `expressionYieldsValue` only consulted `userMethodReturnTypeFromAST`
// which bails out for builtin containers (List/Map/Set/String/Bytes).
// Without a stdlib-intrinsic fallback, a method chain like
//
//	fn evens(xs: List<Int>) -> List<Int> { xs.filter(|x| x % 2 == 0) }
//
// drops the trailing call to ExprStmt → MIR UnreachableTerm → stage0
// declined.
func TestLowerTrailingStdlibMethodCallYieldsValue(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			"list_filter",
			`fn evens(xs: List<Int>) -> List<Int> {
    xs.filter(|x| x % 2 == 0)
}
fn main() {}`,
		},
		{
			"list_sorted",
			`fn ord(xs: List<Int>) -> List<Int> { xs.sorted() }
fn main() {}`,
		},
		{
			"list_contains",
			`fn has(xs: List<Int>, n: Int) -> Bool { xs.contains(n) }
fn main() {}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, _ := parser.ParseDiagnostics([]byte(tt.src))
			res := resolve.ResolveFileSourceDefault([]byte(tt.src), file, stdlib.LoadCached())
			reg := stdlib.LoadCached()
			chk := check.SelfhostFile(file, res, check.Opts{
				Stdlib:        reg,
				Primitives:    reg.Primitives,
				ResultMethods: reg.ResultMethods,
				Source:        []byte(tt.src),
				Privileged:    true,
			})
			mod, _ := Lower("main", file, res, chk)
			for _, decl := range mod.Decls {
				fn, ok := decl.(*FnDecl)
				if !ok || fn.Name == "main" {
					continue
				}
				if fn.Body == nil || fn.Body.Result == nil {
					t.Errorf("fn %s: body.Result = nil (trailing stdlib-method regression)", fn.Name)
				}
			}
		})
	}
}

// Tuple field access (`t.0`, `t.1`) lowers via FieldExpr with a
// numeric name. Post-#1645 the checker no longer fills `chk.Types[e]`
// and the SemanticDB byID/byKey lookups miss for FieldExpr-on-tuple,
// so `TupleAccess.T` regressed to `<error>` — poisoning any enclosing
// `BinaryExpr` (`t.0 + t.1`). The lowerer now derives the element
// type from the receiver's TupleType when the checker is silent.
func TestLowerTupleAccessRecoversElementTypeFromReceiver(t *testing.T) {
	src := `fn sumPair(t: (Int, Int)) -> Int { t.0 + t.1 }
fn main() {}`
	file, _ := parser.ParseDiagnostics([]byte(src))
	res := resolve.ResolveFileSourceDefault([]byte(src), file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
		Privileged:    true,
	})
	mod, _ := Lower("main", file, res, chk)
	var sumPair *FnDecl
	for _, decl := range mod.Decls {
		if fn, ok := decl.(*FnDecl); ok && fn.Name == "sumPair" {
			sumPair = fn
			break
		}
	}
	if sumPair == nil || sumPair.Body == nil || sumPair.Body.Result == nil {
		t.Fatal("sumPair body result missing")
	}
	bx, ok := sumPair.Body.Result.(*BinaryExpr)
	if !ok {
		t.Fatalf("body result = %T, want *BinaryExpr", sumPair.Body.Result)
	}
	if got := typeString(bx.T); got != "Int" {
		t.Errorf("BinaryExpr.T = %q, want Int (tuple-access type-recovery regression)", got)
	}
	left, ok := bx.Left.(*TupleAccess)
	if !ok {
		t.Fatalf("Left = %T, want *TupleAccess", bx.Left)
	}
	if got := typeString(left.T); got != "Int" {
		t.Errorf("TupleAccess.T = %q, want Int", got)
	}
}

// Trailing free-fn call (`helper()`) with a non-unit return type must
// promote to the block's Result. `expressionYieldsValue` previously
// only handled constructor-shaped calls (uppercase Ident callee),
// dropping `fn main() -> Int { helper() }` to an ExprStmt and emitting
// MIR UnreachableTerm.
func TestLowerTrailingFreeFnCallYieldsValue(t *testing.T) {
	src := `fn helper() -> Int { 42 }
fn caller() -> Int { helper() }
fn main() {}`
	file, _ := parser.ParseDiagnostics([]byte(src))
	res := resolve.ResolveFileSourceDefault([]byte(src), file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
		Privileged:    true,
	})
	mod, _ := Lower("main", file, res, chk)
	var caller *FnDecl
	for _, decl := range mod.Decls {
		if fn, ok := decl.(*FnDecl); ok && fn.Name == "caller" {
			caller = fn
			break
		}
	}
	if caller == nil || caller.Body == nil || caller.Body.Result == nil {
		t.Fatal("caller body result missing (trailing free-fn call regression)")
	}
	if _, ok := caller.Body.Result.(*CallExpr); !ok {
		t.Fatalf("body result = %T, want *CallExpr", caller.Body.Result)
	}
}

func TestLowerFieldExprRecoversTypeFromRecordedBinding(t *testing.T) {
	binding := &ast.IdentPat{Name: "p"}
	pRef := &ast.Ident{ID: 1, Name: "p"}
	file := &ast.File{
		Decls: []ast.Decl{&ast.StructDecl{
			Name: "Point",
			Fields: []*ast.Field{{
				Name: "x",
				Type: &ast.NamedType{Path: []string{"Int"}},
			}},
		}},
		Stmts: []ast.Stmt{
			&ast.LetStmt{
				Pattern: binding,
				Value: &ast.StructLit{
					Type: &ast.Ident{Name: "Point"},
					Fields: []*ast.StructLitField{{
						Name:  "x",
						Value: &ast.IntLit{Text: "1"},
					}},
				},
			},
			&ast.LetStmt{
				Pattern: &ast.IdentPat{Name: "x"},
				Value:   &ast.FieldExpr{X: pRef, Name: "x"},
			},
		},
	}
	res := &resolve.Result{
		RefsByID: map[ast.NodeID]*resolve.Symbol{
			pRef.ID: {Name: "p", Kind: resolve.SymLet, Decl: binding},
		},
		RefIdents: []*ast.Ident{pRef},
	}

	mod, issues := Lower("main", file, res, nil)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}
	let, ok := mod.Script[1].(*LetStmt)
	if !ok {
		t.Fatalf("script[1] = %T, want *LetStmt", mod.Script[1])
	}
	if let.Type != TInt {
		t.Fatalf("field let type = %#v, want TInt", let.Type)
	}
	field, ok := let.Value.(*FieldExpr)
	if !ok {
		t.Fatalf("field let value = %T, want *FieldExpr", let.Value)
	}
	if field.T != TInt {
		t.Fatalf("field expr type = %#v, want TInt", field.T)
	}
}

func TestLowerIndexExprRecoversTypeFromRecordedBinding(t *testing.T) {
	binding := &ast.IdentPat{Name: "xs"}
	xsRef := &ast.Ident{ID: 1, Name: "xs"}
	file := &ast.File{
		Stmts: []ast.Stmt{
			&ast.LetStmt{
				Pattern: binding,
				Type: &ast.NamedType{
					Path: []string{"List"},
					Args: []ast.Type{&ast.NamedType{Path: []string{"Int"}}},
				},
				Value: &ast.ListExpr{Elems: []ast.Expr{&ast.IntLit{Text: "1"}}},
			},
			&ast.LetStmt{
				Pattern: &ast.IdentPat{Name: "first"},
				Value: &ast.IndexExpr{
					X:     xsRef,
					Index: &ast.IntLit{Text: "0"},
				},
			},
		},
	}
	res := &resolve.Result{
		RefsByID: map[ast.NodeID]*resolve.Symbol{
			xsRef.ID: {Name: "xs", Kind: resolve.SymLet, Decl: binding},
		},
		RefIdents: []*ast.Ident{xsRef},
	}

	mod, issues := Lower("main", file, res, nil)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}
	let, ok := mod.Script[1].(*LetStmt)
	if !ok {
		t.Fatalf("script[1] = %T, want *LetStmt", mod.Script[1])
	}
	if let.Type != TInt {
		t.Fatalf("index let type = %#v, want TInt", let.Type)
	}
	idx, ok := let.Value.(*IndexExpr)
	if !ok {
		t.Fatalf("index let value = %T, want *IndexExpr", let.Value)
	}
	if idx.T != TInt {
		t.Fatalf("index expr type = %#v, want TInt", idx.T)
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

func TestLowerMethodCallRecoversDowncastOptionalType(t *testing.T) {
	src := `interface Printable {
    fn show(self) -> String
}

struct Note {
    pub msg: String,

    pub fn show(self) -> String {
        self.msg
    }
}

fn probe(p: Printable) -> Note? {
    return p.downcast::<Note>()
}
`
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse: %v", parseDiags)
	}
	res := resolve.ResolveFileSourceDefault([]byte(src), file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{

		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
	})

	probe, ok := file.Decls[len(file.Decls)-1].(*ast.FnDecl)
	if !ok || probe == nil || probe.Body == nil {
		t.Fatalf("probe decl = %T, want *ast.FnDecl with body", file.Decls[len(file.Decls)-1])
	}
	if len(probe.Body.Stmts) != 1 {
		t.Fatalf("probe body stmts = %d, want 1", len(probe.Body.Stmts))
	}
	retStmt, ok := probe.Body.Stmts[0].(*ast.ReturnStmt)
	if !ok || retStmt == nil {
		t.Fatalf("probe stmt = %T, want *ast.ReturnStmt", probe.Body.Stmts[0])
	}
	call, ok := retStmt.Value.(*ast.CallExpr)
	if !ok || call == nil {
		t.Fatalf("probe return value = %T, want *ast.CallExpr", retStmt.Value)
	}
	delete(chk.Types, call)

	mod, issues := Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}

	var irProbe *FnDecl
	for _, decl := range mod.Decls {
		if fn, ok := decl.(*FnDecl); ok && fn.Name == "probe" {
			irProbe = fn
			break
		}
	}
	if irProbe == nil || irProbe.Body == nil {
		t.Fatal("lowered probe function missing body")
	}
	if len(irProbe.Body.Stmts) != 1 {
		t.Fatalf("lowered probe stmts = %d, want 1", len(irProbe.Body.Stmts))
	}
	irRet, ok := irProbe.Body.Stmts[0].(*ReturnStmt)
	if !ok || irRet == nil {
		t.Fatalf("lowered probe stmt = %T, want *ReturnStmt", irProbe.Body.Stmts[0])
	}
	mc, ok := irRet.Value.(*MethodCall)
	if !ok {
		t.Fatalf("probe return value = %T, want *MethodCall", irRet.Value)
	}
	opt, ok := mc.T.(*OptionalType)
	if !ok {
		t.Fatalf("method call type = %T (%v), want *OptionalType", mc.T, mc.T)
	}
	named, ok := opt.Inner.(*NamedType)
	if !ok {
		t.Fatalf("optional inner = %T (%v), want *NamedType", opt.Inner, opt.Inner)
	}
	if named.Name != "Note" {
		t.Fatalf("optional inner name = %q, want %q", named.Name, "Note")
	}
}

func TestLowerUseDeclRecoversBuiltinGenericTypesWithoutResolverTypeRefs(t *testing.T) {
	src := `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}
`
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse: %v", parseDiags)
	}
	res := resolve.ResolveFileSourceDefault([]byte(src), file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{

		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
	})

	mod, issues := Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("Lower() issues = %v, want none", issues)
	}
	if len(mod.Decls) != 1 {
		t.Fatalf("decls = %d, want 1", len(mod.Decls))
	}
	use, ok := mod.Decls[0].(*UseDecl)
	if !ok || use == nil {
		t.Fatalf("decl = %T, want *UseDecl", mod.Decls[0])
	}
	if got, want := len(use.GoBody), 1; got != want {
		t.Fatalf("use.GoBody len = %d, want %d", got, want)
	}
	fn, ok := use.GoBody[0].(*FnDecl)
	if !ok || fn == nil {
		t.Fatalf("use.GoBody[0] = %T, want *FnDecl", use.GoBody[0])
	}
	ret, ok := fn.Return.(*NamedType)
	if !ok || ret == nil {
		t.Fatalf("fn return = %T (%v), want *NamedType", fn.Return, fn.Return)
	}
	if !ret.Builtin || ret.Name != "List" {
		t.Fatalf("fn return = %#v, want builtin List", ret)
	}
	if got, want := len(ret.Args), 1; got != want {
		t.Fatalf("return args = %d, want %d", got, want)
	}
	if ret.Args[0] != TString {
		t.Fatalf("return inner = %v, want TString", ret.Args[0])
	}
}

// TestRecoverMethodReturnTypeToStringOnPrimitives — `Char.toString()`,
// `Int.toString()`, `Float.toString()`, `Byte.toString()`,
// `Bool.toString()` must all recover to `String`. Without this arm,
// a stdlib body injected under `OSTY_STDLIB_BODY_LOWER=1` that calls
// e.g. `fill.toString()` (Char receiver) ends up with an ErrType
// destination local that the LLVM emitter rejects with
// "unsupported local type <error> ... written by call Char__toString".
// The MIR backend's `emitPrimitiveMethodCall` already routes the
// mangled `Type__toString` symbol to the matching runtime helper —
// it just needs the call's destination local to carry the right type
// so the function can be checkFunctionSupported-clean.
func TestRecoverMethodReturnTypeToStringOnPrimitives(t *testing.T) {
	cases := []struct {
		name string
		in   Type
	}{
		{"Char", TChar},
		{"Byte", TByte},
		{"Int", TInt},
		{"Float", TFloat},
		{"Bool", TBool},
		{"String", TString},
	}
	for _, tc := range cases {
		got := recoverMethodReturnTypeFromType("toString", tc.in)
		if got != TString {
			t.Errorf("recoverMethodReturnTypeFromType(toString, %s) = %v, want TString", tc.name, got)
		}
	}
}
