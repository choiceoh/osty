package lint

import (
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// TestLintDeferResultFiresL0007 covers LANG_SPEC_v0.5 §4.12 rule 9: a
// `defer expr` whose expression is typed Result/Option/`T?` must warn so
// the user either matches, `?`-propagates, or wraps with
// `ignoreError`/`logError`.
func TestLintDeferResultFiresL0007(t *testing.T) {
	src := []byte(`fn close() -> Result<(), Error> { Err(Error.new("boom")) }

fn run() {
    defer close()
}
`)
	diags := runLint(t, src)
	if !hasCode(diags, diag.CodeIgnoredResult) {
		t.Fatalf("want L0007 on Result-returning defer; got %s", codeList(diags))
	}
}

// TestLintDeferOptionFiresL0007 checks the Option<T>/`T?` arm of the same rule.
func TestLintDeferOptionFiresL0007(t *testing.T) {
	src := []byte(`fn maybe() -> Int? { None }

fn run() {
    defer maybe()
}
`)
	diags := runLint(t, src)
	if !hasCode(diags, diag.CodeIgnoredResult) {
		t.Fatalf("want L0007 on Option-returning defer; got %s", codeList(diags))
	}
}

// TestLintDeferUnitSkipsL0007 ensures the rule does not misfire on
// defers whose target has unit return — those are the common case and
// must stay quiet.
func TestLintDeferUnitSkipsL0007(t *testing.T) {
	src := []byte(`fn shutdown() {}

fn run() {
    defer shutdown()
}
`)
	diags := runLint(t, src)
	if hasCode(diags, diag.CodeIgnoredResult) {
		t.Fatalf("unit-returning defer should stay silent; got %s", codeList(diags))
	}
}

func runLint(t *testing.T, src []byte) []*diag.Diagnostic {
	t.Helper()
	file, _ := parser.Parse(src)
	if file == nil {
		t.Fatal("parse returned nil file")
	}
	reg := stdlib.LoadCached()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	chk := check.SelfhostFile(file, res, check.Opts{
		Source:        src,
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
	})
	return File(file, src, res, chk).Diags
}

func hasCode(diags []*diag.Diagnostic, code string) bool {
	for _, d := range diags {
		if d != nil && d.Code == code {
			return true
		}
	}
	return false
}

func codeList(diags []*diag.Diagnostic) string {
	out := ""
	for i, d := range diags {
		if i > 0 {
			out += " "
		}
		if d == nil {
			out += "<nil>"
			continue
		}
		out += d.Code
	}
	if out == "" {
		return "<none>"
	}
	return out
}
