package parser

import (
	"fmt"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

func parseOrFatal(t *testing.T, src string) *ast.File {
	t.Helper()
	file, errs := Parse([]byte(src))
	if len(errs) > 0 {
		var msgs []string
		for _, e := range errs {
			msgs = append(msgs, e.Error())
		}
		t.Fatalf("unexpected parse errors:\n  %s\nsource:\n%s", strings.Join(msgs, "\n  "), src)
	}
	return file
}

// expectCode asserts the parser emits at least one diagnostic with the
// given code. A helper to keep "reject this input" tests specific rather
// than relying on `len(errs) > 0`, which would pass even if the parser
// rejected the input for the wrong reason.
func expectCode(t *testing.T, src, code string) {
	t.Helper()
	_, diags := ParseDiagnostics([]byte(src))
	for _, d := range diags {
		if d.Code == code {
			return
		}
	}
	var got []string
	for _, d := range diags {
		got = append(got, fmt.Sprintf("%s: %s", d.Code, d.Error()))
	}
	t.Fatalf("expected diagnostic with code %q; got:\n  %s\nsource:\n%s",
		code, strings.Join(got, "\n  "), src)
}

func TestParseSimpleFn(t *testing.T) {
	src := `fn add(a: Int, b: Int) -> Int {
    a + b
}`
	f := parseOrFatal(t, src)
	if len(f.Decls) != 1 {
		t.Fatalf("want 1 decl, got %d", len(f.Decls))
	}
	fd := f.Decls[0].(*ast.FnDecl)
	if fd.Name != "add" {
		t.Fatalf("name = %q", fd.Name)
	}
	if len(fd.Params) != 2 {
		t.Fatalf("params = %d", len(fd.Params))
	}
	if fd.Params[0].Name != "a" || fd.Params[1].Name != "b" {
		t.Fatalf("param names = %v", fd.Params)
	}
	nt, ok := fd.ReturnType.(*ast.NamedType)
	if !ok || nt.Path[0] != "Int" {
		t.Fatalf("return type = %T %+v", fd.ReturnType, fd.ReturnType)
	}
	if fd.Body == nil || len(fd.Body.Stmts) != 1 {
		t.Fatalf("body stmts = %+v", fd.Body)
	}
	es := fd.Body.Stmts[0].(*ast.ExprStmt)
	be := es.X.(*ast.BinaryExpr)
	if be.Op != token.PLUS {
		t.Fatalf("op = %s", be.Op)
	}
}

func TestParseLetStmt(t *testing.T) {
	src := `fn f() {
    let x = 5
    let mut y: Int = 10
    let (a, b) = makeTuple()
    let User { name, age } = getUser()
}`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	if len(body.Stmts) != 4 {
		t.Fatalf("stmts = %d", len(body.Stmts))
	}
	let0 := body.Stmts[0].(*ast.LetStmt)
	if _, ok := let0.Pattern.(*ast.IdentPat); !ok {
		t.Fatalf("let x pattern = %T", let0.Pattern)
	}
	let1 := body.Stmts[1].(*ast.LetStmt)
	if !let1.Mut || let1.Type == nil {
		t.Fatalf("let mut y mismatch: mut=%v, type=%v", let1.Mut, let1.Type)
	}
	let2 := body.Stmts[2].(*ast.LetStmt)
	if _, ok := let2.Pattern.(*ast.TuplePat); !ok {
		t.Fatalf("let (a,b) pattern = %T", let2.Pattern)
	}
	let3 := body.Stmts[3].(*ast.LetStmt)
	sp, ok := let3.Pattern.(*ast.StructPat)
	if !ok {
		t.Fatalf("let User pattern = %T", let3.Pattern)
	}
	if len(sp.Fields) != 2 {
		t.Fatalf("struct pat fields = %d", len(sp.Fields))
	}
}

func TestParseBinaryPrecedence(t *testing.T) {
	src := `fn f() -> Int { 1 + 2 * 3 }`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	be := body.Stmts[0].(*ast.ExprStmt).X.(*ast.BinaryExpr)
	if be.Op != token.PLUS {
		t.Fatalf("outer op = %s", be.Op)
	}
	right := be.Right.(*ast.BinaryExpr)
	if right.Op != token.STAR {
		t.Fatalf("inner op = %s", right.Op)
	}
}

func TestParseNilCoalescingPrecedence(t *testing.T) {
	// Per spec, `??` binds looser than comparison: `a == b ?? c` is
	// `(a == b) ?? c`.
	src := `fn f() { a == b ?? c }`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	be := body.Stmts[0].(*ast.ExprStmt).X.(*ast.BinaryExpr)
	if be.Op != token.QQ {
		t.Fatalf("outer op = %s; want ??", be.Op)
	}
	left := be.Left.(*ast.BinaryExpr)
	if left.Op != token.EQ {
		t.Fatalf("inner op = %s; want ==", left.Op)
	}
}

func TestParseCallAndField(t *testing.T) {
	src := `fn f() { user.name.toUpper() }`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	call := body.Stmts[0].(*ast.ExprStmt).X.(*ast.CallExpr)
	fieldUp := call.Fn.(*ast.FieldExpr)
	if fieldUp.Name != "toUpper" {
		t.Fatalf("method = %s", fieldUp.Name)
	}
	fieldName := fieldUp.X.(*ast.FieldExpr)
	if fieldName.Name != "name" {
		t.Fatalf("field = %s", fieldName.Name)
	}
	if id, ok := fieldName.X.(*ast.Ident); !ok || id.Name != "user" {
		t.Fatalf("base = %T %+v", fieldName.X, fieldName.X)
	}
}

func TestParseQuestion(t *testing.T) {
	src := `fn f() -> Result<Int, Error> { doIt()? }`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	q := body.Stmts[0].(*ast.ExprStmt).X.(*ast.QuestionExpr)
	if _, ok := q.X.(*ast.CallExpr); !ok {
		t.Fatalf("question base = %T", q.X)
	}
}

func TestParseIfExpr(t *testing.T) {
	src := `fn f() -> String {
    if score >= 90 {
        "A"
    } else if score >= 80 {
        "B"
    } else {
        "C"
    }
}`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	ife := body.Stmts[0].(*ast.ExprStmt).X.(*ast.IfExpr)
	if ife.Else == nil {
		t.Fatalf("else is nil")
	}
	elseIf, ok := ife.Else.(*ast.IfExpr)
	if !ok {
		t.Fatalf("else = %T", ife.Else)
	}
	if elseIf.Else == nil {
		t.Fatalf("final else is nil")
	}
}

func TestParseIfLet(t *testing.T) {
	src := `fn f() {
    if let Some(u) = user {
        println("hi, {u.name}")
    }
}`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	ife := body.Stmts[0].(*ast.ExprStmt).X.(*ast.IfExpr)
	if !ife.IsIfLet {
		t.Fatal("want IsIfLet")
	}
	vp := ife.Pattern.(*ast.VariantPat)
	if len(vp.Path) != 1 || vp.Path[0] != "Some" {
		t.Fatalf("pattern path = %v", vp.Path)
	}
}

func TestParseMatch(t *testing.T) {
	src := `fn f(shape: Shape) -> Float {
    match shape {
        Circle(r) -> 3.14 * r * r,
        Rect(w, h) -> w * h,
        Empty -> 0.0,
    }
}`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	m := body.Stmts[0].(*ast.ExprStmt).X.(*ast.MatchExpr)
	if len(m.Arms) != 3 {
		t.Fatalf("arms = %d", len(m.Arms))
	}
	if _, ok := m.Arms[0].Pattern.(*ast.VariantPat); !ok {
		t.Fatalf("arm 0 pat = %T", m.Arms[0].Pattern)
	}
}

func TestParseMatchGuard(t *testing.T) {
	src := `fn f() -> String {
    match x {
        Some(n) if n > 0 -> "positive",
        Some(n) if n < 0 -> "negative",
        Some(_) -> "zero",
        None -> "missing",
    }
}`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	m := body.Stmts[0].(*ast.ExprStmt).X.(*ast.MatchExpr)
	if m.Arms[0].Guard == nil {
		t.Fatal("first arm guard is nil")
	}
	if m.Arms[2].Guard != nil {
		t.Fatal("arm[2] should have no guard")
	}
}

func TestParseForIn(t *testing.T) {
	src := `fn f() {
    for i in 0..10 {
        println("{i}")
    }
}`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	fst := body.Stmts[0].(*ast.ForStmt)
	if fst.Pattern == nil {
		t.Fatal("pattern nil")
	}
	if re, ok := fst.Iter.(*ast.RangeExpr); !ok {
		t.Fatalf("iter = %T", fst.Iter)
	} else if re.Inclusive {
		t.Fatal("range should be exclusive")
	}
}

func TestParseForLet(t *testing.T) {
	src := `fn f() {
    for let Some(x) = queue.pop() {
        process(x)
    }
}`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	fst := body.Stmts[0].(*ast.ForStmt)
	if !fst.IsForLet {
		t.Fatal("want IsForLet")
	}
}

func TestParseClosure(t *testing.T) {
	src := `fn f() { list.map(|x| x * 2) }`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	call := body.Stmts[0].(*ast.ExprStmt).X.(*ast.CallExpr)
	if len(call.Args) != 1 {
		t.Fatalf("args = %d", len(call.Args))
	}
	if _, ok := call.Args[0].Value.(*ast.ClosureExpr); !ok {
		t.Fatalf("arg = %T", call.Args[0].Value)
	}
}

func TestParseStructLit(t *testing.T) {
	src := `fn f() {
    let p = Point { x: 0, y: 0 }
    let older = User { ..user, age: 31 }
}`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	l0 := body.Stmts[0].(*ast.LetStmt).Value.(*ast.StructLit)
	if len(l0.Fields) != 2 {
		t.Fatalf("fields = %d", len(l0.Fields))
	}
	l1 := body.Stmts[1].(*ast.LetStmt).Value.(*ast.StructLit)
	if l1.Spread == nil {
		t.Fatal("spread nil")
	}
	if len(l1.Fields) != 1 {
		t.Fatalf("fields = %d", len(l1.Fields))
	}
}

func TestParseStructDecl(t *testing.T) {
	src := `pub struct User {
    pub name: String,
    pub age: Int,
    email: String,

    pub fn new(name: String, email: String) -> User {
        User { name, age: 0, email }
    }

    pub fn greet(self) -> String {
        "hi, {self.name}"
    }
}`
	f := parseOrFatal(t, src)
	sd := f.Decls[0].(*ast.StructDecl)
	if !sd.Pub {
		t.Fatal("pub not set")
	}
	if len(sd.Fields) != 3 {
		t.Fatalf("fields = %d", len(sd.Fields))
	}
	if len(sd.Methods) != 2 {
		t.Fatalf("methods = %d", len(sd.Methods))
	}
	if sd.Methods[1].Recv == nil {
		t.Fatal("greet() should have self receiver")
	}
}

func TestParseEnumDecl(t *testing.T) {
	src := `pub enum Shape {
    Circle(Float),
    Rect(Float, Float),
    Empty,

    pub fn isEmpty(self) -> Bool {
        match self {
            Empty -> true,
            _ -> false,
        }
    }
}`
	f := parseOrFatal(t, src)
	ed := f.Decls[0].(*ast.EnumDecl)
	if len(ed.Variants) != 3 {
		t.Fatalf("variants = %d", len(ed.Variants))
	}
	if len(ed.Variants[0].Fields) != 1 {
		t.Fatalf("Circle fields = %d", len(ed.Variants[0].Fields))
	}
	if len(ed.Methods) != 1 {
		t.Fatalf("methods = %d", len(ed.Methods))
	}
}

func TestParseInterfaceDecl(t *testing.T) {
	src := `pub interface Error {
    fn message(self) -> String
    fn source(self) -> Error? { None }
}`
	f := parseOrFatal(t, src)
	id := f.Decls[0].(*ast.InterfaceDecl)
	if len(id.Methods) != 2 {
		t.Fatalf("methods = %d", len(id.Methods))
	}
	if id.Methods[0].Body != nil {
		t.Fatal("message should have no body")
	}
	if id.Methods[1].Body == nil {
		t.Fatal("source should have default body")
	}
}

func TestParseInterfaceComposition(t *testing.T) {
	src := `pub interface ReadWriter {
    Reader
    Writer
}`
	f := parseOrFatal(t, src)
	id := f.Decls[0].(*ast.InterfaceDecl)
	if len(id.Extends) != 2 {
		t.Fatalf("extends = %d", len(id.Extends))
	}
}

func TestParseGenerics(t *testing.T) {
	src := `fn max<T: Ordered>(a: T, b: T) -> T {
    if a > b { a } else { b }
}`
	f := parseOrFatal(t, src)
	fd := f.Decls[0].(*ast.FnDecl)
	if len(fd.Generics) != 1 {
		t.Fatalf("generics = %d", len(fd.Generics))
	}
	if fd.Generics[0].Name != "T" || len(fd.Generics[0].Constraints) != 1 {
		t.Fatalf("generic = %+v", fd.Generics[0])
	}
}

func TestParseTurbofish(t *testing.T) {
	src := `fn f() {
    let cfg = json.parse::<Config>(text)
}`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	call := body.Stmts[0].(*ast.LetStmt).Value.(*ast.CallExpr)
	if _, ok := call.Fn.(*ast.TurbofishExpr); !ok {
		t.Fatalf("call fn = %T", call.Fn)
	}
}

func TestParseKeywordArgs(t *testing.T) {
	src := `fn f() {
    connect("api.com", timeout: 60)
    connect("api.com", port: 443, timeout: 60)
}`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	c0 := body.Stmts[0].(*ast.ExprStmt).X.(*ast.CallExpr)
	if c0.Args[1].Name != "timeout" {
		t.Fatalf("arg name = %q", c0.Args[1].Name)
	}
}

func TestParseRange(t *testing.T) {
	src := `fn f() {
    let r1 = 0..10
    let r2 = 0..=10
    let r3 = ..10
    let r4 = 0..
}`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	for i, s := range body.Stmts {
		if _, ok := s.(*ast.LetStmt).Value.(*ast.RangeExpr); !ok {
			t.Fatalf("stmt %d value = %T", i, s.(*ast.LetStmt).Value)
		}
	}
}

func TestParseTypeAlias(t *testing.T) {
	src := `pub type UserMap = Map<String, List<User>>
type Handler = fn(Request) -> Result<Response, Error>`
	f := parseOrFatal(t, src)
	if len(f.Decls) != 2 {
		t.Fatalf("decls = %d", len(f.Decls))
	}
	if _, ok := f.Decls[1].(*ast.TypeAliasDecl).Target.(*ast.FnType); !ok {
		t.Fatal("Handler target not FnType")
	}
}

func TestParseUse(t *testing.T) {
	src := `use std.fs
use github.com/user/lib as mylib
use go "net/http" {
    fn Get(url: String) -> Result<Response, Error>
}
fn main() {}`
	f := parseOrFatal(t, src)
	if len(f.Uses) != 3 {
		t.Fatalf("uses = %d", len(f.Uses))
	}
	if f.Uses[1].Alias != "mylib" {
		t.Fatalf("alias = %q", f.Uses[1].Alias)
	}
	if !f.Uses[2].IsGoFFI || f.Uses[2].GoPath != "net/http" {
		t.Fatalf("go FFI mismatch: %+v", f.Uses[2])
	}
	if len(f.Uses[2].GoBody) != 1 {
		t.Fatalf("go body = %d", len(f.Uses[2].GoBody))
	}
}

func TestParseScript(t *testing.T) {
	src := `let args = envArgs()
let name = args.get(1) ?? "world"
println("hello, {name}")`
	f := parseOrFatal(t, src)
	if !f.IsScript() {
		t.Fatal("want script")
	}
	if len(f.Stmts) != 3 {
		t.Fatalf("stmts = %d", len(f.Stmts))
	}
}

func TestParseStringInterpolation(t *testing.T) {
	src := `fn f() { let s = "hi, {name}!" }`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	sl := body.Stmts[0].(*ast.LetStmt).Value.(*ast.StringLit)
	if len(sl.Parts) != 3 {
		t.Fatalf("parts = %d", len(sl.Parts))
	}
	if !sl.Parts[0].IsLit || sl.Parts[0].Lit != "hi, " {
		t.Fatalf("part 0 = %+v", sl.Parts[0])
	}
	if sl.Parts[1].IsLit {
		t.Fatalf("part 1 should be expr")
	}
	if id, ok := sl.Parts[1].Expr.(*ast.Ident); !ok || id.Name != "name" {
		t.Fatalf("part 1 expr = %+v", sl.Parts[1].Expr)
	}
}

func TestParseDefer(t *testing.T) {
	src := `fn f() -> Result<(), Error> {
    let h = open()?
    defer h.close()
    Ok(())
}`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	if _, ok := body.Stmts[1].(*ast.DeferStmt); !ok {
		t.Fatalf("stmt 1 = %T", body.Stmts[1])
	}
}

func TestParseBreakContinue(t *testing.T) {
	src := `fn f() {
    for item in items {
        if !item.valid { continue }
        if item.done { break }
        process(item)
    }
}`
	parseOrFatal(t, src)
}

func TestParseUnaryChain(t *testing.T) {
	src := `fn f() { !!!x }`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	u := body.Stmts[0].(*ast.ExprStmt).X.(*ast.UnaryExpr)
	inner := u.X.(*ast.UnaryExpr).X.(*ast.UnaryExpr)
	if id, ok := inner.X.(*ast.Ident); !ok || id.Name != "x" {
		t.Fatalf("innermost = %T %+v", inner.X, inner.X)
	}
}

func TestParseIfStructLitRequiresParens(t *testing.T) {
	// The forbidden form should NOT parse as a struct literal in the if
	// head. `if Point { x: 0 } == origin { ... }` — we parse the `if`
	// head as `Point` only (not Point { ... }), so the `{ x: 0 }` is the
	// body. This will likely fail body-level parsing, but importantly, the
	// struct literal is rejected in the head.
	src := `fn f() { if (Point { x: 0, y: 0 }) == origin { doIt() } }`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	ife := body.Stmts[0].(*ast.ExprStmt).X.(*ast.IfExpr)
	// Condition must be a BinaryExpr (paren-expr == origin), not a block.
	if _, ok := ife.Cond.(*ast.BinaryExpr); !ok {
		t.Fatalf("cond = %T", ife.Cond)
	}
}

// TestParseElseOnNewLineRejected verifies v0.2 O2: `} else` across a
// newline is an error. The `}` ends the if-expression and the trailing
// `else` is orphaned.
func TestParseElseOnNewLineRejected(t *testing.T) {
	src := `fn f() -> Int {
    if cond {
        1
    }
    else {
        2
    }
}`
	_, errs := Parse([]byte(src))
	if len(errs) == 0 {
		t.Fatal("expected parse error for `} else` across newline (v0.2 O2)")
	}
}

// TestParseElseSameLine is the supported form: `} else {` on the same line.
func TestParseElseSameLine(t *testing.T) {
	src := `fn f() -> Int {
    if cond {
        1
    } else {
        2
    }
}`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	ife := body.Stmts[0].(*ast.ExprStmt).X.(*ast.IfExpr)
	if ife.Else == nil {
		t.Fatal("else branch missing on same line")
	}
}

// TestParseChanSend covers the channel-send statement from §8.5.
func TestParseChanSend(t *testing.T) {
	src := `fn f(ch: Chan<Int>, v: Int) {
    ch <- v
    ch <- computeValue()
}`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	if len(body.Stmts) != 2 {
		t.Fatalf("stmts = %d", len(body.Stmts))
	}
	s0, ok := body.Stmts[0].(*ast.ChanSendStmt)
	if !ok {
		t.Fatalf("stmt 0 = %T", body.Stmts[0])
	}
	if id, ok := s0.Channel.(*ast.Ident); !ok || id.Name != "ch" {
		t.Fatalf("chan = %+v", s0.Channel)
	}
	if _, ok := body.Stmts[1].(*ast.ChanSendStmt); !ok {
		t.Fatalf("stmt 1 = %T", body.Stmts[1])
	}
}

// TestParseStructuredConcurrency covers the §8.1 taskGroup example
// verbatim from the spec.
func TestParseStructuredConcurrency(t *testing.T) {
	src := `fn f() -> Result<(Int, Int, Int), Error> {
    taskGroup(|g| {
        let h1 = g.spawn(|| fetchA())
        let h2 = g.spawn(|| fetchB())
        let h3 = g.spawn(|| fetchC())
        Ok((h1.join()?, h2.join()?, h3.join()?))
    })
}`
	parseOrFatal(t, src)
}

// TestParseSelect covers the §8.6 select example.
func TestParseSelect(t *testing.T) {
	src := `fn f() {
    thread.select(|s| {
        s.recv(ch1, |x| handle1(x))
        s.recv(ch2, |x| handle2(x))
        s.send(out, value, || sent())
        s.timeout(5.s, || giveUp())
        s.default(|| nonBlocking())
    })
}`
	parseOrFatal(t, src)
}

// TestParseTestingSyntax covers the §11 testing example.
func TestParseTestingSyntax(t *testing.T) {
	src := `use std.testing

fn testLoginSuccess() {
    let result = login("alice", "valid_pass")
    testing.assert(result.isOk())
}

fn testLoginRejectsBlankUser() {
    let result = login("", "anything")
    testing.assertEq(result, Err(InvalidInput))
}

fn testAdd() {
    let cases = [(1, 2, 3), (0, 0, 0), (-1, -1, -2)]
    for (i, (a, b, expected)) in cases.enumerate() {
        testing.context("case {i}: add({a}, {b})", || {
            testing.assertEq(add(a, b), expected)
        })
    }
}`
	f := parseOrFatal(t, src)
	if len(f.Decls) != 3 {
		t.Fatalf("decls = %d", len(f.Decls))
	}
}

// TestParseDocComment ensures `///` doc comments attach to the following
// declaration and detach across blank lines.
func TestParseDocComment(t *testing.T) {
	src := `/// Connects to the server.
/// Returns Ok on success.
pub fn connect(host: String) -> Result<Conn, Error> {
    doIt()
}

/// Unrelated — separated by blank line.

pub fn unrelated() {}

/// A type.
pub struct User {
    /// The name.
    pub name: String,
}`
	f := parseOrFatal(t, src)
	fd := f.Decls[0].(*ast.FnDecl)
	want := "Connects to the server.\nReturns Ok on success."
	if fd.DocComment != want {
		t.Fatalf("fn connect doc = %q; want %q", fd.DocComment, want)
	}
	// Second fn: blank line between doc and decl → no doc attached.
	fd2 := f.Decls[1].(*ast.FnDecl)
	if fd2.DocComment != "" {
		t.Fatalf("fn unrelated doc should be empty, got %q", fd2.DocComment)
	}
	sd := f.Decls[2].(*ast.StructDecl)
	if sd.DocComment != "A type." {
		t.Fatalf("struct doc = %q", sd.DocComment)
	}
}

// TestParseUseGoAlias covers the §12.1 FFI alias form.
func TestParseUseGoAlias(t *testing.T) {
	src := `use go "github.com/foo/bar" as bar {
    fn DoThing(x: Int) -> Int
}
fn main() {}`
	f := parseOrFatal(t, src)
	if len(f.Uses) != 1 {
		t.Fatalf("uses = %d", len(f.Uses))
	}
	u := f.Uses[0]
	if !u.IsGoFFI {
		t.Fatal("not FFI")
	}
	if u.Alias != "bar" {
		t.Fatalf("alias = %q", u.Alias)
	}
	if u.GoPath != "github.com/foo/bar" {
		t.Fatalf("goPath = %q", u.GoPath)
	}
}

// TestParseUseGoRejectsFnType covers §12.7: closures / function-typed
// parameters cannot cross the FFI boundary. The parser emits
// CodeUseGoUnsupported when a `use go` fn signature uses `fn(...) -> T`
// for either a parameter or its return type.
func TestParseUseGoRejectsFnType(t *testing.T) {
	src := `use go "pkg" {
    fn OnTick(cb: fn(Int) -> Int) -> Int
    fn Factory(x: Int) -> fn(Int) -> Int
}
fn main() {}`
	_, diags := ParseDiagnostics([]byte(src))
	var seen int
	for _, d := range diags {
		if d.Code == "E0103" {
			seen++
		}
	}
	if seen < 2 {
		t.Fatalf("expected 2+ E0103 diagnostics (one per fn-typed slot); got %d\ndiags: %+v",
			seen, diags)
	}
}

// TestParseUseGoRejectsChannel covers §12.5/§12.7: Go channels obtained
// via FFI do not integrate with Osty's structured concurrency, so
// channel-typed parameters or return types inside a `use go` block
// must be rejected.
func TestParseUseGoRejectsChannel(t *testing.T) {
	src := `use go "pkg" {
    fn Consume(ch: Channel<Int>) -> Int
    fn Produce(x: Int) -> Channel<Int>
}
fn main() {}`
	_, diags := ParseDiagnostics([]byte(src))
	var seen int
	for _, d := range diags {
		if d.Code == "E0103" {
			seen++
		}
	}
	if seen < 2 {
		t.Fatalf("expected 2+ E0103 diagnostics (one per channel-typed slot); got %d\ndiags: %+v",
			seen, diags)
	}
}

// TestParseNilCoalescingVsLogical verifies v0.2 R1: `??` is the LOWEST
// non-assignment precedence, right-associative — so `a || b ?? c` parses
// as `(a || b) ?? c`.
func TestParseNilCoalescingVsLogical(t *testing.T) {
	src := `fn f() { a || b ?? c }`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	outer := body.Stmts[0].(*ast.ExprStmt).X.(*ast.BinaryExpr)
	if outer.Op != token.QQ {
		t.Fatalf("outer op = %s; want ??", outer.Op)
	}
	left := outer.Left.(*ast.BinaryExpr)
	if left.Op != token.OR {
		t.Fatalf("left op = %s; want ||", left.Op)
	}
}

// TestParseNilCoalescingRightAssoc verifies v0.2 R1 right-associativity:
// `a ?? b ?? c` parses as `a ?? (b ?? c)`.
func TestParseNilCoalescingRightAssoc(t *testing.T) {
	src := `fn f() { a ?? b ?? c }`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	outer := body.Stmts[0].(*ast.ExprStmt).X.(*ast.BinaryExpr)
	if outer.Op != token.QQ {
		t.Fatalf("outer op = %s; want ??", outer.Op)
	}
	if id, ok := outer.Left.(*ast.Ident); !ok || id.Name != "a" {
		t.Fatalf("left = %T %+v; want Ident(a)", outer.Left, outer.Left)
	}
	right := outer.Right.(*ast.BinaryExpr)
	if right.Op != token.QQ {
		t.Fatalf("right op = %s; want nested ??", right.Op)
	}
}

// TestParseComparisonNonAssoc verifies v0.2 R1: comparison operators are
// non-associative — `a < b < c` requires parens.
func TestParseComparisonNonAssoc(t *testing.T) {
	expectCode(t, `fn f() { a < b < c }`, diag.CodeNonAssocChain)
}

// TestParseRangeNonAssoc verifies v0.2 R1: range operators are
// non-associative.
func TestParseRangeNonAssoc(t *testing.T) {
	expectCode(t, `fn f() { let r = a..b..c }`, diag.CodeNonAssocChain)
}

// TestParseTurbofishStrict verifies v0.2 O6: `::` must be followed by `<`.
func TestParseTurbofishStrict(t *testing.T) {
	expectCode(t, `fn f() { let x = foo::bar() }`, diag.CodeTurbofishMissingLT)
}

// TestParseUseGoNoBody verifies v0.2 R16/R17: function declarations
// inside a `use go` block must NOT have a body.
func TestParseUseGoNoBody(t *testing.T) {
	src := `use go "net/http" {
    fn Get(u: String) -> String { "x" }
}
fn main() {}`
	expectCode(t, src, diag.CodeUseGoFnHasBody)
}

// TestParseDefaultExprRestricted verifies v0.2 R18: parameter defaults
// must be restricted literal forms — arbitrary expressions are rejected.
func TestParseDefaultExprRestricted(t *testing.T) {
	expectCode(t,
		`fn connect(host: String, timeout: Int = compute()) {}`,
		diag.CodeDefaultExprNotLiteral)
}

// TestParseDefaultExprAccepted verifies the literal forms are accepted.
func TestParseDefaultExprAccepted(t *testing.T) {
	src := `fn connect(host: String, port: Int = 80, timeout: Int = -1, retries: List<Int> = [], headers: Map<String, String> = {:}, opt: Result<Int, Error> = Ok(0)) {}`
	parseOrFatal(t, src)
}

// TestParseClosureRetTypeRequiresBlock verifies v0.2 R25: a closure with
// an explicit return type must have a block body.
func TestParseClosureRetTypeRequiresBlock(t *testing.T) {
	expectCode(t,
		`fn f() { let g = |x: Int| -> Int x * 2 }`,
		diag.CodeClosureRetReqBlock)
}

// TestParseUppercaseBaseRejected verifies v0.2 R11: uppercase base
// prefixes are rejected.
func TestParseUppercaseBaseRejected(t *testing.T) {
	expectCode(t, `fn f() { let x = 0X1F }`, diag.CodeUppercaseBasePrefix)
}

// TestParseUsePathMixingRejected verifies v0.2 R15: a `.IDENT` segment
// after a `/IDENT` is rejected.
func TestParseUsePathMixingRejected(t *testing.T) {
	expectCode(t,
		`use a/b.c
fn main() {}`,
		diag.CodeUsePathMixed)
}

// TestParseAnnotation verifies v0.2 R26 / O1: `#[json(...)]` and
// `#[deprecated(...)]` are accepted, others are rejected.
func TestParseAnnotation(t *testing.T) {
	src := `#[deprecated(since = "0.5", use = "newConnect", message = "use newConnect instead")]
pub fn oldConnect(host: String) -> Result<Conn, Error> {
    newConnect(host)
}

pub struct User {
    #[json(key = "user_name")]
    pub name: String,
    #[json(skip)]
    internal: Int = 0,
}`
	f := parseOrFatal(t, src)
	fd := f.Decls[0].(*ast.FnDecl)
	if len(fd.Annotations) != 1 || fd.Annotations[0].Name != "deprecated" {
		t.Fatalf("fn annotation = %+v", fd.Annotations)
	}
	sd := f.Decls[1].(*ast.StructDecl)
	if len(sd.Fields) != 2 {
		t.Fatalf("fields = %d", len(sd.Fields))
	}
	if len(sd.Fields[0].Annotations) != 1 || sd.Fields[0].Annotations[0].Name != "json" {
		t.Fatalf("field 0 ann = %+v", sd.Fields[0].Annotations)
	}
	// Check key=value arg parsed.
	a := sd.Fields[0].Annotations[0]
	if len(a.Args) != 1 || a.Args[0].Key != "key" {
		t.Fatalf("json args = %+v", a.Args)
	}
}

// TestParseUnknownAnnotationRejected verifies unknown names error.
func TestParseUnknownAnnotationRejected(t *testing.T) {
	expectCode(t,
		`#[inline]
pub fn fast() {}`,
		diag.CodeUnknownAnnotation)
}

// TestParseMultilineMethodChain verifies v0.2 R2 case 2: a leading `.`
// suppresses the preceding newline so method chains continue across lines.
func TestParseMultilineMethodChain(t *testing.T) {
	src := `fn f() {
    let result = iter.from(xs)
        .map(|x| x * 2)
        .filter(|x| x > 10)
        .toList()
}`
	parseOrFatal(t, src)
}

// TestParseTrailingDotRejected verifies v0.2 O3: a trailing `.` (then
// newline, then continuation) is a syntax error — leading `.` is the
// supported style.
func TestParseTrailingDotRejected(t *testing.T) {
	// `xs.` followed by NEWLINE: the `.` should NOT suppress the trailing
	// newline in the lexer (since `.` is in the "previous token" suppress
	// list per R2 case 1, but per O3, `.` and `?.` are EXCLUDED). So
	// `xs.\n map(...)` parses as two separate statements: `xs.` (error)
	// and `map(...)`.
	src := `fn f() {
    xs.
        map(|x| x)
}`
	_, errs := Parse([]byte(src))
	if len(errs) == 0 {
		t.Fatal("expected at least one error for trailing `.` (v0.2 O3)")
	}
}

// TestParseEndVAccurate spot-checks that node EndV points at the end of
// the last token the node owns, not the start of the following one.
func TestParseEndVAccurate(t *testing.T) {
	src := `fn add(a: Int, b: Int) -> Int { a + b }`
	f := parseOrFatal(t, src)
	fd := f.Decls[0].(*ast.FnDecl)
	// The function spans columns 1..end of closing `}`. With the old
	// off-by-one, EndV sat at the EOF position (column 1 of line 2).
	if fd.End().Line != 1 {
		t.Fatalf("fn End line = %d; want 1 (was off-by-one into next line)", fd.End().Line)
	}
	// Column should be past the closing `}` — i.e. > 39 (length of source).
	if fd.End().Column < 39 {
		t.Fatalf("fn End col = %d; want >= 39", fd.End().Column)
	}
}

// TestErrorRecovery verifies that one malformed decl does not cascade —
// following valid declarations still parse successfully.
func TestErrorRecovery(t *testing.T) {
	src := `fn good1() { 1 }
fn broken( { }
fn good2() { 2 }`
	file, errs := Parse([]byte(src))
	if len(errs) == 0 {
		t.Fatal("expected parse errors for malformed fn")
	}
	// Despite errors, good2 should still appear in Decls.
	names := map[string]bool{}
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FnDecl); ok {
			names[fd.Name] = true
		}
	}
	if !names["good1"] {
		t.Error("good1 not parsed")
	}
	if !names["good2"] {
		t.Error("good2 not parsed after error recovery")
	}
}

// TestParseClosurePatternParam covers SPEC_GAPS G4: closure parameters
// may be destructuring patterns, e.g. `|(k, v)| ...`.
func TestParseClosurePatternParam(t *testing.T) {
	src := `fn f() {
    let entries = m.entries()
    entries.map(|(k, v)| k + v)
    entries.map(|(_, v)| v)
}`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	c0 := body.Stmts[1].(*ast.ExprStmt).X.(*ast.CallExpr)
	cl := c0.Args[0].Value.(*ast.ClosureExpr)
	if len(cl.Params) != 1 {
		t.Fatalf("params = %d", len(cl.Params))
	}
	if cl.Params[0].Pattern == nil {
		t.Fatal("expected destructuring pattern, got nil")
	}
	if _, ok := cl.Params[0].Pattern.(*ast.TuplePat); !ok {
		t.Fatalf("pattern = %T", cl.Params[0].Pattern)
	}
}

func TestParseRangeInMatch(t *testing.T) {
	src := `fn f() -> String {
    match n {
        0..=9 -> "single",
        10..=99 -> "two",
        x @ 100..=999 -> "three",
        _ -> "more",
    }
}`
	f := parseOrFatal(t, src)
	body := f.Decls[0].(*ast.FnDecl).Body
	m := body.Stmts[0].(*ast.ExprStmt).X.(*ast.MatchExpr)
	if _, ok := m.Arms[0].Pattern.(*ast.RangePat); !ok {
		t.Fatalf("arm 0 = %T", m.Arms[0].Pattern)
	}
	if _, ok := m.Arms[2].Pattern.(*ast.BindingPat); !ok {
		t.Fatalf("arm 2 = %T", m.Arms[2].Pattern)
	}
}
