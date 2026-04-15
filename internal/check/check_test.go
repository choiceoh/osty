package check_test

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
	"github.com/osty/osty/internal/types"
)

// runCheck parses, resolves, and type-checks one snippet, returning the
// combined diagnostics. Only error-severity entries are returned (the
// tests don't care about warnings).
func runCheck(t *testing.T, src string) []*diag.Diagnostic {
	t.Helper()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res)
	all := append(append([]*diag.Diagnostic{}, parseDiags...), res.Diags...)
	all = append(all, chk.Diags...)
	var errs []*diag.Diagnostic
	for _, d := range all {
		if d.Severity == diag.Error {
			errs = append(errs, d)
		}
	}
	return errs
}

// assertCodes asserts that the observed set of diagnostic codes matches
// `want` exactly (set equality, order-independent). Useful because
// parser + resolver + checker diagnostics can interleave and we only
// care that the *type-checker's* expected codes fire.
func assertCodes(t *testing.T, got []*diag.Diagnostic, want ...string) {
	t.Helper()
	set := map[string]int{}
	for _, d := range got {
		set[d.Code]++
	}
	for _, w := range want {
		if set[w] == 0 {
			codes := make([]string, 0, len(got))
			for _, d := range got {
				codes = append(codes, d.Code+": "+d.Message)
			}
			t.Fatalf("expected code %s; got: %s", w, strings.Join(codes, " | "))
		}
		set[w]--
	}
}

// assertOK asserts that no error-severity diagnostics were produced.
func assertOK(t *testing.T, got []*diag.Diagnostic) {
	t.Helper()
	if len(got) == 0 {
		return
	}
	lines := make([]string, 0, len(got))
	for _, d := range got {
		lines = append(lines, d.Code+": "+d.Message)
	}
	t.Fatalf("expected no errors, got %d:\n  %s", len(got), strings.Join(lines, "\n  "))
}

// ---- Happy-path tests ----

func TestCheck_LetWithAnnotationMatches(t *testing.T) {
	src := `
fn main() {
    let x: Int = 5
    let y: Float = 2.5
    let s: String = "hi"
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_NumericLiteralInference(t *testing.T) {
	src := `
fn main() {
    let a: Int32 = 100
    let b: UInt8 = 200
    let c: Float32 = 1.5
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_BuiltinTypeArity(t *testing.T) {
	src := `
fn useBuiltins(xs: List<Int>, h: Handle<Int>, group: TaskGroup) {
    let xs: List<Int> = [1]
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_BuiltinMarkerInterfacesRejectTypeArgs(t *testing.T) {
	src := `
fn main() {
    let x: Equal<Int>
}
`
	assertCodes(t, runCheck(t, src), diag.CodeGenericArgCount)
}

func TestCheck_BuiltinValueRejectedInTypePosition(t *testing.T) {
	src := `
fn main() {
    let x: Some<Int>
}
`
	assertCodes(t, runCheck(t, src), diag.CodeWrongSymbolKind)
}

func TestCheck_ArithmeticSameType(t *testing.T) {
	src := `
fn add(a: Int, b: Int) -> Int {
    a + b
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_IfBranchesUnify(t *testing.T) {
	src := `
fn label(n: Int) -> String {
    if n > 0 { "positive" } else { "non-positive" }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_MatchArmsUnify(t *testing.T) {
	src := `
pub enum Shape {
    Circle(Float),
    Rect(Float, Float),
    Empty,
}

fn area(s: Shape) -> Float {
    match s {
        Circle(r) -> r * r,
        Rect(w, h) -> w * h,
        Empty -> 0.0,
    }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_OptionSome(t *testing.T) {
	src := `
fn wrap(x: Int) -> Int? {
    Some(x)
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_ResultOkErr(t *testing.T) {
	src := `
pub interface Error {
    fn message(self) -> String
}

fn parseInt(s: String) -> Result<Int, Error> {
    Ok(42)
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_StructLiteralFullFields(t *testing.T) {
	src := `
pub struct Point {
    pub x: Int,
    pub y: Int,
}

fn origin() -> Point {
    Point { x: 0, y: 0 }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_MethodCall(t *testing.T) {
	src := `
pub struct Greeter {
    pub name: String,

    pub fn new(name: String) -> Greeter {
        Greeter { name }
    }

    pub fn greet(self) -> String {
        "hi"
    }
}

fn main() {
    let g = Greeter { name: "alice" }
    let s = g.greet()
}
`
	assertOK(t, runCheck(t, src))
}

// ---- Error tests ----

func TestCheck_LetAnnotationMismatch(t *testing.T) {
	src := `
fn main() {
    let x: Int = "not an int"
}
`
	assertCodes(t, runCheck(t, src), diag.CodeTypeMismatch)
}

func TestCheck_ArithOnBool(t *testing.T) {
	src := `
fn main() {
    let a = true + false
}
`
	assertCodes(t, runCheck(t, src), diag.CodeBinaryOpUntyped)
}

func TestCheck_IfBranchMismatch(t *testing.T) {
	src := `
fn main() {
    let x = if true { 5 } else { "no" }
}
`
	assertCodes(t, runCheck(t, src), diag.CodeIfBranchMismatch)
}

func TestCheck_ConditionNotBool(t *testing.T) {
	src := `
fn main() {
    let n: Int = 5
    if n { }
}
`
	assertCodes(t, runCheck(t, src), diag.CodeConditionNotBool)
}

func TestCheck_UnknownStructField(t *testing.T) {
	src := `
pub struct Point {
    pub x: Int,
    pub y: Int,
}

fn origin() -> Point {
    Point { x: 0, z: 9 }
}
`
	assertCodes(t, runCheck(t, src), diag.CodeUnknownStructField)
}

func TestCheck_MissingStructField(t *testing.T) {
	src := `
pub struct Point {
    pub x: Int,
    pub y: Int,
}

fn origin() -> Point {
    Point { x: 0 }
}
`
	assertCodes(t, runCheck(t, src), diag.CodeMissingStructField)
}

func TestCheck_ReturnTypeMismatch(t *testing.T) {
	src := `
fn f() -> Int {
    "hi"
}
`
	assertCodes(t, runCheck(t, src), diag.CodeTypeMismatch)
}

func TestCheck_AssignToImmutable(t *testing.T) {
	src := `
fn main() {
    let x = 5
    x = 6
}
`
	assertCodes(t, runCheck(t, src), diag.CodeMutabilityMismatch)
}

func TestCheck_AssignToMutableOK(t *testing.T) {
	src := `
fn main() {
    let mut x = 5
    x = 6
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_UnknownMethod(t *testing.T) {
	src := `
pub struct Empty {}

fn main() {
    let e = Empty {}
    e.nonexistent()
}
`
	assertCodes(t, runCheck(t, src), diag.CodeUnknownMethod)
}

func TestCheck_QuestionOnNonResult(t *testing.T) {
	src := `
fn main() {
    let x: Int = 5
    let y = x?
}
`
	assertCodes(t, runCheck(t, src), diag.CodeQuestionNotPropagate)
}

func TestCheck_IterForLoop(t *testing.T) {
	src := `
fn sum(xs: List<Int>) -> Int {
    let mut total = 0
    for x in xs {
        total = total + x
    }
    total
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_MapIndex(t *testing.T) {
	src := `
fn main() {
    let m: Map<String, Int> = {:}
    let v: Int = m["key"]
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_BoolCoalesce(t *testing.T) {
	src := `
fn main() {
    let name: String? = Some("alice")
    let display: String = name ?? "anonymous"
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_MatchSomeNone(t *testing.T) {
	src := `
fn describe(u: Int?) -> String {
    match u {
        Some(n) -> "got",
        None -> "missing",
    }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_OrderedComparisonOK(t *testing.T) {
	src := `
fn pickMax(a: Int, b: Int) -> Int {
    if a > b { a } else { b }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_GenericFunctionCall(t *testing.T) {
	// Generic bodies are checked once; call sites should type-check
	// without asserting identity against the TypeVar.
	src := `
fn identity<T>(x: T) -> T {
    x
}

fn main() {
    let a: Int = identity(5)
    let b: String = identity("hi")
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_GenericArgsAgreeAcrossParams(t *testing.T) {
	// Both arguments to `max<T>(a: T, b: T)` must agree on T.
	// Passing a String and an Int must be a type error.
	src := `
fn max<T: Ordered>(a: T, b: T) -> T {
    if a > b { a } else { b }
}

fn main() {
    let x = max(1, "two")
}
`
	assertCodes(t, runCheck(t, src), diag.CodeTypeMismatch)
}

func TestCheck_GenericHintPropagatesReturn(t *testing.T) {
	// Return-type hint from the annotation fixes T even before the
	// first argument is inspected.
	src := `
fn pick<T>(xs: List<T>) -> T {
    xs[0]
}

fn main() {
    let ys: List<Int> = [1, 2, 3]
    let first: Int = pick(ys)
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_ExplicitGenericArgCount(t *testing.T) {
	src := `
fn identity<T>(x: T) -> T {
    x
}

fn main() {
    let a = identity::<Int, String>(5)
}
`
	assertCodes(t, runCheck(t, src), diag.CodeGenericArgCount)
}

func TestCheck_ExplicitGenericArgsOnNonGenericFunction(t *testing.T) {
	src := `
fn plain(x: Int) -> Int {
    x
}

fn main() {
    let a = plain::<Int>(5)
}
`
	assertCodes(t, runCheck(t, src), diag.CodeGenericArgCount)
}

func TestCheck_FunctionValueCallTooFewArgs(t *testing.T) {
	src := `
fn add(a: Int, b: Int) -> Int {
    a + b
}

fn main() {
    let f = add
    let x = f(1)
}
`
	assertCodes(t, runCheck(t, src), diag.CodeWrongArgCount)
}

func TestCheck_FunctionValueRejectsKeywordArgs(t *testing.T) {
	src := `
fn connect(host: String, port: Int) -> Bool {
    true
}

fn main() {
    let f = connect
    let ok = f(host: "api.com", port: 443)
}
`
	assertCodes(t, runCheck(t, src), diag.CodeKeywordArgUnknown)
}

func TestCheck_GenericFunctionReferenceRejected(t *testing.T) {
	src := `
fn identity<T>(x: T) -> T {
    x
}

fn main() {
    let f = identity
}
`
	assertCodes(t, runCheck(t, src), diag.CodeGenericCallableReference)
}

func TestCheck_GenericMethodTurbofish(t *testing.T) {
	src := `
pub struct Box<T> {
    pub value: T,

    pub fn pair<U>(self, other: U) -> (T, U) {
        (self.value, other)
    }
}

fn main() {
    let b: Box<Int> = Box.builder().value(1).build()
    let p: (Int, String) = b.pair::<String>("x")
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_GenericMethodTurbofishCountsOnlyMethodGenerics(t *testing.T) {
	src := `
pub struct Box<T> {
    pub value: T,

    pub fn pair<U>(self, other: U) -> (T, U) {
        (self.value, other)
    }
}

fn main() {
    let b: Box<Int> = Box.builder().value(1).build()
    let p = b.pair::<Int, String>("x")
}
`
	assertCodes(t, runCheck(t, src), diag.CodeGenericArgCount)
}

func TestCheck_GenericMethodReferenceRejected(t *testing.T) {
	src := `
pub struct Box<T> {
    pub value: T,

    pub fn pair<U>(self, other: U) -> (T, U) {
        (self.value, other)
    }
}

fn main() {
    let b: Box<Int> = Box.builder().value(1).build()
    let f = b.pair
}
`
	assertCodes(t, runCheck(t, src), diag.CodeGenericCallableReference)
}

func TestCheck_TaskGroupCapabilityMayPassToHelper(t *testing.T) {
	src := `
fn one() -> Int { 1 }

fn useHandle(h: Handle<Int>) -> Int {
    h.join()
}

fn main() {
    let out = taskGroup(|g| {
        let h = g.spawn(|| one())
        useHandle(h)
    })
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_TaskGroupSpawnMayCaptureGroup(t *testing.T) {
	src := `
fn worker(g: TaskGroup) -> Int {
    if g.isCancelled() { 0 } else { 1 }
}

fn main() {
    let out = taskGroup(|g| {
        let h = g.spawn(|| worker(g))
        h.join()
    })
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_TaskGroupCapabilityCannotReturn(t *testing.T) {
	src := `
fn one() -> Int { 1 }

fn leak() -> Handle<Int> {
    taskGroup(|g| {
        g.spawn(|| one())
    })
}
`
	assertCodes(t, runCheck(t, src), diag.CodeCapabilityEscape)
}

func TestCheck_TaskGroupCapabilityCannotStoreTopLevel(t *testing.T) {
	src := `
fn one() -> Int { 1 }

pub let leaked = spawn(|| one())
`
	assertCodes(t, runCheck(t, src), diag.CodeCapabilityEscape)
}

func TestCheck_TaskGroupCapabilityCannotStoreInStructField(t *testing.T) {
	src := `
fn one() -> Int { 1 }

pub struct Box {
    pub h: Handle<Int>,
}

fn main() {
    let out = taskGroup(|g| {
        let h = g.spawn(|| one())
        let b = Box { h: h }
        0
    })
}
`
	assertCodes(t, runCheck(t, src), diag.CodeCapabilityEscape)
}

func TestCheck_TaskGroupCapabilityCannotStoreInList(t *testing.T) {
	src := `
fn one() -> Int { 1 }

fn main() {
    let out = taskGroup(|g| {
        let h = g.spawn(|| one())
        let hs = [h]
        0
    })
}
`
	assertCodes(t, runCheck(t, src), diag.CodeCapabilityEscape)
}

func TestCheck_TaskGroupCapabilityCannotSendOnChannel(t *testing.T) {
	src := `
fn one() -> Int { 1 }

fn main(ch: Chan<Handle<Int>>) {
    let out = taskGroup(|g| {
        let h = g.spawn(|| one())
        ch <- h
        0
    })
}
`
	assertCodes(t, runCheck(t, src), diag.CodeCapabilityEscape)
}

func TestCheck_TaskGroupCapabilityCannotEscapeViaClosureCapture(t *testing.T) {
	src := `
fn one() -> Int { 1 }

fn run(f: fn() -> Int) -> Int {
    f()
}

fn main() {
    let out = taskGroup(|g| {
        let h = g.spawn(|| one())
        let f = || h.join()
        run(f)
    })
}
`
	assertCodes(t, runCheck(t, src), diag.CodeCapabilityEscape)
}

func TestCheck_TaskGroupClosureArity(t *testing.T) {
	src := `
fn main() {
    let out = taskGroup(|| 1)
}
`
	assertCodes(t, runCheck(t, src), diag.CodeWrongArgCount)
}

func TestCheck_ClosurePatternParamBindsNames(t *testing.T) {
	src := `
fn render(f: fn((Int, Int)) -> Int, pair: (Int, Int)) -> Int {
    f(pair)
}

fn main() {
    let out = render(|(a, b)| a + b, (1, 2))
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_ClosurePatternParamRejectsRefutableNestedPattern(t *testing.T) {
	src := `
fn render(f: fn((Int, Int)) -> Int, pair: (Int, Int)) -> Int {
    f(pair)
}

fn main() {
    let out = render(|(_, 1)| 0, (1, 2))
}
`
	assertCodes(t, runCheck(t, src), diag.CodeRefutableClosurePattern)
}

func TestCheck_InterfaceBoundSatisfiedByPrim(t *testing.T) {
	// Int satisfies Ordered; the call is well-typed.
	src := `
fn max<T: Ordered>(a: T, b: T) -> T {
    if a > b { a } else { b }
}

fn main() {
    let m = max(1, 2)
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_InterfaceBoundFloatNotHashable(t *testing.T) {
	// Float is explicitly non-Hashable per §2.6.5; the call must error.
	src := `
fn key<T: Hashable>(x: T) -> T { x }

fn main() {
    let h = key(1.5)
}
`
	assertCodes(t, runCheck(t, src), diag.CodeTypeMismatch)
}

func TestCheck_MapKeyRequiresHashableTypeArg(t *testing.T) {
	src := `
fn main() {
    let m: Map<Float, Int> = {:}
}
`
	assertCodes(t, runCheck(t, src), diag.CodeTypeMismatch)
}

func TestCheck_MapLiteralKeyRequiresHashable(t *testing.T) {
	src := `
fn main() {
    let m = {1.5: "nope"}
}
`
	assertCodes(t, runCheck(t, src), diag.CodeTypeMismatch)
}

func TestCheck_MapKeyRejectsNonHashableComposite(t *testing.T) {
	src := `
fn main() {
    let m: Map<List<Int>, Int> = {:}
}
`
	assertCodes(t, runCheck(t, src), diag.CodeTypeMismatch)
}

func TestCheck_MapKeyAcceptsHashableGenericBound(t *testing.T) {
	src := `
fn make<T: Hashable>() -> Map<T, Int> {
    {:}
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_SetElementRequiresHashableTypeArg(t *testing.T) {
	src := `
fn main() {
    let s: Set<Float> = [1.5].toSet()
}
`
	assertCodes(t, runCheck(t, src), diag.CodeTypeMismatch)
}

func TestCheck_ToSetElementRequiresHashable(t *testing.T) {
	src := `
fn collect<T>(xs: List<T>) -> List<T> {
    let s = xs.toSet()
    xs
}
`
	assertCodes(t, runCheck(t, src), diag.CodeTypeMismatch)
}

func TestCheck_ToSetRejectsNonHashableCompositeElement(t *testing.T) {
	src := `
fn main() {
    let xs = [[1], [2]]
    let s = xs.toSet()
}
`
	assertCodes(t, runCheck(t, src), diag.CodeTypeMismatch)
}

func TestCheck_ToSetAcceptsHashableGenericBound(t *testing.T) {
	src := `
fn collect<T: Hashable>(xs: List<T>) -> Set<T> {
    xs.toSet()
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_InterfaceMethodMissing(t *testing.T) {
	// Empty struct has no `message` method → doesn't satisfy Error.
	// Note: Error is a BUILTIN marker interface in this MVP; the
	// checker's structural check reports the mismatch at the call site.
	// Uses a user-defined `Printable` to exercise the structural path.
	src := `
pub interface Printable {
    fn show(self) -> String
}

pub struct Empty {}

fn print_it<T: Printable>(x: T) {}

fn main() {
    print_it(Empty {})
}
`
	assertCodes(t, runCheck(t, src), diag.CodeTypeMismatch)
}

func TestCheck_InterfaceMethodMatchesStructurally(t *testing.T) {
	src := `
pub interface Printable {
    fn show(self) -> String
}

pub struct Greeting {
    pub name: String,

    pub fn show(self) -> String {
        "hi"
    }
}

fn print_it<T: Printable>(x: T) {}

fn main() {
    print_it(Greeting { name: "alice" })
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_InterfaceValueExposesRequiredMethods(t *testing.T) {
	src := `
pub interface Printable {
    fn show(self) -> String
}

fn render(p: Printable) -> String {
    p.show()
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_InterfaceBoundExposesRequiredMethods(t *testing.T) {
	src := `
pub interface Printable {
    fn show(self) -> String
}

fn render<T: Printable>(p: T) -> String {
    p.show()
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_InterfaceReturnRequiresGenericBound(t *testing.T) {
	src := `
pub interface Printable {
    fn show(self) -> String
}

fn render<T>(p: T) -> Printable {
    p
}
`
	assertCodes(t, runCheck(t, src), diag.CodeTypeMismatch)
}

func TestCheck_InterfaceReturnAcceptsGenericBound(t *testing.T) {
	src := `
pub interface Printable {
    fn show(self) -> String
}

fn render<T: Printable>(p: T) -> Printable {
    p
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_InterfaceReturnRejectsDifferentBoundSignature(t *testing.T) {
	src := `
pub interface IntBox {
    fn get(self) -> Int
}

pub interface StringBox {
    fn get(self) -> String
}

fn render<T: IntBox>(p: T) -> StringBox {
    p
}
`
	assertCodes(t, runCheck(t, src), diag.CodeTypeMismatch)
}

func TestCheck_InterfaceCompositionSatisfiedStructurally(t *testing.T) {
	src := `
pub interface Named {
    fn name(self) -> String
}

pub interface Printable {
    fn show(self) -> String
}

pub interface DisplayNamed {
    Named
    Printable
}

pub struct User {
    pub label: String,

    pub fn name(self) -> String { self.label }
    pub fn show(self) -> String { self.label }
}

fn render<T: DisplayNamed>(x: T) -> String {
    let n = x.name()
    x.show()
}

fn main() {
    let u = User { label: "ada" }
    render(u)
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_InterfaceCompositionMissingInheritedMethod(t *testing.T) {
	src := `
pub interface Named {
    fn name(self) -> String
}

pub interface Printable {
    fn show(self) -> String
}

pub interface DisplayNamed {
    Named
    Printable
}

pub struct OnlyNamed {
    pub label: String,

    pub fn name(self) -> String { self.label }
}

fn render<T: DisplayNamed>(x: T) {}

fn main() {
    render(OnlyNamed { label: "ada" })
}
`
	assertCodes(t, runCheck(t, src), diag.CodeTypeMismatch)
}

func TestCheck_InterfaceSelfSignatureMatchesConcreteSelf(t *testing.T) {
	src := `
pub interface Same {
    fn same(self, other: Self) -> Bool
}

pub struct Point {
    pub x: Int,

    pub fn same(self, other: Point) -> Bool { true }
}

fn require<T: Same>(x: T) {}

fn main() {
    require(Point { x: 1 })
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_InterfaceSatisfactionSubstitutesGenericReceiver(t *testing.T) {
	src := `
pub interface IntBox {
    fn get(self) -> Int
}

pub struct Box<T> {
    pub value: T,

    pub fn get(self) -> T { self.value }
}

fn require<T: IntBox>(x: T) {}

fn main() {
    require(Box.builder().value(1).build())
}
`
	assertOK(t, runCheck(t, src))
}

// runCheckWithStdlib parses + resolves + type-checks with the embedded
// stdlib attached. Tests use it for intrinsic primitive methods and for
// resolved `use std.*` module surfaces.
func runCheckWithStdlib(t *testing.T, src string) []*diag.Diagnostic {
	t.Helper()
	reg := loadRegistry()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	chk := check.File(file, res, check.Opts{Primitives: reg.Primitives, ResultMethods: reg.ResultMethods})
	all := append(append([]*diag.Diagnostic{}, parseDiags...), res.Diags...)
	all = append(all, chk.Diags...)
	var errs []*diag.Diagnostic
	for _, d := range all {
		if d.Severity == diag.Error {
			errs = append(errs, d)
		}
	}
	return errs
}

func TestCheck_StdRandomModuleSurface(t *testing.T) {
	src := `
use std.random

fn demo() -> Int {
    let rng = random.seeded(42)
    rng.int(0, 10)
}
`
	assertOK(t, runCheckWithStdlib(t, src))
}

func TestCheck_StdURLParseSurface(t *testing.T) {
	src := `
use std.url

fn hostOf(text: String) -> Result<String, Error> {
    let parsed = url.parse(text)?
    Ok(parsed.host)
}
`
	assertOK(t, runCheckWithStdlib(t, src))
}

func TestCheck_IntAbsReturnsSelfKind(t *testing.T) {
	// `Int8.abs()` must return Int8 (not Int). The stdlib stub uses
	// `Self`, so each primitive's method table preserves the kind.
	src := `
fn main() {
    let x: Int8 = 5
    let y: Int8 = x.abs()
}
`
	got := runCheckWithStdlib(t, src)
	if len(got) > 0 {
		lines := make([]string, 0, len(got))
		for _, d := range got {
			lines = append(lines, d.Code+": "+d.Message)
		}
		t.Fatalf("expected no errors, got: %s", strings.Join(lines, ", "))
	}
}

func TestCheck_IntCheckedAddReturnsOptional(t *testing.T) {
	src := `
fn main() {
    let a: Int32 = 5
    let b: Int32 = 3
    let c: Int32? = a.checkedAdd(b)
}
`
	got := runCheckWithStdlib(t, src)
	if len(got) > 0 {
		lines := make([]string, 0, len(got))
		for _, d := range got {
			lines = append(lines, d.Code+": "+d.Message)
		}
		t.Fatalf("expected no errors, got: %s", strings.Join(lines, ", "))
	}
}

func TestCheck_IntToInt32ReturnsResult(t *testing.T) {
	// Narrowing conversion returns Result; using ? propagates the err.
	src := `
pub interface Error {
    fn message(self) -> String
}
pub enum Result<T, E> {
    Ok(T),
    Err(E),
}

fn down(x: Int) -> Result<Int32, Error> {
    let y: Int32 = x.toInt32()?
    Ok(y)
}
`
	got := runCheckWithStdlib(t, src)
	if len(got) > 0 {
		lines := make([]string, 0, len(got))
		for _, d := range got {
			lines = append(lines, d.Code+": "+d.Message)
		}
		t.Fatalf("expected no errors, got: %s", strings.Join(lines, ", "))
	}
}

func TestCheck_OptionUnwrapReturnsInner(t *testing.T) {
	src := `
fn main() {
    let x: Int? = Some(5)
    let y: Int = x.unwrap()
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_ResultIsOkReturnsBool(t *testing.T) {
	// Uses the prelude's builtin Result; no user-defined enum shadow.
	src := `
fn test(r: Result<Int, String>) -> Bool {
    r.isOk()
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_ResultCombinatorsPreservePreciseTypes(t *testing.T) {
	src := `
fn parse() -> Result<Int, String> { Ok(1) }

fn test() {
    let mapped: Result<String, String> = parse().map(|n| "value={n}")
    let mappedErr: Result<Int, Int> = parse().mapErr(|e| e.len())
    let okValue: Int? = parse().ok()
    let errValue: String? = parse().err()
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_ResultMapRejectsWrongReturnType(t *testing.T) {
	src := `
fn parse() -> Result<Int, String> { Ok(1) }

fn test() {
    let mapped: Result<Int, String> = parse().map(|n| "value={n}")
}
`
	assertCodes(t, runCheck(t, src), diag.CodeTypeMismatch)
}

func TestCheck_ResultMethodsCanComeFromStdlibSource(t *testing.T) {
	method := resultMethodForTest(t, "map", `pub enum Result<T, E> {
    Ok(T),
    Err(E),

    pub fn map(self) -> Bool { true }
}
`)
	src := `
fn parse() -> Result<Int, String> { Ok(1) }

fn test() {
    let b: Bool = parse().map()
}
`
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res, check.Opts{ResultMethods: map[string]*ast.FnDecl{"map": method}})
	all := append(append([]*diag.Diagnostic{}, parseDiags...), res.Diags...)
	all = append(all, chk.Diags...)
	assertOK(t, onlyErrors(all))
}

func resultMethodForTest(t *testing.T, name, src string) *ast.FnDecl {
	t.Helper()
	file, diags := parser.ParseDiagnostics([]byte(src))
	if errs := onlyErrors(diags); len(errs) > 0 {
		t.Fatalf("parse result stub: %v", errs[0])
	}
	for _, d := range file.Decls {
		enum, ok := d.(*ast.EnumDecl)
		if !ok || enum.Name != "Result" {
			continue
		}
		for _, m := range enum.Methods {
			if m.Name == name {
				return m
			}
		}
	}
	t.Fatalf("result method %q not found", name)
	return nil
}

func onlyErrors(diags []*diag.Diagnostic) []*diag.Diagnostic {
	errs := make([]*diag.Diagnostic, 0, len(diags))
	for _, d := range diags {
		if d.Severity == diag.Error {
			errs = append(errs, d)
		}
	}
	return errs
}

// loadRegistry loads the stdlib registry once per test run. Isolated
// here so the check_test importing the stdlib package doesn't force a
// cycle with the check package.
func loadRegistry() registryShim {
	return loadRegistryOnce()
}

func TestCheck_OverflowInFunctionArg(t *testing.T) {
	src := `
fn take(x: UInt8) {}

fn main() {
    take(300)
}
`
	assertCodes(t, runCheck(t, src), diag.CodeNumericLitRange)
}

func TestCheck_OverflowInStructLiteral(t *testing.T) {
	src := `
pub struct Color { pub r: UInt8, pub g: UInt8, pub b: UInt8 }

fn main() {
    let c = Color { r: 100, g: 300, b: 50 }
}
`
	assertCodes(t, runCheck(t, src), diag.CodeNumericLitRange)
}

func TestCheck_OverflowInReturnValue(t *testing.T) {
	src := `
fn getByte() -> UInt8 {
    300
}
`
	assertCodes(t, runCheck(t, src), diag.CodeNumericLitRange)
}

func TestCheck_PositionQueryTypeAt(t *testing.T) {
	src := `fn main() {
    let x: Int = 5
}
`
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) > 0 {
		t.Fatalf("parse errors: %v", parseDiags)
	}
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res)

	// `5` is at line 2, col 18 (1-indexed). TypeAt should resolve it
	// to the literal's typed value — Int in this context.
	pos := token.Pos{Line: 2, Column: 18}
	tp := chk.TypeAt(pos)
	if tp == nil {
		t.Fatalf("expected a type at %v, got nil", pos)
	}
	if tp.String() != "Int" {
		t.Fatalf("expected Int at %v, got %s", pos, tp)
	}
}

func TestCheck_HoverReturnsSymbolAndType(t *testing.T) {
	src := `fn greet() -> String {
    "hi"
}

fn main() {
    let g = greet
}
`
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) > 0 {
		t.Fatalf("parse errors: %v", parseDiags)
	}
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res)

	// `greet` on the last line, column 13 — position of the `g` is
	// col 9 and `greet` starts at col 13.
	pos := token.Pos{Line: 6, Column: 13}
	info := chk.Hover(pos, res)
	if info.Symbol == nil {
		t.Fatal("expected a symbol at hover position")
	}
	if info.Symbol.Name != "greet" {
		t.Fatalf("expected symbol `greet`, got `%s`", info.Symbol.Name)
	}
}

func TestCheck_UnreachableAfterReturn(t *testing.T) {
	src := `
fn main() -> Int {
    return 42
    let x = 1
}
`
	assertCodes(t, runCheck(t, src), diag.CodeUnreachableCode)
}

func TestCheck_UnreachableAfterBreak(t *testing.T) {
	src := `
fn main() {
    for {
        break
        let x = 1
    }
}
`
	assertCodes(t, runCheck(t, src), diag.CodeUnreachableCode)
}

func TestCheck_MissingReturnOnNonUnit(t *testing.T) {
	src := `
fn getInt() -> Int {
    let x = 5
}
`
	got := runCheck(t, src)
	found := false
	for _, d := range got {
		if d.Code == diag.CodeMissingReturn || d.Code == diag.CodeTypeMismatch {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected missing-return or type-mismatch, got %v", got)
	}
}

func TestCheck_DefaultArgMustBeLiteral(t *testing.T) {
	src := `
fn helper() -> Int { 5 }

fn connect(x: Int = helper()) -> Int { x }
`
	assertCodes(t, runCheck(t, src), diag.CodeDefaultNotLiteral)
}

func TestCheck_DefaultArgLiteralOK(t *testing.T) {
	src := `
fn connect(
    host: String,
    port: Int = 80,
    retries: Int = 3,
    trace: Bool = false,
    opt: Int? = None,
) -> Bool { true }
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_SuggestTypoFieldName(t *testing.T) {
	src := `
pub struct User {
    pub name: String,
    pub age: Int,
}

fn main() {
    let u = User { name: "alice", age: 30 }
    let x = u.nmae
}
`
	got := runCheck(t, src)
	if len(got) == 0 {
		t.Fatal("expected E0702 diagnostic")
	}
	if !strings.Contains(got[0].Hint, "name") {
		t.Fatalf("expected `did you mean name`, got hint: %q", got[0].Hint)
	}
}

func TestCheck_SuggestTypoMethodName(t *testing.T) {
	// The field-access path already walks fields AND methods, so a
	// typo on `greet` lands in errFieldNotFound with a method-aware
	// candidate list. errMethodNotFound on the call path consumes the
	// same list via methodCandidates (stubbed while that helper's
	// type-aware analysis is being reworked externally).
	src := `
pub struct Greeter {
    pub name: String,

    pub fn greet(self) -> String { "hi" }
}

fn main() {
    let g = Greeter { name: "alice" }
    let s = g.greeet
}
`
	got := runCheck(t, src)
	if len(got) == 0 {
		t.Fatal("expected E0702 diagnostic")
	}
	if !strings.Contains(got[0].Hint, "greet") {
		t.Fatalf("expected `did you mean greet`, got hint: %q", got[0].Hint)
	}
}

func TestCheck_SuggestTypoStructLitField(t *testing.T) {
	src := `
pub struct Point {
    pub x: Int,
    pub y: Int,
}

fn main() {
    let p = Point { x: 1, yy: 2 }
}
`
	got := runCheck(t, src)
	if len(got) == 0 {
		t.Fatal("expected E0707 diagnostic")
	}
	found := false
	for _, d := range got {
		if strings.Contains(d.Hint, "y") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected `did you mean y` hint; got %v", got)
	}
}

func TestCheck_WitnessNamesTupleGap(t *testing.T) {
	// The diagnostic should quote the missing (true, false) pattern,
	// not a generic "add a wildcard".
	src := `
fn classify(t: (Bool, Bool)) -> String {
    match t {
        (true, true) -> "tt",
        (false, _) -> "f_",
    }
}
`
	got := runCheck(t, src)
	if len(got) == 0 {
		t.Fatal("expected E0731 diagnostic")
	}
	if !strings.Contains(got[0].Message, "(true, false)") {
		t.Fatalf("expected witness `(true, false)`, got: %s", got[0].Message)
	}
}

func TestCheck_WitnessNamesStructGap(t *testing.T) {
	src := `
pub struct Pair {
    pub a: Bool,
    pub b: Bool,
}

fn classify(p: Pair) -> String {
    match p {
        Pair { a: true, b: true } -> "tt",
        Pair { a: false, .. } -> "f_",
    }
}
`
	got := runCheck(t, src)
	if len(got) == 0 {
		t.Fatal("expected E0731 diagnostic")
	}
	if !strings.Contains(got[0].Message, "Pair") {
		t.Fatalf("expected struct Pair in witness, got: %s", got[0].Message)
	}
}

func TestCheck_RangePatternOnOrdered(t *testing.T) {
	src := `
fn classify(c: Char) -> String {
    match c {
        'a'..='z' -> "lower",
        'A'..='Z' -> "upper",
        _ -> "other",
    }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_RangePatternOnNonOrdered(t *testing.T) {
	// Range on a struct scrutinee — scrutinee isn't Ordered.
	src := `
pub struct Wrapper { pub n: Int }

fn check(w: Wrapper) -> String {
    match w {
        0..=9 -> "low",
        _ -> "other",
    }
}
`
	assertCodes(t, runCheck(t, src), diag.CodeRangePatternNonOrd)
}

func TestCheck_BindingPatternCarriesType(t *testing.T) {
	// `x @ pattern` binds x to the scrutinee's type at the binding
	// site; inner pattern narrows but the outer binding keeps the
	// full scrutinee type.
	src := `
fn classify(n: Int) -> Int {
    match n {
        x @ 0..=9 -> x + 1,
        _ -> 0,
    }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_NestedLetDestructuring(t *testing.T) {
	src := `
fn main() {
    let (a, (b, c)) = (1, (2, 3))
    let sum: Int = a + b + c
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_StructDestructureLet(t *testing.T) {
	src := `
pub struct Point {
    pub x: Int,
    pub y: Int,
}

fn main() {
    let p = Point { x: 1, y: 2 }
    let Point { x, y } = p
    let sum: Int = x + y
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_RecursiveGenericType(t *testing.T) {
	src := `
pub struct Node<T> {
    pub value: T,
    pub next: Node<T>?,
}

fn head_value(n: Node<Int>) -> Int {
    n.value
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_MutuallyRecursiveTypes(t *testing.T) {
	// Two structs referring to each other through Optionals.
	src := `
pub struct Forward {
    pub next: Backward?,
}

pub struct Backward {
    pub prev: Forward?,
}

fn main() {
    let f = Forward { next: None }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_TypeAliasChain(t *testing.T) {
	// Aliases through other aliases should be transparent — the final
	// type is whatever the alias chain terminates at.
	src := `
type ID = Int
type UserID = ID

fn identity(n: UserID) -> Int {
    n
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_OrPatternBindingsMustAgree(t *testing.T) {
	// Both alternatives bind `n`; arm body uses it. This is the
	// positive case — the resolver should accept it.
	src := `
pub enum Tag {
    A(Int),
    B(Int),
    C,
}

fn val(t: Tag) -> Int {
    match t {
        A(n) | B(n) -> n,
        C -> 0,
    }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_TupleMatchExhaustive(t *testing.T) {
	src := `
fn classify(t: (Bool, Bool)) -> String {
    match t {
        (true, true) -> "tt",
        (true, false) -> "tf",
        (false, true) -> "ft",
        (false, false) -> "ff",
    }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_TupleMatchNonExhaustive(t *testing.T) {
	src := `
fn classify(t: (Bool, Bool)) -> String {
    match t {
        (true, true) -> "tt",
        (false, _) -> "f_",
    }
}
`
	assertCodes(t, runCheck(t, src), diag.CodeNonExhaustiveMatch)
}

func TestCheck_TupleMatchCatchAllColumn(t *testing.T) {
	// First column wildcarded across both arms; second column also
	// fully covered (true/false). This IS exhaustive.
	src := `
fn classify(t: (Bool, Bool)) -> String {
    match t {
        (_, true) -> "xt",
        (_, false) -> "xf",
    }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_StructMatchExhaustiveViaRest(t *testing.T) {
	src := `
pub struct Point {
    pub x: Int,
    pub y: Int,
}

fn check(p: Point) -> Int {
    match p {
        Point { .. } -> 0,
    }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_StructMatchExhaustiveViaFieldBindings(t *testing.T) {
	src := `
pub struct Point {
    pub x: Int,
    pub y: Int,
}

fn check(p: Point) -> Int {
    match p {
        Point { x, y } -> x + y,
    }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_StdlibMapCallback(t *testing.T) {
	src := `
fn main() {
    let xs: List<Int> = [1, 2, 3]
    let doubled: List<Int> = xs.map(|x| x * 2)
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_StdlibMapCallbackBadArity(t *testing.T) {
	src := `
fn main() {
    let xs: List<Int> = [1, 2, 3]
    let bad = xs.map()
}
`
	assertCodes(t, runCheck(t, src), diag.CodeWrongArgCount)
}

func TestCheck_StdlibFilterMustReturnBool(t *testing.T) {
	src := `
fn main() {
    let xs: List<Int> = [1, 2, 3]
    let r = xs.filter(|x| x + 1)
}
`
	assertCodes(t, runCheck(t, src), diag.CodeTypeMismatch)
}

func TestCheck_StdlibPushTypeCheck(t *testing.T) {
	src := `
fn main() {
    let xs: List<Int> = [1, 2, 3]
    xs.push("not an int")
}
`
	assertCodes(t, runCheck(t, src), diag.CodeTypeMismatch)
}

func TestCheck_StdlibMutatingCollectionNeedsMutBinding(t *testing.T) {
	src := `
fn main() {
    let xs: List<Int> = [1, 2, 3]
    xs.push(4)
}
`
	assertCodes(t, runCheck(t, src), diag.CodeMutabilityMismatch)
}

func TestCheck_StdlibMutatingCollectionNeedsAssignableReceiver(t *testing.T) {
	src := `
fn make() -> List<Int> { [] }

fn main() {
    make().push(1)
}
`
	assertCodes(t, runCheck(t, src), diag.CodeAssignTarget)
}

func TestCheck_StdlibEnumerateTuplePattern(t *testing.T) {
	src := `
fn main() {
    let pairs = [(1, 2), (3, 4)]
    for (i, (a, b)) in pairs.enumerate() {
        let total: Int = i + a + b
    }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_ParallelMapForm(t *testing.T) {
	src := `
fn main() {
    let xs: List<Int> = [1, 2, 3]
    let rs: List<Result<Int, Error>> = parallel(xs, 2, |x| Ok(x * 2))
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_QuestionUsesClosureReturnType(t *testing.T) {
	src := `
fn parse() -> Result<Int, String> {
    Ok(1)
}

fn main() {
    let f: fn() -> Result<Int, String> = || {
        let x = parse()?
        Ok(x)
    }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_IteratorProtocolUserDefined(t *testing.T) {
	// A type with `iter(self) -> I` where `I` has `next(mut self) -> T?`
	// should iterate over T in `for`.
	src := `
pub struct Countdown {
    pub n: Int,

    pub fn next(mut self) -> Int? {
        if self.n <= 0 { None } else { Some(self.n) }
    }
}

pub struct CountdownFrom {
    pub start: Int,

    pub fn iter(self) -> Countdown {
        Countdown { n: self.start }
    }
}

fn main() {
    let cf = CountdownFrom { start: 3 }
    for x in cf {
        let y: Int = x
    }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_IteratorProtocolNonIterable(t *testing.T) {
	src := `
pub struct Empty {}

fn main() {
    let e = Empty {}
    for x in e { }
}
`
	assertCodes(t, runCheck(t, src), diag.CodeIterableNotProtocol)
}

func TestCheck_ChannelSendWrongType(t *testing.T) {
	src := `
fn main(ch: Chan<Int>) {
    ch <- "not an int"
}
`
	assertCodes(t, runCheck(t, src), diag.CodeChannelWrongValue)
}

func TestCheck_ChannelSendNotChan(t *testing.T) {
	src := `
fn main() {
    let x: Int = 5
    x <- 1
}
`
	assertCodes(t, runCheck(t, src), diag.CodeChannelNotChan)
}

func TestCheck_QuestionErrTypeMatches(t *testing.T) {
	src := `
pub interface Error {
    fn message(self) -> String
}

pub enum Result<T, E> {
    Ok(T),
    Err(E),
}

fn loadConfig() -> Result<Int, Error> {
    let v: Result<Int, Error> = Ok(1)
    let x: Int = v?
    Ok(x)
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_QuestionErrTypeMismatch(t *testing.T) {
	// Function returns Result<_, String>, the `?` expr propagates Err(Int).
	// Int isn't assignable to String, so the propagation is invalid.
	src := `
pub enum Result<T, E> {
    Ok(T),
    Err(E),
}

fn loadConfig() -> Result<Int, String> {
    let v: Result<Int, Int> = Ok(1)
    let x: Int = v?
    Ok(x)
}
`
	assertCodes(t, runCheck(t, src), diag.CodeQuestionBadReturn)
}

func TestCheck_QuestionErrSatisfiesInterface(t *testing.T) {
	// Function returns Result<_, Error> (builtin Error interface).
	// Propagating a MyError that has message() method is fine.
	src := `
pub interface Error {
    fn message(self) -> String
}

pub enum Result<T, E> {
    Ok(T),
    Err(E),
}

pub struct MyError {
    pub msg: String,

    pub fn message(self) -> String {
        "err"
    }
}

fn loadConfig() -> Result<Int, Error> {
    let v: Result<Int, MyError> = Ok(1)
    let x: Int = v?
    Ok(x)
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_StructSpreadCoversMissingFields(t *testing.T) {
	src := `
pub struct Point {
    pub x: Int,
    pub y: Int,
}

fn main() {
    let p = Point { x: 1, y: 2 }
    let q = Point { ..p, x: 3 }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_StructSpreadWrongType(t *testing.T) {
	// Spread source must be the same struct. `Other` values can't
	// spread into `Point`.
	src := `
pub struct Point {
    pub x: Int,
    pub y: Int,
}

pub struct Other {
    pub a: Int,
    pub b: Int,
}

fn main() {
    let o = Other { a: 1, b: 2 }
    let p = Point { ..o, x: 3 }
}
`
	assertCodes(t, runCheck(t, src), diag.CodeTypeMismatch)
}

func TestCheck_InterpolationRejectsFunctionValue(t *testing.T) {
	src := `
pub fn greet() -> String { "hi" }

fn main() {
    let s = "hello {greet}"
}
`
	assertCodes(t, runCheck(t, src), diag.CodeInterpolationNonStr)
}

func TestCheck_InterpolationPrimOK(t *testing.T) {
	src := `
fn main() {
    let n: Int = 5
    let s = "n is {n}"
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_KeywordArgOK(t *testing.T) {
	src := `
fn connect(host: String, port: Int = 80, timeout: Int = 30) -> Bool { true }

fn main() {
    let a = connect("api.com", port: 443)
    let b = connect("api.com", timeout: 60, port: 8080)
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_KeywordArgUnknown(t *testing.T) {
	src := `
fn connect(host: String, port: Int = 80) -> Bool { true }

fn main() {
    let a = connect("api.com", nonexistent: 42)
}
`
	assertCodes(t, runCheck(t, src), diag.CodeKeywordArgUnknown)
}

func TestCheck_PositionalAfterKeyword(t *testing.T) {
	src := `
fn connect(host: String, port: Int = 80, timeout: Int = 30) -> Bool { true }

fn main() {
    let a = connect("api.com", port: 443, 60)
}
`
	assertCodes(t, runCheck(t, src), diag.CodePositionalAfterKw)
}

func TestCheck_DuplicateKeywordArg(t *testing.T) {
	src := `
fn connect(host: String, port: Int = 80) -> Bool { true }

fn main() {
    let a = connect("api.com", port: 443, port: 80)
}
`
	assertCodes(t, runCheck(t, src), diag.CodeDuplicateArg)
}

func TestCheck_MissingRequiredArg(t *testing.T) {
	src := `
fn connect(host: String, port: Int = 80) -> Bool { true }

fn main() {
    let a = connect(port: 443)
}
`
	assertCodes(t, runCheck(t, src), diag.CodeWrongArgCount)
}

func TestCheck_NestedOptionExhaustive(t *testing.T) {
	src := `
fn describe(u: Int??) -> String {
    match u {
        Some(Some(n)) -> "inner",
        Some(None) -> "empty",
        None -> "missing",
    }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_NestedOptionMissingInner(t *testing.T) {
	// Some(None) not covered, inner is Option[Int] so Some(Some) alone
	// doesn't cover all Some values.
	src := `
fn describe(u: Int??) -> String {
    match u {
        Some(Some(n)) -> "inner",
        None -> "missing",
    }
}
`
	assertCodes(t, runCheck(t, src), diag.CodeNonExhaustiveMatch)
}

func TestCheck_NestedOptionCatchAllSome(t *testing.T) {
	// Some(_) catches every Some payload — no recursion needed.
	src := `
fn describe(u: Int??) -> String {
    match u {
        Some(_) -> "present",
        None -> "missing",
    }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_UnreachableArmAfterCatchAll(t *testing.T) {
	src := `
fn describe(u: Int?) -> String {
    match u {
        _ -> "any",
        Some(n) -> "some",
    }
}
`
	assertCodes(t, runCheck(t, src), diag.CodeUnreachableArm)
}

func TestCheck_UnreachableArmAfterFullVariantCover(t *testing.T) {
	// First `Some(_)` covers every Some value; second Some arm is dead.
	src := `
fn describe(u: Int?) -> String {
    match u {
        Some(_) -> "a",
        Some(5) -> "b",
        None -> "n",
    }
}
`
	assertCodes(t, runCheck(t, src), diag.CodeUnreachableArm)
}

func TestCheck_NumericLiteralOverflowUInt8(t *testing.T) {
	// 300 cannot fit in UInt8 (range 0..255) — §2.2.
	src := `
fn main() {
    let x: UInt8 = 300
}
`
	assertCodes(t, runCheck(t, src), diag.CodeNumericLitRange)
}

func TestCheck_NumericLiteralNegativeIntoUnsigned(t *testing.T) {
	src := `
fn main() {
    let x: UInt32 = -1
}
`
	assertCodes(t, runCheck(t, src), diag.CodeNumericLitRange)
}

func TestCheck_NumericLiteralFitsBounds(t *testing.T) {
	src := `
fn main() {
    let a: UInt8 = 255
    let b: Int8 = -128
    let c: Int32 = 2147483647
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_DeprecatedWarningEmitted(t *testing.T) {
	src := `
#[deprecated(since = "0.5", use = "newThing")]
fn oldThing() -> Int { 42 }

fn main() {
    let x = oldThing()
}
`
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) > 0 {
		t.Fatalf("parse errors: %v", parseDiags)
	}
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res)
	found := false
	for _, d := range chk.Diags {
		if d.Code == diag.CodeDeprecatedUse {
			found = true
			if !strings.Contains(d.Message, "since 0.5") {
				t.Errorf("expected `since 0.5` in message, got %q", d.Message)
			}
			if !strings.Contains(d.Message, "newThing") {
				t.Errorf("expected `newThing` in message, got %q", d.Message)
			}
		}
	}
	if !found {
		t.Fatalf("expected W0750 deprecation warning, got %v", chk.Diags)
	}
}

func TestCheck_AnnotationJSONBadKey(t *testing.T) {
	src := `
pub struct User {
    #[json(key = 42)]
    pub name: String,
}
`
	assertCodes(t, runCheck(t, src), diag.CodeUnknownAnnotation)
}

func TestCheck_AnnotationJSONUnknownArg(t *testing.T) {
	src := `
pub struct User {
    #[json(rename = "user_name")]
    pub name: String,
}
`
	assertCodes(t, runCheck(t, src), diag.CodeUnknownAnnotation)
}

func TestCheck_MethodReferenceHasFnType(t *testing.T) {
	// Method reference (no parens) returns a value of the method's
	// FnType with the receiver dropped.
	src := `
pub struct Greeter {
    pub name: String,

    pub fn greet(self) -> String {
        "hi"
    }
}

fn main() {
    let g = Greeter { name: "alice" }
    let f = g.greet
}
`
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) > 0 {
		t.Fatalf("parse errors: %v", parseDiags)
	}
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res)
	for _, d := range chk.Diags {
		if d.Severity == diag.Error {
			t.Fatalf("unexpected error: %s", d.Error())
		}
	}
	// Find the `g.greet` field-expr node (the second-to-last let's
	// value) and verify its recorded type is an FnType returning String.
	found := false
	for e, tp := range chk.Types {
		fx, ok := e.(*ast.FieldExpr)
		if !ok || fx.Name != "greet" {
			continue
		}
		found = true
		fn, ok := tp.(*types.FnType)
		if !ok {
			t.Fatalf("expected FnType for method ref, got %s", tp)
		}
		if fn.Return == nil || fn.Return.String() != "String" {
			t.Fatalf("expected return String, got %s", fn.Return)
		}
	}
	if !found {
		t.Fatal("no type recorded for the method-reference FieldExpr")
	}
}

func TestCheck_MethodReferenceUnknownErrors(t *testing.T) {
	src := `
pub struct Empty {}

fn main() {
    let e = Empty {}
    let f = e.nonexistent
}
`
	assertCodes(t, runCheck(t, src), diag.CodeUnknownField)
}

func TestCheck_BuilderHappyPath(t *testing.T) {
	src := `
pub struct HttpConfig {
    pub url: String,
    pub method: String = "GET",
    pub timeout: Int = 30,
}

fn main() {
    let cfg = HttpConfig.builder()
        .url("api.com")
        .build()
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_BuilderMissingRequiredField(t *testing.T) {
	src := `
pub struct HttpConfig {
    pub url: String,
    pub method: String = "GET",
}

fn main() {
    let cfg = HttpConfig.builder().build()
}
`
	assertCodes(t, runCheck(t, src), diag.CodeMissingStructField)
}

func TestCheck_BuilderUnknownSetter(t *testing.T) {
	src := `
pub struct HttpConfig {
    pub url: String = "",
}

fn main() {
    let cfg = HttpConfig.builder().nonexistent("x").build()
}
`
	assertCodes(t, runCheck(t, src), diag.CodeUnknownField)
}

func TestCheck_BuilderSetterTypeMismatch(t *testing.T) {
	src := `
pub struct HttpConfig {
    pub url: String,
}

fn main() {
    let cfg = HttpConfig.builder().url(42).build()
}
`
	assertCodes(t, runCheck(t, src), diag.CodeTypeMismatch)
}

func TestCheck_BuilderPrivateFieldNoDefaultBlocksBuilder(t *testing.T) {
	src := `
pub struct AuthToken {
    value: String,
    issuer: String,

    pub fn signAndCreate(payload: String) -> Self {
        Self { value: "a", issuer: "b" }
    }
}

fn main() {
    let t = AuthToken.builder()
}
`
	assertCodes(t, runCheck(t, src), diag.CodeUnknownMethod)
}

func TestCheck_BuilderToBuilderPreloaded(t *testing.T) {
	src := `
pub struct HttpConfig {
    pub url: String = "",
    pub timeout: Int = 30,
}

fn main() {
    let cfg = HttpConfig.builder().url("api.com").build()
    let variant = cfg.toBuilder().timeout(120).build()
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_GenericBuilderInfersTypeArgsFromSetter(t *testing.T) {
	src := `
pub struct Box<T> {
    pub value: T,
}

fn main() {
    let b: Box<Int> = Box.builder().value(1).build()
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_StructDefaultAllFieldsHaveDefaults(t *testing.T) {
	src := `
pub struct Settings {
    pub verbose: Bool = false,
    pub retries: Int = 3,
}

fn main() {
    let s = Settings.default()
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_MatchExhaustiveEnum(t *testing.T) {
	src := `
pub enum Color { Red, Green, Blue }

fn name(c: Color) -> String {
    match c {
        Red -> "r",
        Green -> "g",
        Blue -> "b",
    }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_MatchNonExhaustiveEnum(t *testing.T) {
	src := `
pub enum Color { Red, Green, Blue }

fn name(c: Color) -> String {
    match c {
        Red -> "r",
        Green -> "g",
    }
}
`
	assertCodes(t, runCheck(t, src), diag.CodeNonExhaustiveMatch)
}

func TestCheck_MatchEnumWildcardCovers(t *testing.T) {
	src := `
pub enum Color { Red, Green, Blue }

fn name(c: Color) -> String {
    match c {
        Red -> "r",
        _ -> "other",
    }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_MatchNonExhaustiveOption(t *testing.T) {
	src := `
fn describe(u: Int?) -> String {
    match u {
        Some(n) -> "got",
    }
}
`
	assertCodes(t, runCheck(t, src), diag.CodeNonExhaustiveMatch)
}

func TestCheck_MatchOptionOrPatternExhaustive(t *testing.T) {
	src := `
fn describe(b: Bool?) -> Int {
    match b {
        Some(_) | _ -> 0,
    }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_MatchGuardsRequireCatchAll(t *testing.T) {
	// Guarded arms don't contribute to exhaustiveness (§4.3.2) — even
	// though these two arms together conceptually cover Some and None,
	// the guard forces a catch-all.
	src := `
fn describe(u: Int?) -> String {
    match u {
        Some(n) if n > 0 -> "positive",
        None -> "missing",
    }
}
`
	assertCodes(t, runCheck(t, src), diag.CodeNonExhaustiveMatch)
}

func TestCheck_MatchGuardWitnessIgnoresGuardedArm(t *testing.T) {
	src := `
fn describe(u: Bool?) -> String {
    match u {
        Some(true) if true -> "guarded",
        None -> "missing",
    }
}
`
	got := runCheck(t, src)
	assertCodes(t, got, diag.CodeNonExhaustiveMatch)
	if !strings.Contains(got[0].Message, "Some") {
		t.Fatalf("expected guarded-arm witness to mention `Some`, got: %s", got[0].Message)
	}
}

func TestCheck_MatchBoolExhaustive(t *testing.T) {
	src := `
fn flag(b: Bool) -> String {
    match b {
        true -> "on",
        false -> "off",
    }
}
`
	assertOK(t, runCheck(t, src))
}

func TestCheck_MatchScalarRequiresCatchAll(t *testing.T) {
	src := `
fn stringify(n: Int) -> String {
    match n {
        0 -> "zero",
        1 -> "one",
    }
}
`
	assertCodes(t, runCheck(t, src), diag.CodeNonExhaustiveMatch)
}

func TestCheck_GenericRecordsInstantiation(t *testing.T) {
	// The checker records the concrete type arguments on
	// Result.Instantiations so the transpiler can emit one
	// monomorphized copy per distinct instantiation.
	src := `
fn identity<T>(x: T) -> T { x }

fn main() {
    let a: Int = identity(5)
    let b: String = identity("hi")
}
`
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) > 0 {
		t.Fatalf("parse errors: %v", parseDiags)
	}
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res)
	if got := len(chk.Instantiations); got < 2 {
		t.Fatalf("expected ≥2 instantiation records, got %d", got)
	}
	seen := map[string]bool{}
	for _, args := range chk.Instantiations {
		if len(args) == 1 {
			seen[args[0].String()] = true
		}
	}
	if !seen["Int"] || !seen["String"] {
		t.Fatalf("expected instantiations for Int and String, got %v", seen)
	}
}

func TestCheck_TurbofishWrongTypeArgCount(t *testing.T) {
	src := `
fn identity<T>(x: T) -> T { x }

fn main() {
    let x = identity::<Int, String>(1)
}
`
	assertCodes(t, runCheck(t, src), diag.CodeGenericArgCount)
}

// TestCheck_MatchEnumVariantPayloadGap verifies that even when every
// top-level variant of an enum is matched, an uncovered sub-pattern
// inside a variant's payload still produces E0731. Before this was
// fixed, `Circle(true)` + `Rect` was incorrectly accepted as covering
// `Shape`.
func TestCheck_MatchEnumVariantPayloadGap(t *testing.T) {
	src := `
pub enum Shape {
    Circle(Bool),
    Rect,
}

fn area(s: Shape) -> Int {
    match s {
        Circle(true) -> 1,
        Rect -> 0,
    }
}
`
	got := runCheck(t, src)
	assertCodes(t, got, diag.CodeNonExhaustiveMatch)
	if !strings.Contains(got[0].Message, "Circle(false)") {
		t.Fatalf("expected witness `Circle(false)` in message, got: %s", got[0].Message)
	}
}

// TestCheck_MatchEnumNestedEnumPayload exercises coverage inside an
// enum-typed payload of another enum variant.
func TestCheck_MatchEnumNestedEnumPayload(t *testing.T) {
	src := `
pub enum Color { Red, Green, Blue }
pub enum Thing { Paint(Color), Plain }

fn name(t: Thing) -> Int {
    match t {
        Paint(Red) -> 1,
        Plain -> 0,
    }
}
`
	got := runCheck(t, src)
	assertCodes(t, got, diag.CodeNonExhaustiveMatch)
	if !strings.Contains(got[0].Message, "Paint(") {
		t.Fatalf("expected witness naming Paint(...), got: %s", got[0].Message)
	}
}

// TestCheck_MatchOptionPayloadGap checks that coverage descends into
// the Some payload even when top-level Some/None are both present.
func TestCheck_MatchOptionPayloadGap(t *testing.T) {
	src := `
fn describe(b: Bool?) -> Int {
    match b {
        Some(true) -> 1,
        None -> 0,
    }
}
`
	got := runCheck(t, src)
	assertCodes(t, got, diag.CodeNonExhaustiveMatch)
	if !strings.Contains(got[0].Message, "Some(false)") {
		t.Fatalf("expected witness `Some(false)`, got: %s", got[0].Message)
	}
}

// TestCheck_MatchResultPayloadGap checks coverage inside Ok/Err
// payloads when both top-level variants are present.
func TestCheck_MatchResultPayloadGap(t *testing.T) {
	src := `
pub enum Status { On, Off }

fn go(r: Result<Status, String>) -> Int {
    match r {
        Ok(On) -> 1,
        Err(_) -> 0,
    }
}
`
	got := runCheck(t, src)
	assertCodes(t, got, diag.CodeNonExhaustiveMatch)
	if !strings.Contains(got[0].Message, "Ok(Off)") {
		t.Fatalf("expected witness `Ok(Off)`, got: %s", got[0].Message)
	}
}
