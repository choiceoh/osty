package selfhost

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
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
	if hasError(run.Diagnostics()) {
		t.Fatalf("Run returned diagnostics:\n%s", diagnosticsText(run.Diagnostics()))
	}
	if len(run.LexDiagnostics()) != 0 {
		t.Fatalf("Run returned lexer diagnostics:\n%s", diagnosticsText(run.LexDiagnostics()))
	}
}

func TestFrontendRunLowersInterpolatedExprWithFullParser(t *testing.T) {
	file, diags := Parse([]byte(`fn main() { let s = "value {items[0] + 1}" }`))
	if hasError(diags) {
		t.Fatalf("Parse returned diagnostics:\n%s", diagnosticsText(diags))
	}
	fn, ok := file.Decls[0].(*ast.FnDecl)
	if !ok {
		t.Fatalf("decl 0 = %T", file.Decls[0])
	}
	let, ok := fn.Body.Stmts[0].(*ast.LetStmt)
	if !ok {
		t.Fatalf("stmt 0 = %T", fn.Body.Stmts[0])
	}
	sl, ok := let.Value.(*ast.StringLit)
	if !ok {
		t.Fatalf("let value = %T", let.Value)
	}
	if len(sl.Parts) != 2 || sl.Parts[1].IsLit {
		t.Fatalf("string parts = %+v", sl.Parts)
	}
	bin, ok := sl.Parts[1].Expr.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("interpolation expr = %T", sl.Parts[1].Expr)
	}
	if _, ok := bin.Left.(*ast.IndexExpr); !ok {
		t.Fatalf("binary left = %T", bin.Left)
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
