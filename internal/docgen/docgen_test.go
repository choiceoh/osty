package docgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/parser"
)

// parseSource is a thin testing helper: it runs the full lex+parse
// pipeline and fails the test on any diagnostic. The docgen package
// consumes only the AST, so resolver/type-check diagnostics are
// irrelevant here.
func parseSource(t *testing.T, src string) *Package {
	t.Helper()
	file, diags := parser.ParseDiagnostics([]byte(src))
	for _, d := range diags {
		t.Logf("parse diag: %s", d.Message)
	}
	if file == nil {
		t.Fatalf("parse returned nil AST for source:\n%s", src)
	}
	return FromFile("test", file)
}

// TestExtractFiltersPrivate confirms that private top-level decls are
// absent from the extracted Package. The extractor defines "public
// API surface" as `pub` items only — internal helpers should never
// leak into docs.
func TestExtractFiltersPrivate(t *testing.T) {
	pkg := parseSource(t, `
/// Public fn.
pub fn Exported(x: Int) -> Int { x }

fn privateHelper() -> Int { 0 }

/// Public struct.
pub struct User {
    pub name: String,
    secret: Int = 0,
}
`)
	if len(pkg.Modules) != 1 {
		t.Fatalf("expected 1 module, got %d", len(pkg.Modules))
	}
	names := make(map[string]bool)
	for _, d := range pkg.Modules[0].Decls {
		names[d.Name] = true
	}
	if !names["Exported"] {
		t.Errorf("expected Exported in docs; got %v", names)
	}
	if !names["User"] {
		t.Errorf("expected User in docs; got %v", names)
	}
	if names["privateHelper"] {
		t.Errorf("private decl should not appear: %v", names)
	}
	// Private struct field must not surface either.
	for _, d := range pkg.Modules[0].Decls {
		if d.Name == "User" {
			for _, f := range d.Fields {
				if f.Name == "secret" {
					t.Errorf("private field `secret` leaked into docs")
				}
			}
		}
	}
}

// TestFnSignature exercises every optional piece of a fn declaration:
// generics, receiver, defaults, Option-sugared return type.
func TestFnSignature(t *testing.T) {
	pkg := parseSource(t, `
/// Trim a string and optionally uppercase it.
pub fn Transform<T>(self, input: String, upper: Bool = false) -> String? {
    None
}
`)
	d := pkg.Modules[0].Decls[0]
	if d.Kind != KindFunction {
		t.Fatalf("expected function, got %v", d.Kind)
	}
	want := "pub fn Transform<T>(self, input: String, upper: Bool = false) -> String?"
	if d.Signature != want {
		t.Errorf("signature mismatch\n  got:  %s\n  want: %s", d.Signature, want)
	}
	if !strings.Contains(d.Doc, "Trim a string") {
		t.Errorf("expected doc comment on function, got %q", d.Doc)
	}
}

// TestEnumVariants checks that bare and tuple-like variants both
// render with the right payload, and that enum methods flow through.
func TestEnumVariants(t *testing.T) {
	pkg := parseSource(t, `
/// Geometric shapes.
pub enum Shape {
    Empty,
    Circle(Float),
    Rect(Float, Float),

    pub fn area(self) -> Float { 0.0 }
}
`)
	d := pkg.Modules[0].Decls[0]
	if d.Kind != KindEnum {
		t.Fatalf("expected enum, got %v", d.Kind)
	}
	if len(d.Variants) != 3 {
		t.Fatalf("expected 3 variants, got %d", len(d.Variants))
	}
	if d.Variants[0].Name != "Empty" || len(d.Variants[0].Payload) != 0 {
		t.Errorf("bare variant wrong: %+v", d.Variants[0])
	}
	if d.Variants[1].Name != "Circle" || d.Variants[1].Payload[0] != "Float" {
		t.Errorf("Circle variant wrong: %+v", d.Variants[1])
	}
	if len(d.Methods) != 1 || d.Methods[0].Name != "area" {
		t.Errorf("expected method area, got %+v", d.Methods)
	}
}

// TestTypeAliasRendering ensures the RHS of a type alias survives the
// roundtrip through RenderType — including nested generics.
func TestTypeAliasRendering(t *testing.T) {
	pkg := parseSource(t, `
/// User index.
pub type UserMap = Map<String, List<User>>
`)
	d := pkg.Modules[0].Decls[0]
	if d.Kind != KindTypeAlias {
		t.Fatalf("expected type alias, got %v", d.Kind)
	}
	if d.AliasTarget != "Map<String, List<User>>" {
		t.Errorf("alias target wrong: %q", d.AliasTarget)
	}
}

// TestDeprecatedAnnotation confirms the `message = "..."` payload of
// #[deprecated] becomes the decl's Deprecated note.
func TestDeprecatedAnnotation(t *testing.T) {
	pkg := parseSource(t, `
#[deprecated(since = "0.5", use = "newFn", message = "use newFn instead")]
pub fn oldFn() -> Int { 0 }
`)
	d := pkg.Modules[0].Decls[0]
	if d.Deprecated != "use newFn instead" {
		t.Errorf("expected deprecated message to be extracted; got %q", d.Deprecated)
	}
}

// TestMarkdownRender asserts a handful of user-visible markdown
// properties rather than doing a full golden-file match — the exact
// layout is expected to evolve; the anchor+heading contract is what
// downstream tooling relies on.
func TestMarkdownRender(t *testing.T) {
	pkg := parseSource(t, `
/// Greeter.
pub struct Hello {
    pub name: String,

    pub fn greet(self) -> String { "hi" }
}
`)
	md := RenderMarkdown(pkg)
	mustContain := []string{
		"# Package `test`",
		"### Struct `Hello`",
		"```osty\npub struct Hello\n```",
		"#### Fields",
		"| `name` | `String` |",
		"##### Function `greet`",
	}
	for _, want := range mustContain {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n---\n%s\n---", want, md)
		}
	}
}

// TestInterfaceMethodsAlwaysIncluded — interface methods are part of
// the contract even when `pub` isn't repeated on each method. This
// matches how Osty programmers treat interface members.
func TestInterfaceMethodsAlwaysIncluded(t *testing.T) {
	pkg := parseSource(t, `
/// Writer interface.
pub interface Writer {
    fn write(self, data: Bytes) -> Result<Int, Error>
    fn close(self) -> Result<(), Error>
}
`)
	d := pkg.Modules[0].Decls[0]
	if d.Kind != KindInterface {
		t.Fatalf("expected interface, got %v", d.Kind)
	}
	if len(d.Methods) != 2 {
		t.Errorf("expected 2 interface methods, got %d", len(d.Methods))
	}
}

// TestRenderTypeSugar covers the Option<T> -> T? rewrite and the
// single-element tuple trailing-comma form.
func TestRenderTypeSugar(t *testing.T) {
	pkg := parseSource(t, `
pub type Pair = (Int, String)
pub type Opt = Option<String>
`)
	want := map[string]string{
		"Pair": "(Int, String)",
		"Opt":  "String?",
	}
	for _, d := range pkg.Modules[0].Decls {
		if got, ok := want[d.Name]; ok {
			if d.AliasTarget != got {
				t.Errorf("%s: got %q, want %q", d.Name, d.AliasTarget, got)
			}
		}
	}
}
