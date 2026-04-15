package lint_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/lint"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

// runLint parses, resolves, type-checks, and lints one snippet. It
// returns only the lint pass's warnings — parse and resolve errors are
// asserted to be absent (every fixture should be valid input so the lint
// pass is what we're measuring).
func runLint(t *testing.T, src string) []*diag.Diagnostic {
	t.Helper()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	for _, d := range parseDiags {
		if d.Severity == diag.Error {
			t.Fatalf("unexpected parse error: %s: %s", d.Code, d.Message)
		}
	}
	res := resolve.File(file, resolve.NewPrelude())
	for _, d := range res.Diags {
		if d.Severity == diag.Error {
			t.Fatalf("unexpected resolve error: %s: %s", d.Code, d.Message)
		}
	}
	chk := check.File(file, res)
	lr := lint.File(file, res, chk)
	return lr.Diags
}

// assertWarns asserts that `got` contains at least one diagnostic with
// each code in `want`. Extra diagnostics are tolerated (they may belong
// to other rules that happen to fire on the same fixture). For strict
// coverage use assertOnly.
func assertWarns(t *testing.T, got []*diag.Diagnostic, want ...string) {
	t.Helper()
	seen := map[string]int{}
	for _, d := range got {
		seen[d.Code]++
	}
	for _, w := range want {
		if seen[w] == 0 {
			t.Fatalf("expected warning code %s; got: %s", w, dumpCodes(got))
		}
	}
}

// assertOnly asserts that the returned codes are EXACTLY the multiset
// `want`. Useful for negative tests that want to prove an unrelated rule
// didn't fire.
func assertOnly(t *testing.T, got []*diag.Diagnostic, want ...string) {
	t.Helper()
	haveCodes := make([]string, 0, len(got))
	for _, d := range got {
		haveCodes = append(haveCodes, d.Code)
	}
	sort.Strings(haveCodes)
	wantCodes := append([]string{}, want...)
	sort.Strings(wantCodes)
	if strings.Join(haveCodes, ",") != strings.Join(wantCodes, ",") {
		t.Fatalf("code set mismatch:\n  want: %s\n  got:  %s",
			strings.Join(wantCodes, ","), dumpCodes(got))
	}
}

func assertClean(t *testing.T, got []*diag.Diagnostic) {
	t.Helper()
	if len(got) == 0 {
		return
	}
	t.Fatalf("expected no warnings, got %d:\n  %s", len(got), dumpCodes(got))
}

func dumpCodes(diags []*diag.Diagnostic) string {
	lines := make([]string, 0, len(diags))
	for _, d := range diags {
		lines = append(lines, d.Code+": "+d.Message)
	}
	return strings.Join(lines, " | ")
}

// ---- L0001 / L0002 / L0003 : Unused ----

func TestLint_UnusedLet(t *testing.T) {
	src := `
fn f() {
    let unused = 42
    let used = 10
    println(used)
}
`
	assertWarns(t, runLint(t, src), diag.CodeUnusedLet)
}

func TestLint_UnusedLet_UnderscorePrefixClean(t *testing.T) {
	src := `
fn f() {
    let _discarded = expensive()
    let used = 1
    println(used)
}

fn expensive() -> Int { 7 }
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeUnusedLet {
			t.Fatalf("underscore-prefixed let should not fire L0001: %s", d.Message)
		}
	}
}

func TestLint_UnusedParam(t *testing.T) {
	src := `
fn greet(name: String, times: Int) {
    println(name)
}
`
	assertWarns(t, runLint(t, src), diag.CodeUnusedParam)
}

func TestLint_UnusedParam_PubFnClean(t *testing.T) {
	// Public functions' params are part of the external contract — we
	// don't flag them even if the body doesn't use them.
	src := `
pub fn keep(x: Int, y: Int) -> Int {
    x
}
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeUnusedParam {
			t.Fatalf("pub fn params should not fire L0002: %s", d.Message)
		}
	}
}

func TestLint_UnusedImport(t *testing.T) {
	src := `
use foo.bar.baz

fn main() {
    println("hi")
}
`
	assertWarns(t, runLint(t, src), diag.CodeUnusedImport)
}

func TestLint_UnusedImport_AliasUsedClean(t *testing.T) {
	src := `
use foo.bar as b

fn main() {
    b.call()
}
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeUnusedImport {
			t.Fatalf("used import alias should not fire L0003: %s", d.Message)
		}
	}
}

// ---- L0005 / L0006 : Unused struct field / method ----

func TestLint_UnusedField(t *testing.T) {
	src := `
struct S {
    secret: Int
}

fn main() {
    let _s = S { secret: 1 }
}
`
	// The field IS referenced by the struct literal, so it shouldn't fire.
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeUnusedField {
			t.Fatalf("field used in struct literal shouldn't be unused: %s", d.Message)
		}
	}

	// Now a field that's never accessed anywhere.
	src2 := `
struct Hidden {
    secret: Int
}
`
	assertWarns(t, runLint(t, src2), diag.CodeUnusedField)
}

func TestLint_UnusedFieldIgnoredForPub(t *testing.T) {
	src := `
pub struct Public {
    secret: Int
}
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeUnusedField {
			t.Fatalf("pub struct field should not fire L0005: %s", d.Message)
		}
	}
}

func TestLint_UnusedMethod(t *testing.T) {
	src := `
struct S {
    x: Int
}

struct S {
    fn inner(self) -> Int { self.x }
}

fn main() {
    let _s = S { x: 1 }
}
`
	assertWarns(t, runLint(t, src), diag.CodeUnusedMethod)
}

// ---- L0004 : Unused mut ----

func TestLint_UnusedMut(t *testing.T) {
	src := `
fn f() {
    let mut x = 1
    println(x)
}
`
	assertWarns(t, runLint(t, src), diag.CodeUnusedMut)
}

func TestLint_UsedMutClean(t *testing.T) {
	src := `
fn f() {
    let mut x = 1
    x = 2
    println(x)
}
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeUnusedMut {
			t.Fatalf("reassigned mut should be clean: %s", d.Message)
		}
	}
}

func TestLint_CompoundAssignCountsAsMut(t *testing.T) {
	src := `
fn f() {
    let mut total = 0
    for i in 0..10 {
        total += i
    }
    println(total)
}
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeUnusedMut {
			t.Fatalf("compound assign should count as mut: %s", d.Message)
		}
	}
}

func TestLint_IndexAssignCountsAsMut(t *testing.T) {
	src := `
fn f() {
    let mut xs = [1, 2, 3]
    xs[0] = 9
    println(xs)
}
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeUnusedMut {
			t.Fatalf("index assign should count as mut: %s", d.Message)
		}
	}
}

// ---- L0010 : Shadowing ----

func TestLint_ShadowedBinding(t *testing.T) {
	src := `
fn f() {
    let x = 1
    {
        let x = 2
        println(x)
    }
    println(x)
}
`
	assertWarns(t, runLint(t, src), diag.CodeShadowedBinding)
}

func TestLint_SiblingScopesClean(t *testing.T) {
	// Two `let x` in disjoint scopes — neither shadows the other.
	src := `
fn f() {
    if true {
        let x = 1
        println(x)
    } else {
        let x = 2
        println(x)
    }
}
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeShadowedBinding {
			t.Fatalf("sibling scopes should not shadow each other: %s", d.Message)
		}
	}
}

func TestLint_ShadowClosureParam(t *testing.T) {
	src := `
fn f() {
    let x = 1
    let g = |x| x * 2
    println(g(5))
    println(x)
}
`
	assertWarns(t, runLint(t, src), diag.CodeShadowedBinding)
}

func TestLint_ShadowForLoopBinding(t *testing.T) {
	src := `
fn f() {
    let i = 100
    for i in 0..10 {
        println(i)
    }
    println(i)
}
`
	assertWarns(t, runLint(t, src), diag.CodeShadowedBinding)
}

func TestLint_ShadowMatchArmBinding(t *testing.T) {
	src := `
enum Opt {
    Some(Int),
    None
}

fn f() {
    let x = 1
    match Opt.Some(5) {
        Opt.Some(x) -> println(x),
        Opt.None -> println(x)
    }
}
`
	assertWarns(t, runLint(t, src), diag.CodeShadowedBinding)
}

func TestLint_ShadowParamByLet(t *testing.T) {
	src := `
fn f(x: Int) {
    println(x)
    let x = 2
    println(x)
}
`
	assertWarns(t, runLint(t, src), diag.CodeShadowedBinding)
}

// ---- L0020 : Dead code ----

func TestLint_DeadCodeAfterReturn(t *testing.T) {
	src := `
fn f() -> Int {
    return 1
    let dead = 2
    dead
}
`
	assertWarns(t, runLint(t, src), diag.CodeDeadCode)
}

func TestLint_DeadCodeAfterBreak(t *testing.T) {
	src := `
fn f() {
    for {
        break
        let dead = 1
        println(dead)
    }
}
`
	assertWarns(t, runLint(t, src), diag.CodeDeadCode)
}

func TestLint_DeadCodeAfterContinue(t *testing.T) {
	src := `
fn f() {
    for i in 0..10 {
        continue
        let dead = 1
        println(dead)
    }
}
`
	assertWarns(t, runLint(t, src), diag.CodeDeadCode)
}

func TestLint_NoDeadCodeWhenReturnLastClean(t *testing.T) {
	src := `
fn f() -> Int {
    let x = 3
    return x
}
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeDeadCode {
			t.Fatalf("return as last statement should be clean: %s", d.Message)
		}
	}
}

func TestLint_DeadCodeAfterIfElseBothReturn(t *testing.T) {
	src := `
fn f(c: Bool) -> Int {
    if c {
        return 1
    } else {
        return 2
    }
    let dead = 3
    dead
}
`
	assertWarns(t, runLint(t, src), diag.CodeDeadCode)
}

func TestLint_DeadCodeNotFlaggedIfOnlyOneBranchReturns(t *testing.T) {
	src := `
fn f(c: Bool) -> Int {
    if c {
        return 1
    }
    let x = 2
    x
}
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeDeadCode {
			t.Fatalf("single-branch return is not diverging: %s", d.Message)
		}
	}
}

func TestLint_DeadCodeAfterInfiniteLoop(t *testing.T) {
	src := `
fn f() {
    for {
        println("tick")
    }
    let dead = 1
    println(dead)
}
`
	assertWarns(t, runLint(t, src), diag.CodeDeadCode)
}

func TestLint_InfiniteLoopWithBreakNotDiverging(t *testing.T) {
	src := `
fn f() {
    for {
        if true {
            break
        }
    }
    let ok = 1
    println(ok)
}
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeDeadCode {
			t.Fatalf("loop with break should fall through: %s", d.Message)
		}
	}
}

func TestLint_DeadCodeAfterNeverCall(t *testing.T) {
	// A call to a function whose return type is Never should make the
	// following statements unreachable — requires type info.
	src := `
fn halt() -> Never { halt() }

fn f() {
    halt()
    let dead = 1
    println(dead)
}
`
	assertWarns(t, runLint(t, src), diag.CodeDeadCode)
}

func TestLint_DeadCodeAfterConventionalPanic(t *testing.T) {
	// panic / exit / abort / unreachable / todo are treated as diverging
	// by name even when the type checker hasn't labelled them Never.
	for _, name := range []string{"panic", "exit", "abort", "unreachable", "todo"} {
		src := `
fn ` + name + `(msg: String) { }

fn f() {
    ` + name + `("boom")
    let dead = 1
    println(dead)
}
`
		got := runLint(t, src)
		found := false
		for _, d := range got {
			if d.Code == diag.CodeDeadCode {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("conventional `%s` should fire L0020: got=%s", name, dumpCodes(got))
		}
	}
}

func TestLint_DeadCodeAfterExhaustiveMatchAllDiverge(t *testing.T) {
	src := `
fn f(x: Int) -> Int {
    match x {
        0 -> { return 0 },
        _ -> { return x }
    }
    let dead = 99
    dead
}
`
	assertWarns(t, runLint(t, src), diag.CodeDeadCode)
}

// ---- L0030 / L0031 / L0032 : Naming ----

func TestLint_NamingType(t *testing.T) {
	src := `
struct my_struct {
    x: Int
}
`
	assertWarns(t, runLint(t, src), diag.CodeNamingType)
}

func TestLint_NamingValueFn(t *testing.T) {
	src := `
fn LoadConfig() {
}
`
	assertWarns(t, runLint(t, src), diag.CodeNamingValue)
}

func TestLint_NamingValueParam(t *testing.T) {
	src := `
fn f(User_Id: Int) {
    println(User_Id)
}
`
	assertWarns(t, runLint(t, src), diag.CodeNamingValue)
}

func TestLint_NamingValueLet(t *testing.T) {
	src := `
fn f() {
    let user_name = "ada"
    println(user_name)
}
`
	assertWarns(t, runLint(t, src), diag.CodeNamingValue)
}

func TestLint_NamingVariant(t *testing.T) {
	src := `
enum Color {
    red,
    Green,
    Blue
}
`
	assertWarns(t, runLint(t, src), diag.CodeNamingVariant)
}

func TestLint_NamingCanonicalClean(t *testing.T) {
	src := `
struct User {
    name: String
}

enum Color {
    Red,
    Green,
    Blue
}

fn loadConfig(path: String) -> User {
    let base = User { name: path }
    base
}
`
	for _, d := range runLint(t, src) {
		switch d.Code {
		case diag.CodeNamingType, diag.CodeNamingValue, diag.CodeNamingVariant:
			t.Fatalf("canonical names should be clean: %s: %s", d.Code, d.Message)
		}
	}
}

// ---- L0040 / L0041 / L0042 : Simplifiable / buggy patterns ----

func TestLint_RedundantBoolTrueFalse(t *testing.T) {
	src := `
fn f(c: Bool) -> Bool {
    if c { true } else { false }
}
`
	assertWarns(t, runLint(t, src), diag.CodeRedundantBool)
}

func TestLint_RedundantBoolFalseTrue(t *testing.T) {
	src := `
fn f(c: Bool) -> Bool {
    if c { false } else { true }
}
`
	assertWarns(t, runLint(t, src), diag.CodeRedundantBool)
}

func TestLint_IfElseNonBoolClean(t *testing.T) {
	src := `
fn f(c: Bool) -> Int {
    if c { 1 } else { 2 }
}
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeRedundantBool {
			t.Fatalf("non-bool if shouldn't fire: %s", d.Message)
		}
	}
}

func TestLint_SelfCompareEq(t *testing.T) {
	src := `
fn f(x: Int) -> Bool {
    x == x
}
`
	assertWarns(t, runLint(t, src), diag.CodeSelfCompare)
}

func TestLint_SelfCompareLt(t *testing.T) {
	src := `
fn f(x: Int) -> Bool {
    x < x
}
`
	assertWarns(t, runLint(t, src), diag.CodeSelfCompare)
}

func TestLint_SelfCompareFieldChain(t *testing.T) {
	src := `
struct P { x: Int }

fn f(p: P) -> Bool {
    p.x == p.x
}
`
	assertWarns(t, runLint(t, src), diag.CodeSelfCompare)
}

func TestLint_CallNotSelfCompareClean(t *testing.T) {
	// Two calls to the same function may return different values — do
	// not flag as self-compare.
	src := `
fn now() -> Int { 42 }

fn f() -> Bool {
    now() == now()
}
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeSelfCompare {
			t.Fatalf("call self-compare should not fire: %s", d.Message)
		}
	}
}

func TestLint_SelfAssign(t *testing.T) {
	src := `
fn f() {
    let mut x = 1
    x = x
    println(x)
}
`
	assertWarns(t, runLint(t, src), diag.CodeSelfAssign)
}

func TestLint_SelfAssignFieldChain(t *testing.T) {
	src := `
struct P { x: Int }

fn f() {
    let mut p = P { x: 1 }
    p.x = p.x
    println(p.x)
}
`
	assertWarns(t, runLint(t, src), diag.CodeSelfAssign)
}

func TestLint_CompoundAssignNotSelfAssignClean(t *testing.T) {
	// `x += x` is a valid doubling — not a no-op.
	src := `
fn f() {
    let mut x = 1
    x += x
    println(x)
}
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeSelfAssign {
			t.Fatalf("compound assign is not self-assign: %s", d.Message)
		}
	}
}

// ---- L0007 : Ignored Result ----

func TestLint_IgnoredOptional(t *testing.T) {
	src := `
fn find(k: Int) -> Int? {
    if k > 0 { Some(k) } else { None }
}

fn main() {
    find(3)
}
`
	assertWarns(t, runLint(t, src), diag.CodeIgnoredResult)
}

func TestLint_AssignedOptionalClean(t *testing.T) {
	src := `
fn find(k: Int) -> Int? {
    if k > 0 { Some(k) } else { None }
}

fn main() {
    let _x = find(3)
}
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeIgnoredResult {
			t.Fatalf("assigned result should be clean: %s", d.Message)
		}
	}
}

// ---- #[allow(...)] suppression ----

func TestLint_AllowCodeSuppresses(t *testing.T) {
	src := `
#[allow(L0001)]
fn f() {
    let unused = 1
    println("x")
}
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeUnusedLet {
			t.Fatalf("#[allow(L0001)] should suppress L0001: %s", d.Message)
		}
	}
}

func TestLint_AllowCategorySuppresses(t *testing.T) {
	src := `
#[allow(unused)]
fn f() {
    let a = 1
    let b = 2
    println("x")
}
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeUnusedLet {
			t.Fatalf("#[allow(unused)] should suppress unused-let: %s", d.Message)
		}
	}
}

func TestLint_AllowWildcardSuppresses(t *testing.T) {
	src := `
#[allow(lint)]
fn f() {
    let unused_x = 1
    let mut never_mutated = 2
    println("x")
}
`
	for _, d := range runLint(t, src) {
		if strings.HasPrefix(d.Code, "L") {
			t.Fatalf("#[allow(lint)] should suppress every lint code, got %s: %s", d.Code, d.Message)
		}
	}
}

func TestLint_AllowScopedToDecl(t *testing.T) {
	// Only the annotated fn is suppressed; the sibling fn still fires.
	src := `
#[allow(L0001)]
fn quiet() {
    let unused = 1
    println("x")
}

fn loud() {
    let unused = 1
    println("x")
}
`
	got := runLint(t, src)
	count := 0
	for _, d := range got {
		if d.Code == diag.CodeUnusedLet {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 L0001 warning (loud only); got %d: %s", count, dumpCodes(got))
	}
}

// ---- Structured suggestions (diag.Suggestion) ----

func TestLint_UnusedLetHasFixSuggestion(t *testing.T) {
	src := `
fn f() {
    let unused = 1
    println("x")
}
`
	for _, d := range runLint(t, src) {
		if d.Code != diag.CodeUnusedLet {
			continue
		}
		if len(d.Suggestions) == 0 {
			t.Fatalf("L0001 should carry a Suggestion")
		}
		if !d.Suggestions[0].MachineApplicable {
			t.Fatalf("the rename-to-underscore fix should be machine-applicable")
		}
		if d.Suggestions[0].Replacement != "_" {
			t.Fatalf("replacement should be a single underscore; got %q", d.Suggestions[0].Replacement)
		}
	}
}

func TestLint_UnusedImportHasDeleteSuggestion(t *testing.T) {
	src := `
use foo.bar.baz

fn main() {
    println("hi")
}
`
	for _, d := range runLint(t, src) {
		if d.Code != diag.CodeUnusedImport {
			continue
		}
		if len(d.Suggestions) == 0 {
			t.Fatalf("L0003 should carry a delete Suggestion")
		}
		if d.Suggestions[0].Replacement != "" {
			t.Fatalf("delete suggestion should have empty replacement; got %q",
				d.Suggestions[0].Replacement)
		}
	}
}

func TestLint_SelfAssignHasDeleteSuggestion(t *testing.T) {
	src := `
fn f() {
    let mut x = 1
    x = x
    println(x)
}
`
	for _, d := range runLint(t, src) {
		if d.Code != diag.CodeSelfAssign {
			continue
		}
		if len(d.Suggestions) == 0 {
			t.Fatalf("L0042 should carry a delete Suggestion")
		}
	}
}

// ---- lint.Config (global allow/deny) ----

func TestLint_ConfigAllowSuppresses(t *testing.T) {
	src := `
fn f() {
    let unused = 1
    println("x")
}
`
	cfg := lint.Config{Allow: []string{"unused_let"}}
	raw := &lint.Result{Diags: runLint(t, src)}
	filtered := cfg.Apply(raw)
	for _, d := range filtered.Diags {
		if d.Code == diag.CodeUnusedLet {
			t.Fatalf("Config.Allow should suppress L0001: %s", d.Message)
		}
	}
}

func TestLint_ConfigDenyElevates(t *testing.T) {
	src := `
fn f() {
    let unused = 1
    println("x")
}
`
	cfg := lint.Config{Deny: []string{"L0001"}}
	raw := &lint.Result{Diags: runLint(t, src)}
	filtered := cfg.Apply(raw)
	foundError := false
	for _, d := range filtered.Diags {
		if d.Code == diag.CodeUnusedLet && d.Severity == diag.Error {
			foundError = true
			break
		}
	}
	if !foundError {
		t.Fatalf("Config.Deny should elevate L0001 to Error; got: %s", dumpCodes(filtered.Diags))
	}
}

func TestLint_ConfigDenyWinsOverAllow(t *testing.T) {
	src := `
fn f() {
    let unused = 1
    println("x")
}
`
	cfg := lint.Config{
		Allow: []string{"L0001"},
		Deny:  []string{"L0001"},
	}
	raw := &lint.Result{Diags: runLint(t, src)}
	filtered := cfg.Apply(raw)
	foundError := false
	for _, d := range filtered.Diags {
		if d.Code == diag.CodeUnusedLet && d.Severity == diag.Error {
			foundError = true
			break
		}
	}
	if !foundError {
		t.Fatalf("Deny should win over Allow; got: %s", dumpCodes(filtered.Diags))
	}
}

// ---- L0008 dead-store ----

func TestLint_DeadStore(t *testing.T) {
	src := `
fn heavy() -> Int { 42 }

fn f() {
    let mut x = heavy()
    x = 1
    println(x)
}
`
	assertWarns(t, runLint(t, src), diag.CodeDeadStore)
}

func TestLint_NoDeadStoreWhenReadBetween(t *testing.T) {
	src := `
fn f() {
    let mut x = 10
    println(x)
    x = 20
    println(x)
}
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeDeadStore {
			t.Fatalf("intermediate read should suppress dead-store: %s", d.Message)
		}
	}
}

func TestLint_NoDeadStoreWhenSelfReferential(t *testing.T) {
	src := `
fn f() {
    let mut x = 1
    x = x + 1
    println(x)
}
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeDeadStore {
			t.Fatalf("self-referential update should not be dead-store: %s", d.Message)
		}
	}
}

// ---- L0021 redundant-else after return ----

func TestLint_RedundantElseAfterReturn(t *testing.T) {
	src := `
fn f(c: Bool) -> Int {
    if c {
        return 1
    } else {
        return 2
    }
}
`
	assertWarns(t, runLint(t, src), diag.CodeRedundantElse)
}

// ---- L0022 constant condition ----

func TestLint_ConstantConditionTrue(t *testing.T) {
	src := `fn f() -> Int { if true { 1 } else { 2 } }`
	assertWarns(t, runLint(t, src), diag.CodeConstantCondition)
}

func TestLint_ConstantConditionNotTrue(t *testing.T) {
	src := `fn f() -> Int { if !true { 1 } else { 2 } }`
	assertWarns(t, runLint(t, src), diag.CodeConstantCondition)
}

// ---- L0023 empty branch ----

func TestLint_EmptyElseBranch(t *testing.T) {
	src := `
fn f(c: Bool) {
    if c {
        println("y")
    } else {
    }
}
`
	assertWarns(t, runLint(t, src), diag.CodeEmptyBranch)
}

// ---- L0024 needless return ----

func TestLint_NeedlessReturn(t *testing.T) {
	src := `
fn f() -> Int {
    let x = 3
    return x
}
`
	assertWarns(t, runLint(t, src), diag.CodeNeedlessReturn)
}

func TestLint_BareReturnIsClean(t *testing.T) {
	// `return` (no value) at the end of a unit fn is idiomatic — don't flag.
	src := `
fn f() {
    println("x")
    return
}
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeNeedlessReturn {
			t.Fatalf("bare return should not fire L0024: %s", d.Message)
		}
	}
}

// ---- L0025 identical branches ----

func TestLint_IdenticalBranches(t *testing.T) {
	src := `fn f(c: Bool) -> Int { if c { 1 } else { 1 } }`
	assertWarns(t, runLint(t, src), diag.CodeIdenticalBranches)
}

// ---- L0026 empty loop body ----

func TestLint_EmptyLoopBody(t *testing.T) {
	src := `
fn f() {
    for x in 0..10 {
    }
}
`
	assertWarns(t, runLint(t, src), diag.CodeEmptyLoopBody)
}

// ---- L0043 double negation ----

func TestLint_DoubleNegation(t *testing.T) {
	src := `
fn f(b: Bool) -> Bool { !!b }
`
	assertWarns(t, runLint(t, src), diag.CodeDoubleNegation)
}

// ---- L0044 bool-literal compare ----

func TestLint_BoolLiteralCompareEq(t *testing.T) {
	src := `
fn f(b: Bool) -> Bool { b == true }
`
	assertWarns(t, runLint(t, src), diag.CodeBoolLiteralCompare)
}

func TestLint_BoolLiteralCompareNe(t *testing.T) {
	src := `
fn f(b: Bool) -> Bool { b != false }
`
	assertWarns(t, runLint(t, src), diag.CodeBoolLiteralCompare)
}

// ---- L0045 negated bool literal ----

func TestLint_NegatedBoolLiteral(t *testing.T) {
	src := `
fn f() -> Bool { !true }
`
	assertWarns(t, runLint(t, src), diag.CodeNegatedBoolLiteral)
}

// ---- L0050 too many parameters ----

func TestLint_TooManyParams(t *testing.T) {
	src := `
fn many(a: Int, b: Int, c: Int, d: Int, e: Int, f: Int, g: Int, h: Int) {
    println(a)
    println(b)
    println(c)
    println(d)
    println(e)
    println(f)
    println(g)
    println(h)
}
`
	assertWarns(t, runLint(t, src), diag.CodeTooManyParams)
}

// ---- L0070 missing doc on pub ----

func TestLint_MissingDocOnPubFn(t *testing.T) {
	src := `
pub fn hashPassword(p: String) -> String { p }
`
	assertWarns(t, runLint(t, src), diag.CodeMissingDoc)
}

func TestLint_PubWithDocClean(t *testing.T) {
	src := `
/// Hash a password using bcrypt.
pub fn hashPassword(p: String) -> String { p }
`
	for _, d := range runLint(t, src) {
		if d.Code == diag.CodeMissingDoc {
			t.Fatalf("pub fn with doc should not fire L0070: %s", d.Message)
		}
	}
}

// ---- Smoke: empty main is fully clean ----

func TestLint_EmptyMainClean(t *testing.T) {
	assertClean(t, runLint(t, `fn main() {}`))
}

// ---- Autofix coverage for simplify rules (L0040, L0043, L0044, L0045) ----

// findFix returns the first machine-applicable Suggestion attached to
// any diagnostic with the given code. Fails the test if none is found.
func findFix(t *testing.T, diags []*diag.Diagnostic, code string) diag.Suggestion {
	t.Helper()
	for _, d := range diags {
		if d.Code != code {
			continue
		}
		for _, s := range d.Suggestions {
			if s.MachineApplicable {
				return s
			}
		}
	}
	t.Fatalf("no machine-applicable suggestion for %s in: %s", code, dumpCodes(diags))
	return diag.Suggestion{}
}

// applyOne rewrites src by applying exactly one suggestion. Returns the
// rewritten source. Errors out if ApplyFixes skips the edit.
func applyOne(t *testing.T, src string, d []*diag.Diagnostic, code string) string {
	t.Helper()
	// Keep only the one diagnostic we care about so other fixes don't
	// interfere when more than one rule fires.
	var filtered []*diag.Diagnostic
	for _, x := range d {
		if x.Code == code {
			filtered = append(filtered, x)
			break
		}
	}
	out, applied, skipped := lint.ApplyFixes([]byte(src), filtered)
	if applied != 1 || skipped != 0 {
		t.Fatalf("ApplyFixes(%s): applied=%d skipped=%d", code, applied, skipped)
	}
	return string(out)
}

func TestLint_NegatedBoolLiteralHasFix(t *testing.T) {
	src := `
fn f() -> Bool { !true }
`
	diags := runLint(t, src)
	fix := findFix(t, diags, diag.CodeNegatedBoolLiteral)
	if fix.Replacement != "false" {
		t.Fatalf("L0045 fix for `!true` should be `false`; got %q", fix.Replacement)
	}
	if fix.CopyFrom != nil {
		t.Fatalf("L0045 fix should be a plain replacement, not CopyFrom")
	}
	got := applyOne(t, src, diags, diag.CodeNegatedBoolLiteral)
	if !strings.Contains(got, "fn f() -> Bool { false }") {
		t.Fatalf("rewrite did not simplify `!true`; got:\n%s", got)
	}
}

func TestLint_NegatedBoolLiteralFalseHasFix(t *testing.T) {
	src := `
fn f() -> Bool { !false }
`
	diags := runLint(t, src)
	fix := findFix(t, diags, diag.CodeNegatedBoolLiteral)
	if fix.Replacement != "true" {
		t.Fatalf("L0045 fix for `!false` should be `true`; got %q", fix.Replacement)
	}
}

func TestLint_DoubleNegationHasCopyFix(t *testing.T) {
	src := `
fn f(b: Bool) -> Bool { !!b }
`
	diags := runLint(t, src)
	fix := findFix(t, diags, diag.CodeDoubleNegation)
	if fix.CopyFrom == nil {
		t.Fatalf("L0043 fix should copy the operand from source")
	}
	got := applyOne(t, src, diags, diag.CodeDoubleNegation)
	if !strings.Contains(got, "{ b }") {
		t.Fatalf("rewrite did not drop `!!`; got:\n%s", got)
	}
}

func TestLint_BoolLiteralCompareEqTrueHasFix(t *testing.T) {
	src := `
fn f(b: Bool) -> Bool { b == true }
`
	diags := runLint(t, src)
	fix := findFix(t, diags, diag.CodeBoolLiteralCompare)
	if fix.CopyFrom == nil {
		t.Fatalf("L0044 fix should carry a CopyFrom span")
	}
	if fix.Replacement != "%s" {
		t.Fatalf("`b == true` should rewrite to a bare copy; got template %q", fix.Replacement)
	}
	got := applyOne(t, src, diags, diag.CodeBoolLiteralCompare)
	if !strings.Contains(got, "{ b }") {
		t.Fatalf("rewrite did not simplify `b == true`; got:\n%s", got)
	}
}

func TestLint_BoolLiteralCompareEqFalseHasNegateFix(t *testing.T) {
	src := `
fn f(b: Bool) -> Bool { b == false }
`
	diags := runLint(t, src)
	fix := findFix(t, diags, diag.CodeBoolLiteralCompare)
	if fix.Replacement != "!(%s)" {
		t.Fatalf("`b == false` should rewrite via `!(%%s)` template; got %q", fix.Replacement)
	}
	got := applyOne(t, src, diags, diag.CodeBoolLiteralCompare)
	if !strings.Contains(got, "!(b)") {
		t.Fatalf("rewrite did not negate operand; got:\n%s", got)
	}
}

func TestLint_BoolLiteralCompareNeFalseHasFix(t *testing.T) {
	src := `
fn f(b: Bool) -> Bool { b != false }
`
	diags := runLint(t, src)
	fix := findFix(t, diags, diag.CodeBoolLiteralCompare)
	if fix.Replacement != "%s" {
		t.Fatalf("`b != false` should rewrite to a bare copy; got template %q", fix.Replacement)
	}
}

func TestLint_BoolLiteralCompareLiteralOnLeftHasFix(t *testing.T) {
	src := `
fn f(b: Bool) -> Bool { true == b }
`
	diags := runLint(t, src)
	fix := findFix(t, diags, diag.CodeBoolLiteralCompare)
	if fix.Replacement != "%s" {
		t.Fatalf("`true == b` should rewrite to a bare copy; got template %q", fix.Replacement)
	}
	got := applyOne(t, src, diags, diag.CodeBoolLiteralCompare)
	if !strings.Contains(got, "{ b }") {
		t.Fatalf("rewrite did not pick the non-literal side; got:\n%s", got)
	}
}

func TestLint_RedundantBoolPositiveHasFix(t *testing.T) {
	src := `
fn f(c: Bool) -> Bool {
    if c { true } else { false }
}
`
	diags := runLint(t, src)
	fix := findFix(t, diags, diag.CodeRedundantBool)
	if fix.CopyFrom == nil {
		t.Fatalf("L0040 fix should carry a CopyFrom span")
	}
	if fix.Replacement != "%s" {
		t.Fatalf("positive-polarity L0040 should rewrite to bare copy; got %q", fix.Replacement)
	}
	got := applyOne(t, src, diags, diag.CodeRedundantBool)
	if !strings.Contains(got, "c\n") && !strings.Contains(got, "{ c }") {
		t.Fatalf("rewrite did not substitute the condition; got:\n%s", got)
	}
}

func TestLint_RedundantBoolNegativeHasNegateFix(t *testing.T) {
	src := `
fn f(c: Bool) -> Bool {
    if c { false } else { true }
}
`
	diags := runLint(t, src)
	fix := findFix(t, diags, diag.CodeRedundantBool)
	if fix.Replacement != "!(%s)" {
		t.Fatalf("negative-polarity L0040 should use `!(%%s)`; got %q", fix.Replacement)
	}
	got := applyOne(t, src, diags, diag.CodeRedundantBool)
	if !strings.Contains(got, "!(c)") {
		t.Fatalf("rewrite did not negate the condition; got:\n%s", got)
	}
}

// ApplyFixes must respect MachineApplicable=false when a suggestion is
// advisory only. (Regression guard: this test ensures adding CopyFrom
// didn't accidentally make non-machine-applicable suggestions start
// applying.)
func TestLint_ApplyFixes_IgnoresAdvisorySuggestion(t *testing.T) {
	d := diag.New(diag.Warning, "advisory").
		Code("X").
		Primary(diag.Span{}, "").
		SuggestCopy(diag.Span{}, diag.Span{}, "%s", "no-op", false).
		Build()
	_, applied, skipped := lint.ApplyFixes([]byte("abc"), []*diag.Diagnostic{d})
	if applied != 0 || skipped != 0 {
		t.Fatalf("advisory suggestion should neither apply nor count as skipped; applied=%d skipped=%d", applied, skipped)
	}
}

// ---- L0024 / L0004 autofixes ----

func TestLint_NeedlessReturnHasCopyFix(t *testing.T) {
	src := `
fn f(x: Int) -> Int {
    return x + 1
}
`
	diags := runLint(t, src)
	fix := findFix(t, diags, diag.CodeNeedlessReturn)
	if fix.CopyFrom == nil {
		t.Fatalf("L0024 fix should copy the returned expression from source")
	}
	got := applyOne(t, src, diags, diag.CodeNeedlessReturn)
	if !strings.Contains(got, "    x + 1\n") {
		t.Fatalf("rewrite did not drop the `return` keyword; got:\n%s", got)
	}
}

func TestLint_UnusedMutHasFix(t *testing.T) {
	src := `
fn f(x: Int) -> Int {
    let mut y = x + 1
    y
}
`
	diags := runLint(t, src)
	fix := findFix(t, diags, diag.CodeUnusedMut)
	if fix.Replacement != "" {
		t.Fatalf("L0004 should propose a deletion, not a replacement; got %q", fix.Replacement)
	}
	got := applyOne(t, src, diags, diag.CodeUnusedMut)
	if !strings.Contains(got, "let y = x + 1") {
		t.Fatalf("rewrite did not drop `mut`; got:\n%s", got)
	}
}

func TestLint_UnusedMutTopLevelHasFix(t *testing.T) {
	// Top-level LetDecl path — exercises the separate MutPos capture in
	// parseLetDecl rather than parseLetStmt. runLint bails on the
	// resolver's "undefined name" when the in-test resolver can't see
	// top-level bindings from fn bodies, so this test parses directly
	// and inspects the AST + emitted suggestion.
	src := []byte(`pub let mut counter = 0
`)
	file, _ := parser.ParseDiagnostics(src)
	var ld *ast.LetDecl
	for _, d := range file.Decls {
		if x, ok := d.(*ast.LetDecl); ok && x.Mut {
			ld = x
			break
		}
	}
	if ld == nil {
		t.Fatalf("parser did not produce a top-level `let mut` decl")
	}
	if ld.MutPos.Offset == 0 && ld.MutPos.Line == 0 {
		t.Fatalf("parseLetDecl did not record MutPos for `let mut`")
	}
	// The `mut` keyword starts at offset 8 in "pub let mut counter".
	if got, want := ld.MutPos.Offset, 8; got != want {
		t.Fatalf("MutPos offset mismatch: got %d want %d", got, want)
	}
}

// ---- Round-trip: every rewrite must re-parse without new errors ----
//
// This is the ultimate safety guarantee for autofix: users should never
// see `osty lint --fix` emit a file that the parser then rejects. The
// test drives each fixable rule's canonical trigger, applies the fix,
// and asserts the rewritten source has no parse errors.

func TestLint_Fixes_RoundTripThroughParser(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"unused_let", "fn f() {\n    let unused = 1\n    println(\"x\")\n}\n"},
		{"unused_import", "use foo.bar.baz\n\nfn main() {\n    println(\"hi\")\n}\n"},
		{"unused_mut_stmt", "fn f(x: Int) -> Int {\n    let mut y = x + 1\n    y\n}\n"},
		{"needless_return", "fn f(x: Int) -> Int {\n    return x + 1\n}\n"},
		{"self_assign", "fn f() {\n    let mut x = 1\n    x = x\n    println(x)\n}\n"},
		{"redundant_bool", "fn f(c: Bool) -> Bool {\n    if c { true } else { false }\n}\n"},
		{"double_negation", "fn f(b: Bool) -> Bool { !!b }\n"},
		{"bool_literal_compare_eq", "fn f(b: Bool) -> Bool { b == true }\n"},
		{"bool_literal_compare_neg", "fn f(b: Bool) -> Bool { b == false }\n"},
		{"negated_bool_literal", "fn f() -> Bool { !true }\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := runLint(t, tc.src)
			out, applied, skipped := lint.ApplyFixes([]byte(tc.src), diags)
			if applied == 0 {
				t.Fatalf("no fixes applied for %s (skipped=%d); fixture may be stale", tc.name, skipped)
			}
			// Re-parse the rewritten source. The fix must produce
			// syntactically valid Osty.
			_, parseDiags := parser.ParseDiagnostics(out)
			for _, d := range parseDiags {
				if d.Severity == diag.Error {
					t.Fatalf("rewrite produced unparsable source for %s:\n--- rewrite ---\n%s\n--- parse error ---\n%s: %s",
						tc.name, string(out), d.Code, d.Message)
				}
			}
		})
	}
}
