package resolve

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
)

// expectCode runs the parser + resolver on src and asserts that at least
// one diagnostic carries the given code. Unlike a bare `len(diags) > 0`
// check, this fails when the pipeline rejects the input for the WRONG
// reason.
func expectCode(t *testing.T, src, code string) {
	t.Helper()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	res := File(file, NewPrelude())
	all := append(append([]*diag.Diagnostic{}, parseDiags...), res.Diags...)
	for _, d := range all {
		if d.Code == code {
			return
		}
	}
	var got []string
	for _, d := range all {
		got = append(got, d.Code+": "+d.Error())
	}
	t.Fatalf("expected diagnostic with code %q; got:\n  %s\nsource:\n%s",
		code, strings.Join(got, "\n  "), src)
}

// expectNoDiag asserts the pipeline accepts src cleanly.
func expectNoDiag(t *testing.T, src string) {
	t.Helper()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	res := File(file, NewPrelude())
	all := append(append([]*diag.Diagnostic{}, parseDiags...), res.Diags...)
	if len(all) == 0 {
		return
	}
	var got []string
	for _, d := range all {
		got = append(got, d.Error())
	}
	t.Fatalf("expected clean parse+resolve; got:\n  %s\nsource:\n%s",
		strings.Join(got, "\n  "), src)
}

// ---- Phase 6 tests: context-aware control flow ----

func TestBreakOutsideLoopInFn(t *testing.T) {
	expectCode(t, `fn f() { break }`, diag.CodeBreakOutsideLoop)
}

func TestContinueOutsideLoopInFn(t *testing.T) {
	expectCode(t, `fn f() { continue }`, diag.CodeContinueOutsideLoop)
}

func TestBreakInLoopOk(t *testing.T) {
	expectNoDiag(t, `fn f() {
    for i in 0..10 {
        if i > 5 { break }
    }
}`)
}

func TestReturnOutsideFnScript(t *testing.T) {
	// Scripts wrap their top-level in an implicit fn, so return is OK.
	expectNoDiag(t, `let x = 5
return`)
}

func TestDeferInFn(t *testing.T) {
	expectNoDiag(t, `fn f() {
    let h = 1
    defer println("{h}")
}`)
}

func TestNestedLoopDepth(t *testing.T) {
	// `break` inside the inner for should be allowed; a trailing bare
	// break outside both loops would be an error.
	expectNoDiag(t, `fn f() {
    for i in 0..3 {
        for j in 0..3 {
            break
        }
        continue
    }
}`)
}

func TestClosureBreakIsolated(t *testing.T) {
	// Closure nested inside a loop should NOT see that loop's break
	// context. `break` is a statement in Osty, so we test via a
	// closure with a block body.
	expectCode(t, `fn f() {
    for i in 0..3 {
        let go = || { break }
    }
}`, diag.CodeBreakOutsideLoop)
}

// ---- Phase 7 tests: duplicate detection ----

func TestDuplicateField(t *testing.T) {
	expectCode(t, `pub struct X {
    a: Int,
    a: String,
}`, diag.CodeDuplicateDecl)
}

func TestDuplicateMethod(t *testing.T) {
	expectCode(t, `pub struct X {
    a: Int,
    pub fn foo(self) {}
    pub fn foo(self) {}
}`, diag.CodeDuplicateDecl)
}

func TestDuplicateVariant(t *testing.T) {
	expectCode(t, `pub enum E {
    A,
    A(Int),
}`, diag.CodeDuplicateDecl)
}

func TestDuplicateVariantJSONTag(t *testing.T) {
	expectCode(t, `pub enum E {
    #[json(key = "same")]
    A,
    #[json(key = "same")]
    B,
}`, diag.CodeDuplicateDecl)
}

func TestDuplicateEnumMethod(t *testing.T) {
	expectCode(t, `pub enum E {
    A,
    pub fn foo(self) {}
    pub fn foo(self) {}
}`, diag.CodeDuplicateDecl)
}

// ---- Phase 8 tests: or-pattern binding consistency ----

func TestOrPatternSameBindingsOk(t *testing.T) {
	// `r` bound by both alternatives — valid per §4.3.1.
	expectNoDiag(t, `pub enum Shape {
    Circle(Float),
    Rect(Float, Float),
}
fn area(s: Shape) -> Float {
    match s {
        Circle(r) | Rect(r, _) -> r,
    }
}`)
}

func TestOrPatternInconsistentBindings(t *testing.T) {
	expectCode(t, `pub enum E {
    A(Int),
    B(Int, Int),
}
fn f(e: E) -> Int {
    match e {
        A(x) | B(x, y) -> x,
    }
}`, diag.CodeOrPatternBindingMismatch)
}

// ---- Phase 9 tests: interface default + annotation targets ----

func TestInterfaceDefaultFieldAccessRejected(t *testing.T) {
	expectCode(t, `pub interface Greeter {
    fn greet(self) -> String {
        self.name
    }
}`, diag.CodeInterfaceDefaultField)
}

func TestInterfaceDefaultMethodCallOk(t *testing.T) {
	// Calling another interface method on self is fine.
	expectNoDiag(t, `pub interface Greeter {
    fn name(self) -> String
    fn greet(self) -> String {
        self.name()
    }
}`)
}

func TestInterfaceNoDefaultBodyNoRestriction(t *testing.T) {
	expectNoDiag(t, `pub interface I {
    fn m(self) -> Int
}`)
}

func TestJsonAnnotationOnFnRejected(t *testing.T) {
	expectCode(t, `#[json]
pub fn f() {}`, diag.CodeAnnotationBadTarget)
}

func TestDeprecatedAnnotationOnFieldRejected(t *testing.T) {
	expectCode(t, `pub struct U {
    #[deprecated]
    pub name: String,
}`, diag.CodeAnnotationBadTarget)
}

func TestJsonAnnotationOnFieldOk(t *testing.T) {
	expectNoDiag(t, `pub struct U {
    #[json(key = "user_name")]
    pub name: String,
}`)
}

func TestDeprecatedAnnotationOnFnOk(t *testing.T) {
	expectNoDiag(t, `#[deprecated(since = "0.5")]
pub fn oldApi() {}`)
}

func TestDeprecatedAnnotationOnMethodOk(t *testing.T) {
	expectNoDiag(t, `pub struct U {
    #[deprecated]
    pub fn old(self) {}
}`)
}

// ---- Phase 10 tests: wildcard in expression ----

func TestWildcardInExpressionRejected(t *testing.T) {
	expectCode(t, `fn f() { let x = _ }`, diag.CodeWildcardInExpr)
}

func TestWildcardInPatternOk(t *testing.T) {
	// `_` as a pattern is fine.
	expectNoDiag(t, `fn f() {
    let (_, b) = (1, 2)
    println("{b}")
}`)
}

// ---- v0.3 rule tests ----

// TestSurrogateCodePointRejected covers v0.3 §2.1: surrogate code points
// (U+D800..U+DFFF) are not representable.
func TestSurrogateCodePointRejected(t *testing.T) {
	expectCode(t, `fn f() { let c = '\u{D800}' }`, diag.CodeUnknownEscape)
}

// TestAnnotationOnUseRejected covers v0.3 §18.1: annotations are not
// allowed on `use` statements.
func TestAnnotationOnUseRejected(t *testing.T) {
	expectCode(t,
		`#[deprecated]
use std.fs`,
		diag.CodeAnnotationBadTarget)
}

// TestTopLevelDeferInScriptRejected covers v0.3 §6 / §18.3: bare defer at
// the top level of a script is a compile error.
func TestTopLevelDeferInScriptRejected(t *testing.T) {
	expectCode(t,
		`let x = 1
defer println("{x}")`,
		diag.CodeDeferAtScriptTop)
}

// TestDeferInFnBodyOk confirms defer inside an explicit fn body is still
// accepted after tightening the top-level rule.
func TestDeferInFnBodyOk(t *testing.T) {
	expectNoDiag(t, `fn main() {
    let x = 1
    defer println("{x}")
}`)
}

// TestDuplicateAnnotationName covers v0.3 §18.1: the same annotation
// name cannot appear more than once on a single target.
func TestDuplicateAnnotationName(t *testing.T) {
	expectCode(t,
		`#[deprecated]
#[deprecated]
pub fn f() {}`,
		diag.CodeDuplicateAnnotation)
}

// TestDifferentAnnotationNamesOk confirms distinct annotations on one
// target are still accepted (when both kinds apply).
func TestDifferentAnnotationNamesOk(t *testing.T) {
	// Only `deprecated` is currently allowed on top-level fns, so this
	// test ensures a single occurrence doesn't produce E0609.
	expectNoDiag(t, `#[deprecated(since = "0.5")]
pub fn f() {}`)
}

// ---- Miscellaneous cross-phase correctness ----
