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
