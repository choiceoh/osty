package ir_test

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ir"
)

// Expanded tests covering the second round of IR work: structs with
// methods, enums and variant construction/match, patterns, closures,
// `?` / `??`, maps/tuples/field/index access, use/interface/alias,
// and the walk + print helpers.

func TestLowerStructMethodAndCall(t *testing.T) {
	mod, issues := lower(t, `
struct Point {
    x: Int,
    y: Int,

    fn sum(self) -> Int {
        self.x + self.y
    }
}

fn main() {
    let p = Point { x: 3, y: 4 }
    println(p.sum())
}
`)
	// The checker may warn (no diagnostics but unused) — tolerate.
	_ = issues
	if len(mod.Decls) < 2 {
		t.Fatalf("decls = %d", len(mod.Decls))
	}
	sd, ok := mod.Decls[0].(*ir.StructDecl)
	if !ok {
		t.Fatalf("want StructDecl, got %T", mod.Decls[0])
	}
	if len(sd.Methods) != 1 {
		t.Fatalf("methods = %d", len(sd.Methods))
	}
	if sd.Methods[0].Name != "sum" {
		t.Fatalf("method name = %q", sd.Methods[0].Name)
	}

	main := mod.Decls[1].(*ir.FnDecl)
	// First stmt: let p = Point { ... }
	let0 := main.Body.Stmts[0].(*ir.LetStmt)
	lit, ok := let0.Value.(*ir.StructLit)
	if !ok {
		t.Fatalf("want StructLit, got %T", let0.Value)
	}
	if lit.TypeName != "Point" || len(lit.Fields) != 2 {
		t.Fatalf("struct lit = %+v", lit)
	}
	// Second stmt: println(p.sum())
	es := main.Body.Stmts[1].(*ir.ExprStmt)
	call := es.X.(*ir.IntrinsicCall)
	mc, ok := call.Args[0].(*ir.MethodCall)
	if !ok {
		t.Fatalf("want MethodCall arg, got %T", call.Args[0])
	}
	if mc.Name != "sum" {
		t.Fatalf("method name = %q", mc.Name)
	}
	// Receiver should be an Ident("p").
	if id, ok := mc.Receiver.(*ir.Ident); !ok || id.Name != "p" {
		t.Fatalf("receiver = %T %+v", mc.Receiver, mc.Receiver)
	}
}

func TestLowerFieldAccess(t *testing.T) {
	mod, _ := lower(t, `
struct Point { x: Int, y: Int }

fn getX(p: Point) -> Int {
    p.x
}
`)
	fn := mod.Decls[1].(*ir.FnDecl)
	if fn.Body.Result == nil {
		t.Fatalf("expected block result")
	}
	fe, ok := fn.Body.Result.(*ir.FieldExpr)
	if !ok {
		t.Fatalf("want FieldExpr, got %T", fn.Body.Result)
	}
	if fe.Name != "x" {
		t.Fatalf("name = %q", fe.Name)
	}
	if fe.Optional {
		t.Fatalf("should not be optional")
	}
}

func TestLowerEnumAndVariantConstruct(t *testing.T) {
	mod, _ := lower(t, `
enum Shape {
    Circle(Float),
    Square(Int),
    Empty,
}

fn make() -> Shape {
    Shape.Circle(1.5)
}
`)
	ed, ok := mod.Decls[0].(*ir.EnumDecl)
	if !ok {
		t.Fatalf("want EnumDecl, got %T", mod.Decls[0])
	}
	if len(ed.Variants) != 3 {
		t.Fatalf("variants = %d", len(ed.Variants))
	}

	fn := mod.Decls[1].(*ir.FnDecl)
	// The variant construct may surface either as the block Result (if
	// the checker promoted the call to a typed value) or as a
	// side-effect ExprStmt. Accept either.
	var vl *ir.VariantLit
	if r, ok := fn.Body.Result.(*ir.VariantLit); ok {
		vl = r
	} else {
		for _, s := range fn.Body.Stmts {
			if es, ok := s.(*ir.ExprStmt); ok {
				if v, ok := es.X.(*ir.VariantLit); ok {
					vl = v
					break
				}
			}
		}
	}
	if vl == nil {
		t.Fatalf("VariantLit not found in body: %s", ir.PrintNode(fn.Body))
	}
	if vl.Enum != "Shape" || vl.Variant != "Circle" {
		t.Fatalf("variant = %+v", vl)
	}
	if len(vl.Args) != 1 {
		t.Fatalf("args = %d", len(vl.Args))
	}
}

func TestLowerMatchExpr(t *testing.T) {
	mod, _ := lower(t, `
enum Shape { Circle(Float), Square(Int), Empty }

fn area(s: Shape) -> Float {
    match s {
        Shape.Circle(r) -> r * r,
        Shape.Square(side) -> 0.0,
        Shape.Empty -> 0.0,
    }
}
`)
	fn := mod.Decls[1].(*ir.FnDecl)
	me, ok := fn.Body.Result.(*ir.MatchExpr)
	if !ok {
		t.Fatalf("want MatchExpr, got %T", fn.Body.Result)
	}
	if len(me.Arms) != 3 {
		t.Fatalf("arms = %d", len(me.Arms))
	}
	// First arm pattern: Shape.Circle(r)
	vp, ok := me.Arms[0].Pattern.(*ir.VariantPat)
	if !ok {
		t.Fatalf("want VariantPat, got %T", me.Arms[0].Pattern)
	}
	if vp.Variant != "Circle" {
		t.Fatalf("variant = %q", vp.Variant)
	}
	if len(vp.Args) != 1 {
		t.Fatalf("variant args = %d", len(vp.Args))
	}
	if _, ok := vp.Args[0].(*ir.IdentPat); !ok {
		t.Fatalf("want IdentPat, got %T", vp.Args[0])
	}
	// Third arm: Shape.Empty bare, no args.
	v3 := me.Arms[2].Pattern.(*ir.VariantPat)
	if v3.Variant != "Empty" || len(v3.Args) != 0 {
		t.Fatalf("Empty pattern = %+v", v3)
	}
}

func TestLowerTuplePattern(t *testing.T) {
	mod, _ := lower(t, `
fn swap(p: (Int, Int)) -> (Int, Int) {
    let (a, b) = p
    (b, a)
}
`)
	fn := mod.Decls[0].(*ir.FnDecl)
	let0 := fn.Body.Stmts[0].(*ir.LetStmt)
	if let0.Pattern == nil {
		t.Fatalf("expected destructuring pattern")
	}
	tp, ok := let0.Pattern.(*ir.TuplePat)
	if !ok {
		t.Fatalf("want TuplePat, got %T", let0.Pattern)
	}
	if len(tp.Elems) != 2 {
		t.Fatalf("elems = %d", len(tp.Elems))
	}
	// Result (b, a) is a TupleLit.
	if tl, ok := fn.Body.Result.(*ir.TupleLit); !ok || len(tl.Elems) != 2 {
		t.Fatalf("want TupleLit, got %T %+v", fn.Body.Result, fn.Body.Result)
	}
}

func TestLowerClosure(t *testing.T) {
	mod, _ := lower(t, `
fn apply(f: fn(Int) -> Int, x: Int) -> Int {
    f(x)
}

fn main() {
    let g = |n: Int| n + 1
    apply(g, 5)
}
`)
	main := mod.Decls[1].(*ir.FnDecl)
	let0 := main.Body.Stmts[0].(*ir.LetStmt)
	cl, ok := let0.Value.(*ir.Closure)
	if !ok {
		t.Fatalf("want Closure, got %T", let0.Value)
	}
	if len(cl.Params) != 1 || cl.Params[0].Name != "n" {
		t.Fatalf("params = %+v", cl.Params)
	}
	if cl.Body == nil {
		t.Fatalf("closure body nil")
	}
	// Body is either a Result-only block or contains a BinaryExpr.
	if cl.Body.Result == nil && len(cl.Body.Stmts) == 0 {
		t.Fatalf("empty body")
	}
}

func TestLowerQuestionAndCoalesce(t *testing.T) {
	mod, _ := lower(t, `
fn maybe() -> Int? {
    42
}

fn use_it() -> Int {
    let x = maybe() ?? 0
    x
}
`)
	fn := mod.Decls[1].(*ir.FnDecl)
	let0 := fn.Body.Stmts[0].(*ir.LetStmt)
	co, ok := let0.Value.(*ir.CoalesceExpr)
	if !ok {
		t.Fatalf("want CoalesceExpr, got %T", let0.Value)
	}
	if _, ok := co.Right.(*ir.IntLit); !ok {
		t.Fatalf("rhs = %T", co.Right)
	}
}

func TestLowerOptionalChain(t *testing.T) {
	mod, _ := lower(t, `
struct User { name: String }

fn display(u: User?) -> String {
    u?.name ?? "anon"
}
`)
	fn := mod.Decls[1].(*ir.FnDecl)
	co, ok := fn.Body.Result.(*ir.CoalesceExpr)
	if !ok {
		t.Fatalf("want CoalesceExpr, got %T", fn.Body.Result)
	}
	fe, ok := co.Left.(*ir.FieldExpr)
	if !ok {
		t.Fatalf("want FieldExpr, got %T", co.Left)
	}
	if !fe.Optional {
		t.Fatalf("expected optional field")
	}
}

func TestLowerAssignToField(t *testing.T) {
	// Osty doesn't have `mut` on fn params, so exercise AssignStmt on
	// mutable struct fields instead. Single target, confirm lowering
	// preserves the FieldExpr as target.
	mod, _ := lower(t, `
struct Counter {
    count: Int,

    fn inc(mut self) {
        self.count = self.count + 1
    }
}
`)
	sd := mod.Decls[0].(*ir.StructDecl)
	method := sd.Methods[0]
	as0, ok := method.Body.Stmts[0].(*ir.AssignStmt)
	if !ok {
		t.Fatalf("want AssignStmt, got %T", method.Body.Stmts[0])
	}
	if len(as0.Targets) != 1 {
		t.Fatalf("targets = %d", len(as0.Targets))
	}
	if _, ok := as0.Targets[0].(*ir.FieldExpr); !ok {
		t.Fatalf("target = %T", as0.Targets[0])
	}
}

func TestLowerUseAndAlias(t *testing.T) {
	mod, _ := lower(t, `
type Id = Int

fn f() -> Id { 1 }
`)
	found := false
	for _, d := range mod.Decls {
		if ta, ok := d.(*ir.TypeAliasDecl); ok {
			found = true
			if ta.Name != "Id" {
				t.Fatalf("alias name = %q", ta.Name)
			}
			if ta.Target != ir.TInt {
				t.Fatalf("alias target = %v", ta.Target)
			}
		}
	}
	if !found {
		t.Fatalf("TypeAliasDecl not present in %+v", mod.Decls)
	}
}

func TestLowerInterface(t *testing.T) {
	mod, _ := lower(t, `
interface Greet {
    fn hello(self) -> String
}
`)
	id, ok := mod.Decls[0].(*ir.InterfaceDecl)
	if !ok {
		t.Fatalf("want InterfaceDecl, got %T", mod.Decls[0])
	}
	if id.Name != "Greet" || len(id.Methods) != 1 {
		t.Fatalf("iface = %+v", id)
	}
}

func TestLowerMapLiteral(t *testing.T) {
	mod, issues := lower(t, `
fn build() -> Map<String, Int> {
    let m: Map<String, Int> = {"a": 1, "b": 2}
    m
}
`)
	_ = issues
	fn := mod.Decls[0].(*ir.FnDecl)
	let0 := fn.Body.Stmts[0].(*ir.LetStmt)
	ml, ok := let0.Value.(*ir.MapLit)
	if !ok {
		t.Fatalf("want MapLit, got %T", let0.Value)
	}
	if len(ml.Entries) != 2 {
		t.Fatalf("entries = %d", len(ml.Entries))
	}
}

func TestWalkCountsNodes(t *testing.T) {
	mod, _ := lower(t, `
fn f(a: Int, b: Int) -> Int {
    if a > b {
        a
    } else {
        b
    }
}
`)
	var exprs, stmts, decls int
	ir.Inspect(mod, func(n ir.Node) bool {
		switch n.(type) {
		case ir.Expr:
			exprs++
		case ir.Stmt:
			stmts++
		case ir.Decl:
			decls++
		}
		return true
	})
	if decls < 1 {
		t.Errorf("decls = %d", decls)
	}
	if exprs < 3 {
		t.Errorf("exprs = %d", exprs)
	}
	// Walker hit every branch of an if-expr's two blocks.
	if stmts < 1 {
		t.Errorf("stmts = %d", stmts)
	}
}

func TestPrintStable(t *testing.T) {
	mod, _ := lower(t, `
fn add(a: Int, b: Int) -> Int {
    a + b
}
`)
	out := ir.Print(mod)
	// Spot-check that the expected structure is present in the output.
	wants := []string{`(module "main"`, `(fn add`, `(+`}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("Print missing %q in:\n%s", w, out)
		}
	}
}

func TestPrintMatch(t *testing.T) {
	mod, _ := lower(t, `
enum Shape { Circle(Float), Empty }

fn dispatch(s: Shape) -> Int {
    match s {
        Shape.Circle(_) -> 1,
        Shape.Empty -> 0,
    }
}
`)
	out := ir.Print(mod)
	if !strings.Contains(out, "(match ") {
		t.Errorf("Print missing match form:\n%s", out)
	}
	if !strings.Contains(out, "Circle(_)") && !strings.Contains(out, "Shape.Circle") {
		t.Errorf("Print missing variant pattern:\n%s", out)
	}
}

func TestLowerDefer(t *testing.T) {
	mod, _ := lower(t, `
fn f() {
    defer println("bye")
    println("hi")
}
`)
	fn := mod.Decls[0].(*ir.FnDecl)
	ds, ok := fn.Body.Stmts[0].(*ir.DeferStmt)
	if !ok {
		t.Fatalf("want DeferStmt, got %T", fn.Body.Stmts[0])
	}
	if ds.Body == nil {
		t.Fatalf("nil defer body")
	}
}

func TestValidateClean(t *testing.T) {
	mod, _ := lower(t, `
struct P { x: Int, y: Int }

enum Shape { Circle(Float), Empty }

fn demo(a: Int, b: Int) -> Int {
    let p = P { x: a, y: b }
    if a > b { a } else { b }
}
`)
	errs := ir.Validate(mod)
	if len(errs) != 0 {
		t.Errorf("Validate reported %d errors:\n", len(errs))
		for _, e := range errs {
			t.Logf("  %v", e)
		}
	}
}

func TestLowerWildcardPattern(t *testing.T) {
	mod, _ := lower(t, `
enum Flag { On, Off }

fn toInt(f: Flag) -> Int {
    match f {
        Flag.On -> 1,
        _ -> 0,
    }
}
`)
	fn := mod.Decls[1].(*ir.FnDecl)
	me := fn.Body.Result.(*ir.MatchExpr)
	if _, ok := me.Arms[1].Pattern.(*ir.WildPat); !ok {
		t.Fatalf("want WildPat in last arm, got %T", me.Arms[1].Pattern)
	}
}
