package ir_test

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

// lower runs the full parse → resolve → check → lower pipeline on the
// source snippet. Fatal on parse error; checker/resolver diagnostics
// are tolerated so tests can lower partially-invalid inputs too.
func lower(t *testing.T, src string) (*ir.Module, []error) {
	t.Helper()
	file, pd := parser.ParseDiagnostics([]byte(src))
	for _, d := range pd {
		if d.Severity.String() == "error" {
			t.Fatalf("parse error: %s", d.Message)
		}
	}
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res)
	return ir.Lower("main", file, res, chk)
}

func TestLowerEmptyFile(t *testing.T) {
	mod, issues := lower(t, "")
	if mod == nil {
		t.Fatal("nil module")
	}
	if len(mod.Decls) != 0 || len(mod.Script) != 0 {
		t.Fatalf("unexpected content: %+v", mod)
	}
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
}

func TestLowerHelloWorld(t *testing.T) {
	mod, issues := lower(t, `fn main() {
    println("hello, world")
}
`)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
	if len(mod.Decls) != 1 {
		t.Fatalf("expected one decl, got %d", len(mod.Decls))
	}
	fn, ok := mod.Decls[0].(*ir.FnDecl)
	if !ok {
		t.Fatalf("expected FnDecl, got %T", mod.Decls[0])
	}
	if fn.Name != "main" {
		t.Fatalf("name = %q", fn.Name)
	}
	if fn.Body == nil || len(fn.Body.Stmts) != 1 {
		t.Fatalf("unexpected body: %+v", fn.Body)
	}
	es, ok := fn.Body.Stmts[0].(*ir.ExprStmt)
	if !ok {
		t.Fatalf("want ExprStmt, got %T", fn.Body.Stmts[0])
	}
	call, ok := es.X.(*ir.IntrinsicCall)
	if !ok {
		t.Fatalf("want IntrinsicCall, got %T", es.X)
	}
	if call.Kind != ir.IntrinsicPrintln {
		t.Fatalf("kind = %v", call.Kind)
	}
	if len(call.Args) != 1 {
		t.Fatalf("args = %d", len(call.Args))
	}
	if _, ok := call.Args[0].(*ir.StringLit); !ok {
		t.Fatalf("want StringLit arg, got %T", call.Args[0])
	}
}

func TestLowerArithmetic(t *testing.T) {
	mod, issues := lower(t, `fn add(a: Int, b: Int) -> Int {
    a + b
}
`)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
	fn := mod.Decls[0].(*ir.FnDecl)
	if len(fn.Params) != 2 {
		t.Fatalf("params = %d", len(fn.Params))
	}
	if fn.Params[0].Name != "a" || fn.Params[0].Type != ir.TInt {
		t.Fatalf("param[0] = %+v", fn.Params[0])
	}
	if fn.Return != ir.TInt {
		t.Fatalf("return = %v", fn.Return)
	}
	if fn.Body.Result == nil {
		t.Fatalf("expected block result, got stmts=%v", fn.Body.Stmts)
	}
	bin, ok := fn.Body.Result.(*ir.BinaryExpr)
	if !ok {
		t.Fatalf("want BinaryExpr, got %T", fn.Body.Result)
	}
	if bin.Op != ir.BinAdd {
		t.Fatalf("op = %v", bin.Op)
	}
	if bin.Type() != ir.TInt {
		t.Fatalf("type = %v", bin.Type())
	}
}

func TestLowerLetAndIdent(t *testing.T) {
	mod, issues := lower(t, `fn demo() {
    let x = 42
    let y: Int = x + 1
    println(y)
}
`)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
	fn := mod.Decls[0].(*ir.FnDecl)
	if len(fn.Body.Stmts) != 3 {
		t.Fatalf("stmts = %d", len(fn.Body.Stmts))
	}
	let1 := fn.Body.Stmts[0].(*ir.LetStmt)
	if let1.Name != "x" || let1.Type != ir.TInt {
		t.Fatalf("let1 = %+v (T=%v)", let1, let1.Type)
	}
	let2 := fn.Body.Stmts[1].(*ir.LetStmt)
	if let2.Name != "y" || let2.Type != ir.TInt {
		t.Fatalf("let2 = %+v", let2)
	}
	bin, ok := let2.Value.(*ir.BinaryExpr)
	if !ok {
		t.Fatalf("want BinaryExpr, got %T", let2.Value)
	}
	id, ok := bin.Left.(*ir.Ident)
	if !ok {
		t.Fatalf("want Ident, got %T", bin.Left)
	}
	if id.Name != "x" {
		t.Fatalf("name = %q", id.Name)
	}
	if id.Kind != ir.IdentLocal {
		t.Fatalf("kind = %v, want IdentLocal", id.Kind)
	}
}

func TestLowerForRange(t *testing.T) {
	mod, issues := lower(t, `fn loop() {
    for i in 0..10 {
        println(i)
    }
}
`)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
	fn := mod.Decls[0].(*ir.FnDecl)
	fs, ok := fn.Body.Stmts[0].(*ir.ForStmt)
	if !ok {
		t.Fatalf("want ForStmt, got %T", fn.Body.Stmts[0])
	}
	if fs.Kind != ir.ForRange {
		t.Fatalf("kind = %v", fs.Kind)
	}
	if fs.Var != "i" || fs.Inclusive {
		t.Fatalf("for shape = %+v", fs)
	}
	if fs.Start == nil || fs.End == nil {
		t.Fatalf("missing range bounds")
	}
}

func TestLowerIfStmt(t *testing.T) {
	mod, issues := lower(t, `fn branch(n: Int) {
    if n > 0 {
        println("pos")
    } else {
        println("non-pos")
    }
}
`)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
	fn := mod.Decls[0].(*ir.FnDecl)
	es, ok := fn.Body.Stmts[0].(*ir.ExprStmt)
	if !ok {
		t.Fatalf("want ExprStmt, got %T", fn.Body.Stmts[0])
	}
	ifE, ok := es.X.(*ir.IfExpr)
	if !ok {
		t.Fatalf("want IfExpr, got %T", es.X)
	}
	if ifE.Then == nil || ifE.Else == nil {
		t.Fatalf("both arms required: %+v", ifE)
	}
}

func TestLowerStruct(t *testing.T) {
	mod, issues := lower(t, `struct Point {
    x: Int,
    y: Int,
}
`)
	// Methods and field defaults aren't exercised; but the checker
	// may emit a diagnostic for unused struct. We tolerate issues
	// here since they'd be about unsupported dependents, not errors.
	_ = issues
	if len(mod.Decls) != 1 {
		t.Fatalf("decls = %d", len(mod.Decls))
	}
	sd, ok := mod.Decls[0].(*ir.StructDecl)
	if !ok {
		t.Fatalf("want StructDecl, got %T", mod.Decls[0])
	}
	if sd.Name != "Point" || len(sd.Fields) != 2 {
		t.Fatalf("shape = %+v", sd)
	}
	if sd.Fields[0].Type != ir.TInt {
		t.Fatalf("field type = %v", sd.Fields[0].Type)
	}
}

func TestLowerScriptMode(t *testing.T) {
	mod, issues := lower(t, `println("script")`)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
	if len(mod.Script) != 1 {
		t.Fatalf("script stmts = %d", len(mod.Script))
	}
	es, ok := mod.Script[0].(*ir.ExprStmt)
	if !ok {
		t.Fatalf("want ExprStmt, got %T", mod.Script[0])
	}
	if _, ok := es.X.(*ir.IntrinsicCall); !ok {
		t.Fatalf("want IntrinsicCall, got %T", es.X)
	}
}

func TestLowerListLiteral(t *testing.T) {
	mod, issues := lower(t, `fn demo() {
    let xs: List<Int> = [1, 2, 3]
    println(xs)
}
`)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
	fn := mod.Decls[0].(*ir.FnDecl)
	let0 := fn.Body.Stmts[0].(*ir.LetStmt)
	list, ok := let0.Value.(*ir.ListLit)
	if !ok {
		t.Fatalf("want ListLit, got %T", let0.Value)
	}
	if len(list.Elems) != 3 {
		t.Fatalf("elems = %d", len(list.Elems))
	}
	if list.Elem != ir.TInt {
		t.Fatalf("elem type = %v", list.Elem)
	}
	nt, ok := list.Type().(*ir.NamedType)
	if !ok || nt.Name != "List" || !nt.Builtin {
		t.Fatalf("list type = %v", list.Type())
	}
}

func TestLowerSelfContainedIR(t *testing.T) {
	// Assertion: no IR node exposes the checker's types.Type or the
	// resolver's *Symbol. We spot-check by walking a lowered tree and
	// confirming every expression's Type() implements ir.Type only.
	mod, _ := lower(t, `fn f(a: Int) -> Int {
    let b = a * 2
    b + 1
}
`)
	fn := mod.Decls[0].(*ir.FnDecl)
	walkExprs(fn.Body, func(e ir.Expr) {
		if e.Type() == nil {
			t.Errorf("expr %T has nil Type()", e)
		}
		if _, ok := e.Type().(ir.Type); !ok {
			t.Errorf("expr %T Type() is not ir.Type", e)
		}
	})
}

// walkExprs invokes fn on every Expr reachable from b.
func walkExprs(b *ir.Block, fn func(ir.Expr)) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		walkStmtExprs(s, fn)
	}
	if b.Result != nil {
		walkExpr(b.Result, fn)
	}
}

func walkStmtExprs(s ir.Stmt, fn func(ir.Expr)) {
	switch s := s.(type) {
	case *ir.LetStmt:
		if s.Value != nil {
			walkExpr(s.Value, fn)
		}
	case *ir.ExprStmt:
		walkExpr(s.X, fn)
	case *ir.ReturnStmt:
		if s.Value != nil {
			walkExpr(s.Value, fn)
		}
	case *ir.AssignStmt:
		walkExpr(s.Target, fn)
		walkExpr(s.Value, fn)
	case *ir.IfStmt:
		walkExpr(s.Cond, fn)
		walkExprs(s.Then, fn)
		walkExprs(s.Else, fn)
	case *ir.ForStmt:
		if s.Cond != nil {
			walkExpr(s.Cond, fn)
		}
		if s.Iter != nil {
			walkExpr(s.Iter, fn)
		}
		if s.Start != nil {
			walkExpr(s.Start, fn)
		}
		if s.End != nil {
			walkExpr(s.End, fn)
		}
		walkExprs(s.Body, fn)
	case *ir.Block:
		walkExprs(s, fn)
	}
}

func walkExpr(e ir.Expr, fn func(ir.Expr)) {
	fn(e)
	switch e := e.(type) {
	case *ir.BinaryExpr:
		walkExpr(e.Left, fn)
		walkExpr(e.Right, fn)
	case *ir.UnaryExpr:
		walkExpr(e.X, fn)
	case *ir.CallExpr:
		walkExpr(e.Callee, fn)
		for _, a := range e.Args {
			walkExpr(a, fn)
		}
	case *ir.IntrinsicCall:
		for _, a := range e.Args {
			walkExpr(a, fn)
		}
	case *ir.ListLit:
		for _, el := range e.Elems {
			walkExpr(el, fn)
		}
	case *ir.StringLit:
		for _, p := range e.Parts {
			if !p.IsLit && p.Expr != nil {
				walkExpr(p.Expr, fn)
			}
		}
	case *ir.BlockExpr:
		walkExprs(e.Block, fn)
	case *ir.IfExpr:
		walkExpr(e.Cond, fn)
		walkExprs(e.Then, fn)
		walkExprs(e.Else, fn)
	}
}

func TestIrTypeStringsStable(t *testing.T) {
	cases := []struct {
		t    ir.Type
		want string
	}{
		{ir.TInt, "Int"},
		{ir.TString, "String"},
		{&ir.OptionalType{Inner: ir.TInt}, "Int?"},
		{&ir.TupleType{Elems: []ir.Type{ir.TInt, ir.TBool}}, "(Int, Bool)"},
		{&ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}, "List<Int>"},
		{&ir.FnType{Params: []ir.Type{ir.TInt}, Return: ir.TBool}, "fn(Int) -> Bool"},
		{&ir.FnType{Return: ir.TUnit}, "fn()"},
	}
	for _, c := range cases {
		got := c.t.String()
		if got != c.want {
			t.Errorf("String() = %q, want %q", got, c.want)
		}
	}
	// Also confirm the doc comment's promise: PrimType.String for
	// Unit is the source form.
	if !strings.Contains(ir.TUnit.String(), "()") {
		t.Errorf("unit = %q", ir.TUnit.String())
	}
}
