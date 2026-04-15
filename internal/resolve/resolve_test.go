package resolve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
)

// resolveSrc parses src and runs the resolver on it, returning the result.
// Test failures use t.Fatalf so callers can rely on a non-nil result.
func resolveSrc(t *testing.T, src string) *Result {
	t.Helper()
	file, parseErrs := parser.ParseDiagnostics([]byte(src))
	if len(parseErrs) > 0 {
		var msgs []string
		for _, e := range parseErrs {
			msgs = append(msgs, e.Error())
		}
		t.Fatalf("unexpected parse errors:\n  %s\nsource:\n%s",
			strings.Join(msgs, "\n  "), src)
	}
	return File(file, NewPrelude())
}

// findDiag returns the first diagnostic with the given code, or nil.
func findDiag(diags []*diag.Diagnostic, code string) *diag.Diagnostic {
	for _, d := range diags {
		if d.Code == code {
			return d
		}
	}
	return nil
}

func TestPreludeNamesVisible(t *testing.T) {
	res := resolveSrc(t, `fn f() {
    let s: String = "hi"
    println(s)
}`)
	if len(res.Diags) != 0 {
		t.Fatalf("unexpected diags: %v", res.Diags)
	}
	// `println` and `s` are Idents; `String` is a NamedType.
	wantIdent := map[string]SymbolKind{
		"println": SymBuiltin,
		"s":       SymLet,
	}
	gotIdent := map[string]SymbolKind{}
	for id, sym := range res.Refs {
		if _, ok := wantIdent[id.Name]; ok {
			gotIdent[id.Name] = sym.Kind
		}
	}
	for n, k := range wantIdent {
		if gotIdent[n] != k {
			t.Errorf("ident %q kind = %v; want %v", n, gotIdent[n], k)
		}
	}
	// `String` should appear in TypeRefs as SymBuiltin.
	gotString := false
	for nt, sym := range res.TypeRefs {
		if len(nt.Path) > 0 && nt.Path[0] == "String" && sym.Kind == SymBuiltin {
			gotString = true
		}
	}
	if !gotString {
		t.Error("String type ref not resolved")
	}
}

func TestUndefinedName(t *testing.T) {
	res := resolveSrc(t, `fn f() { let x = unknownThing }`)
	d := findDiag(res.Diags, diag.CodeUndefinedName)
	if d == nil {
		t.Fatalf("no E0500; got %v", res.Diags)
	}
}

func TestUndefinedNameSuggestion(t *testing.T) {
	res := resolveSrc(t, `fn f() { let x = printlng("x") }`)
	d := findDiag(res.Diags, diag.CodeUndefinedName)
	if d == nil {
		t.Fatal("no E0500")
	}
	if !strings.Contains(d.Hint, "println") {
		t.Errorf("hint should suggest `println`, got %q", d.Hint)
	}
}

func TestDuplicateTopLevel(t *testing.T) {
	res := resolveSrc(t, `fn x() {}
fn x() {}`)
	d := findDiag(res.Diags, diag.CodeDuplicateDecl)
	if d == nil {
		t.Fatal("no E0501")
	}
}

func TestDuplicateLetSameBlock(t *testing.T) {
	res := resolveSrc(t, `fn f() {
    let x = 1
    let x = 2
}`)
	d := findDiag(res.Diags, diag.CodeDuplicateDecl)
	if d == nil {
		t.Fatal("no E0501 for duplicate let in same block")
	}
}

func TestShadowingInChildBlock(t *testing.T) {
	// Shadowing across nested blocks IS allowed.
	res := resolveSrc(t, `fn f() {
    let x = 1
    {
        let x = 2
        println("{x}")
    }
}`)
	if len(res.Diags) != 0 {
		t.Fatalf("expected no diags, got %v", res.Diags)
	}
}

func TestForwardReferenceBetweenFns(t *testing.T) {
	// `b` references `a` which is declared later — should resolve.
	res := resolveSrc(t, `fn b() { a() }
fn a() {}`)
	if len(res.Diags) != 0 {
		t.Fatalf("forward ref should resolve; got %v", res.Diags)
	}
}

func TestSelfInsideMethod(t *testing.T) {
	res := resolveSrc(t, `pub struct U {
    pub n: Int,
    pub fn m(self) -> Int { self.n }
}`)
	if len(res.Diags) != 0 {
		t.Fatalf("expected no diags, got %v", res.Diags)
	}
}

func TestSelfOutsideMethod(t *testing.T) {
	res := resolveSrc(t, `fn f() { self }`)
	if findDiag(res.Diags, diag.CodeSelfOutsideMethod) == nil {
		t.Fatal("expected E0503")
	}
}

func TestSelfTypeOutside(t *testing.T) {
	res := resolveSrc(t, `fn f() -> Self { 1 }`)
	if findDiag(res.Diags, diag.CodeSelfTypeOutside) == nil {
		t.Fatal("expected E0504")
	}
}

func TestSelfTypeInsideStruct(t *testing.T) {
	res := resolveSrc(t, `pub struct U {
    pub n: Int,
    pub fn new(n: Int) -> Self { Self { n } }
}`)
	if len(res.Diags) != 0 {
		t.Fatalf("expected no diags, got %v", res.Diags)
	}
}

func TestEnumVariantBareReference(t *testing.T) {
	// `Circle` and `Rect` should be visible bare in match arms because
	// they are enum variants of the same package's enum.
	res := resolveSrc(t, `pub enum Shape {
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
}`)
	if len(res.Diags) != 0 {
		t.Fatalf("expected no diags, got %v", res.Diags)
	}
}

func TestUseAlias(t *testing.T) {
	res := resolveSrc(t, `use std.fs
use std.http as web
fn f() {
    let x = fs
    let y = web
}`)
	if len(res.Diags) != 0 {
		t.Fatalf("expected no diags, got %v", res.Diags)
	}
	// The `fs` reference should resolve to a SymPackage.
	for id, sym := range res.Refs {
		if id.Name == "fs" && sym.Kind != SymPackage {
			t.Errorf("fs kind = %v; want SymPackage", sym.Kind)
		}
		if id.Name == "web" && sym.Kind != SymPackage {
			t.Errorf("web kind = %v; want SymPackage", sym.Kind)
		}
	}
}

func TestUseGoFFIAlias(t *testing.T) {
	res := resolveSrc(t, `use go "net/http" as http {
    fn Get(u: String) -> String
}
fn f() { let x = http }`)
	if len(res.Diags) != 0 {
		t.Fatalf("expected no diags, got %v", res.Diags)
	}
}

func TestGenericParamScope(t *testing.T) {
	res := resolveSrc(t, `fn first<T>(xs: List<T>) -> T {
    xs.get(0)
}`)
	if len(res.Diags) != 0 {
		t.Fatalf("expected no diags (T should resolve in signature & body), got %v", res.Diags)
	}
}

func TestMatchArmBindings(t *testing.T) {
	res := resolveSrc(t, `pub enum Result2 {
    Ok2(Int),
    Err2(String),
}
fn f(r: Result2) -> Int {
    match r {
        Ok2(n) -> n,
        Err2(_) -> 0,
    }
}`)
	if len(res.Diags) != 0 {
		t.Fatalf("expected no diags, got %v", res.Diags)
	}
}

func TestForLoopBinding(t *testing.T) {
	res := resolveSrc(t, `fn f(xs: List<Int>) -> Int {
    let mut total = 0
    for i in xs {
        total = total + i
    }
    total
}`)
	if len(res.Diags) != 0 {
		t.Fatalf("expected no diags, got %v", res.Diags)
	}
}

func TestIfLetBinding(t *testing.T) {
	res := resolveSrc(t, `fn f(u: Int?) -> Int {
    if let Some(n) = u {
        n
    } else {
        0
    }
}`)
	if len(res.Diags) != 0 {
		t.Fatalf("expected no diags, got %v", res.Diags)
	}
}

func TestClosurePatternBinding(t *testing.T) {
	// SPEC_GAPS G4: closure with a destructuring tuple pattern.
	res := resolveSrc(t, `fn f(xs: List<Int>) {
    xs.map(|(a, b)| a + b)
}`)
	if len(res.Diags) != 0 {
		t.Fatalf("expected no diags, got %v", res.Diags)
	}
}

func TestStringInterpolationResolved(t *testing.T) {
	res := resolveSrc(t, `fn f() {
    let name = "alice"
    println("hi, {name}!")
}`)
	if len(res.Diags) != 0 {
		t.Fatalf("expected no diags, got %v", res.Diags)
	}
	// Verify that the embedded `name` Ident was actually resolved.
	gotName := false
	for id, sym := range res.Refs {
		if id.Name == "name" && sym.Kind == SymLet {
			gotName = true
		}
	}
	if !gotName {
		t.Error("interpolated `name` was not resolved")
	}
}

func TestWrongSymbolKindInTypePosition(t *testing.T) {
	// Using a function name where a type is expected should error.
	res := resolveSrc(t, `fn helper() {}
fn f() -> helper { 1 }`)
	d := findDiag(res.Diags, diag.CodeWrongSymbolKind)
	if d == nil {
		t.Fatal("expected E0502")
	}
}

func TestResolvableFixtureFile(t *testing.T) {
	// testdata/resolve_ok.osty is intentionally self-contained — every
	// name used in it must resolve. Regressions here usually indicate a
	// scope-handling bug.
	src, err := readTestdata(t, "resolve_ok.osty")
	if err != nil {
		t.Skip(err)
	}
	res := resolveSrc(t, string(src))
	if len(res.Diags) > 0 {
		for _, d := range res.Diags {
			t.Errorf("unexpected diag: %s", d.Error())
		}
	}
}

// readTestdata locates testdata/<name> by walking up to the project root.
func readTestdata(t *testing.T, name string) ([]byte, error) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	for {
		path := filepath.Join(dir, "testdata", name)
		if b, err := os.ReadFile(path); err == nil {
			return b, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, os.ErrNotExist
		}
		dir = parent
	}
}

// Compile-time guard that ast.Ident is the AST node we resolve refs for.
var _ = (*ast.Ident)(nil)
