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
