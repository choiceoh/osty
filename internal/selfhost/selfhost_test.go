package selfhost

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
)

func TestParseAdvancedSelfhostSurface(t *testing.T) {
	src := []byte(`
pub enum Shape {
    #[json(key = "circle")]
    Circle(Float),
    Empty,
}

fn main() {
    let raw: json.Json = json.Object({})
    let decoded = json.decode::<error.BasicError>("null")
    let newer = User { ..user, age: 31 }
    let handler = |(left, right), Ok(value)| value
}
`)
	_, diags := Parse(src)
	if hasError(diags) {
		t.Fatalf("Parse returned diagnostics:\n%s", diagnosticsText(diags))
	}
}

func TestLexEscapedUnicodeDoesNotBecomeUnknownEscape(t *testing.T) {
	_, diags, _ := Lex([]byte(`"\\uD800"`))
	for _, d := range diags {
		if strings.Contains(d.Message, "unknown escape") {
			t.Fatalf("escaped unicode marker reported as unknown escape: %s", d.Error())
		}
	}
}

func TestParseDiagnosticsIncludesLexerDiagnostics(t *testing.T) {
	diags := ParseDiagnostics([]byte(`"\q"`))
	for _, d := range diags {
		if strings.Contains(d.Message, "unknown escape") {
			return
		}
	}
	t.Fatalf("ParseDiagnostics did not include lexer diagnostic:\n%s", diagnosticsText(diags))
}

func TestFrontendRunExposesSharedSurfaces(t *testing.T) {
	run := Run([]byte("fn main() { 1 }\n"))
	if len(run.Tokens()) == 0 {
		t.Fatal("Run returned no tokens")
	}
	if run.File() == nil {
		t.Fatal("Run returned nil file")
	}
	if hasError(run.Diagnostics()) {
		t.Fatalf("Run returned diagnostics:\n%s", diagnosticsText(run.Diagnostics()))
	}
	if len(run.LexDiagnostics()) != 0 {
		t.Fatalf("Run returned lexer diagnostics:\n%s", diagnosticsText(run.LexDiagnostics()))
	}
}

func TestCheckSourceUsesBootstrappedChecker(t *testing.T) {
	good := CheckSourceStructured([]byte(`
struct Box<T> { value: T }

fn id<T>(value: T) -> T { value }

fn main() {
    let boxed: Box<Int> = Box { value: id::<Int>(1) }
}
`))
	if good.Summary.Errors != 0 {
		t.Fatalf("CheckSourceStructured returned %d errors for valid generic source", good.Summary.Errors)
	}
	if good.Summary.Assignments == 0 || good.Summary.Accepted == 0 {
		t.Fatalf("CheckSourceStructured did not report assignment checks: %+v", good.Summary)
	}
	if len(good.TypedNodes) == 0 {
		t.Fatal("CheckSourceStructured returned no typed nodes")
	}
	if !hasCheckedBinding(good.Bindings, "boxed", "Box<Int>") {
		t.Fatalf("CheckSourceStructured missed boxed binding: %+v", good.Bindings)
	}
	if !hasCheckedSymbol(good.Symbols, "struct", "Box") || !hasCheckedSymbol(good.Symbols, "fn", "id") {
		t.Fatalf("CheckSourceStructured missed declaration symbols: %+v", good.Symbols)
	}
	if !hasCheckInstantiation(good.Instantiations, "id", "Int") {
		t.Fatalf("CheckSourceStructured missed generic instantiation: %+v", good.Instantiations)
	}

	bad := CheckSource([]byte(`
enum Color { Red, Green }

fn code(color: Color) -> Int {
    match color {
        Red -> 1,
    }
}
`))
	if bad.Errors == 0 {
		t.Fatalf("CheckSource missed a non-exhaustive match: %+v", bad)
	}
}

func hasError(diags []*diag.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == diag.Error {
			return true
		}
	}
	return false
}

func hasCheckedBinding(bindings []CheckedBinding, name, typeName string) bool {
	for _, binding := range bindings {
		if binding.Name == name && binding.TypeName == typeName {
			return true
		}
	}
	return false
}

func hasCheckedSymbol(symbols []CheckedSymbol, kind, name string) bool {
	for _, symbol := range symbols {
		if symbol.Kind == kind && symbol.Name == name {
			return true
		}
	}
	return false
}

func hasCheckInstantiation(instantiations []CheckInstantiation, callee, typeArg string) bool {
	for _, inst := range instantiations {
		if inst.Callee != callee {
			continue
		}
		for _, arg := range inst.TypeArgs {
			if arg == typeArg {
				return true
			}
		}
	}
	return false
}

func diagnosticsText(diags []*diag.Diagnostic) string {
	if len(diags) == 0 {
		return "<none>"
	}
	var b strings.Builder
	for _, d := range diags {
		b.WriteString(d.Error())
		b.WriteByte('\n')
	}
	return b.String()
}
