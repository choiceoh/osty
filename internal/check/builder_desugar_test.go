package check

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

func parseOrFatal(t *testing.T, src string) *ast.File {
	t.Helper()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse diags: %v", parseDiags)
	}
	return file
}

// findLetValue returns the Value expression of the first let with the
// given name found anywhere in the file's top-level fn bodies.
func findLetValue(f *ast.File, name string) ast.Expr {
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FnDecl)
		if !ok || fn.Body == nil {
			continue
		}
		for _, s := range fn.Body.Stmts {
			ls, ok := s.(*ast.LetStmt)
			if !ok {
				continue
			}
			if id, ok := ls.Pattern.(*ast.IdentPat); ok && id.Name == name {
				return ls.Value
			}
		}
	}
	return nil
}

// assertStructLit asserts the expr is a *ast.StructLit naming typeName
// with exactly the given field/value raw-text pairs in order.
func assertStructLit(t *testing.T, e ast.Expr, typeName string, wantFields [][2]string) {
	t.Helper()
	lit, ok := e.(*ast.StructLit)
	if !ok {
		t.Fatalf("expected *ast.StructLit, got %T (%v)", e, e)
	}
	gotName, _ := identName(lit.Type)
	if gotName != typeName {
		t.Fatalf("struct literal type = %q, want %q", gotName, typeName)
	}
	if got, want := len(lit.Fields), len(wantFields); got != want {
		t.Fatalf("literal has %d fields, want %d", got, want)
	}
	for i, f := range lit.Fields {
		if f.Name != wantFields[i][0] {
			t.Errorf("field[%d] name = %q, want %q", i, f.Name, wantFields[i][0])
		}
		if got := rawText(f.Value); got != wantFields[i][1] {
			t.Errorf("field[%d] value = %q, want %q", i, got, wantFields[i][1])
		}
	}
}

// rawText returns a minimal source-ish representation of an Expr for
// test assertions. Only IntLit / StringLit / Ident are supported —
// enough for the builder tests below.
func rawText(e ast.Expr) string {
	switch n := e.(type) {
	case *ast.IntLit:
		return n.Text
	case *ast.StringLit:
		var sb strings.Builder
		for _, p := range n.Parts {
			if p.IsLit {
				sb.WriteString(p.Lit)
			}
		}
		return sb.String()
	case *ast.Ident:
		return n.Name
	}
	return ""
}

// ----- Positive cases: chain rewrites to struct literal -----

func TestDesugarBuilderAllFieldsSet(t *testing.T) {
	src := `
pub struct Point {
    pub x: Int,
    pub y: Int,
}

fn main() {
    let p = Point.builder().x(3).y(4).build()
}
`
	f := parseOrFatal(t, src)
	diags := DesugarBuildersInFile(f)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	val := findLetValue(f, "p")
	assertStructLit(t, val, "Point", [][2]string{
		{"x", "3"}, {"y", "4"},
	})
}

func TestDesugarPreservesDeclarationFieldOrder(t *testing.T) {
	// User calls setters in reverse order; rewrite must follow struct
	// declaration order so the generated StructLit is canonical.
	src := `
pub struct Point {
    pub x: Int,
    pub y: Int,
}

fn main() {
    let p = Point.builder().y(20).x(10).build()
}
`
	f := parseOrFatal(t, src)
	diags := DesugarBuildersInFile(f)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	val := findLetValue(f, "p")
	assertStructLit(t, val, "Point", [][2]string{
		{"x", "10"}, {"y", "20"},
	})
}

func TestDesugarDefaultedPubFieldNotRequired(t *testing.T) {
	// `name` has a default, so it is optional; `count` is required.
	src := `
pub struct Tag {
    pub name: String = "anon",
    pub count: Int,
}

fn main() {
    let t = Tag.builder().count(5).build()
}
`
	f := parseOrFatal(t, src)
	diags := DesugarBuildersInFile(f)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	val := findLetValue(f, "t")
	assertStructLit(t, val, "Tag", [][2]string{
		{"count", "5"},
	})
}

func TestDesugarNestedInArg(t *testing.T) {
	src := `
pub struct Point { pub x: Int, pub y: Int }

fn consume(p: Point) {}

fn main() {
    consume(Point.builder().x(1).y(2).build())
}
`
	f := parseOrFatal(t, src)
	diags := DesugarBuildersInFile(f)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	// Find the call to consume and assert its first arg is now a StructLit.
	var consumeCall *ast.CallExpr
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FnDecl)
		if !ok || fn.Name != "main" {
			continue
		}
		for _, s := range fn.Body.Stmts {
			es, ok := s.(*ast.ExprStmt)
			if !ok {
				continue
			}
			if call, ok := es.X.(*ast.CallExpr); ok {
				consumeCall = call
			}
		}
	}
	if consumeCall == nil {
		t.Fatal("consume call not found")
	}
	if len(consumeCall.Args) != 1 {
		t.Fatalf("consume call has %d args, want 1", len(consumeCall.Args))
	}
	assertStructLit(t, consumeCall.Args[0].Value, "Point", [][2]string{
		{"x", "1"}, {"y", "2"},
	})
}

// ----- Negative cases: G9 diagnostic -----

func TestDesugarMissingRequiredFieldEmitsE0774(t *testing.T) {
	src := `
pub struct Point {
    pub x: Int,
    pub y: Int,
}

fn main() {
    let p = Point.builder().x(3).build()
}
`
	f := parseOrFatal(t, src)
	diags := DesugarBuildersInFile(f)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(diags), diags)
	}
	d := diags[0]
	if d.Code != diag.CodeBuilderMissingRequiredField {
		t.Errorf("diag code = %q, want %q", d.Code, diag.CodeBuilderMissingRequiredField)
	}
	if !strings.Contains(d.Message, "y") {
		t.Errorf("diag message %q should name missing field `y`", d.Message)
	}
	if !strings.Contains(d.Message, "Point") {
		t.Errorf("diag message %q should name struct `Point`", d.Message)
	}
}

func TestDesugarMultipleMissingFieldsListedInDeclOrder(t *testing.T) {
	src := `
pub struct Rect {
    pub w: Int,
    pub h: Int,
    pub color: String,
}

fn main() {
    let r = Rect.builder().build()
}
`
	f := parseOrFatal(t, src)
	diags := DesugarBuildersInFile(f)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %v", len(diags), diags)
	}
	// All three missing, listed in declaration order.
	msg := diags[0].Message
	iw := strings.Index(msg, "w")
	ih := strings.Index(msg, "h")
	ic := strings.Index(msg, "color")
	if iw < 0 || ih < 0 || ic < 0 {
		t.Fatalf("diag %q should list w, h, color", msg)
	}
	if !(iw < ih && ih < ic) {
		t.Errorf("diag %q should list missing fields in declaration order (w < h < color)", msg)
	}
}

// ----- Edges: chains that are NOT builder patterns must be left alone -----

func TestDesugarLeavesUnrelatedCallUntouched(t *testing.T) {
	// `Other` is not declared in the file — must not rewrite.
	src := `
fn main() {
    let x = Other.builder().foo(1).build()
}
`
	f := parseOrFatal(t, src)
	diags := DesugarBuildersInFile(f)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics for unknown struct: %v", diags)
	}
	// Value must still be a *ast.CallExpr, not a StructLit.
	val := findLetValue(f, "x")
	if _, ok := val.(*ast.CallExpr); !ok {
		t.Fatalf("unknown struct should not be rewritten; got %T", val)
	}
}

func TestDesugarLeavesNonBuildChainUntouched(t *testing.T) {
	// No `.build()` terminator — the desugarer must not fire.
	src := `
pub struct Point { pub x: Int, pub y: Int }

fn main() {
    let p = Point.builder().x(3).y(4)
}
`
	f := parseOrFatal(t, src)
	diags := DesugarBuildersInFile(f)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	val := findLetValue(f, "p")
	if _, ok := val.(*ast.StructLit); ok {
		t.Fatal("chain without .build() must not be rewritten to StructLit")
	}
}

func TestDesugarHandlesNilFile(t *testing.T) {
	if got := DesugarBuildersInFile(nil); got != nil {
		t.Errorf("nil file should return nil diagnostics, got %v", got)
	}
}

// ----- End-to-end: check.File runs the desugaring as a pre-pass -----

// TestCheckFileDesugarsBuilderChain confirms that the full check.File
// entry point invokes the builder desugarer so a valid chain is no
// longer visible as a `.builder()...build()` call by the time the
// rest of the checker sees the file — it is a plain struct literal.
func TestCheckFileDesugarsBuilderChain(t *testing.T) {
	src := `
pub struct Point {
    pub x: Int,
    pub y: Int,
}

fn main() {
    let p = Point.builder().x(3).y(4).build()
}
`
	f := parseOrFatal(t, src)
	reg := stdlib.LoadCached()
	res := resolve.FileWithStdlib(f, resolve.NewPrelude(), reg)
	chk := File(f, res, Opts{
		Source:     []byte(src),
		Stdlib:     reg,
		Primitives: reg.Primitives,
	})
	for _, d := range chk.Diags {
		if d.Code == diag.CodeBuilderMissingRequiredField {
			t.Errorf("unexpected E0774 for valid chain: %v", d)
		}
	}
	val := findLetValue(f, "p")
	if _, ok := val.(*ast.StructLit); !ok {
		t.Fatalf("check.File should have rewritten the chain to a StructLit, got %T", val)
	}
}

// TestCheckFileSurfacesE0774 verifies that the missing-required-field
// diagnostic produced by the desugarer reaches the top-level
// Result.Diags slice that callers consume.
func TestCheckFileSurfacesE0774(t *testing.T) {
	src := `
pub struct Point {
    pub x: Int,
    pub y: Int,
}

fn main() {
    let p = Point.builder().x(3).build()
}
`
	f := parseOrFatal(t, src)
	reg := stdlib.LoadCached()
	res := resolve.FileWithStdlib(f, resolve.NewPrelude(), reg)
	chk := File(f, res, Opts{
		Source:     []byte(src),
		Stdlib:     reg,
		Primitives: reg.Primitives,
	})
	found := false
	for _, d := range chk.Diags {
		if d.Code == diag.CodeBuilderMissingRequiredField {
			found = true
			if !strings.Contains(d.Message, "y") {
				t.Errorf("E0774 should name missing field `y`, got: %q", d.Message)
			}
		}
	}
	if !found {
		t.Errorf("check.File did not surface E0774; diags=%v", chk.Diags)
	}
}
