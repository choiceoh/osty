package format

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/parser"
)

// TestFormatUseCRoundTrips verifies that the canonical printer
// emits `use c "libname"` when the AST carries the desugared
// `runtime.cabi.<lib>` runtime path. Together with the parser
// desugaring this gives `use c "libname" { ... }` a stable
// surface form across format runs (LANG_SPEC §12.8).
func TestFormatUseCRoundTrips(t *testing.T) {
	src := []byte(`use c "osty_demo" as demo {
    fn osty_demo_double(x: Int) -> Int
}
`)
	file, diags := parser.ParseDiagnostics(src)
	if len(diags) > 0 {
		t.Fatalf("parse diagnostics: %v", diags[0])
	}
	out := string(File(file))
	for _, want := range []string{
		`use c "osty_demo" as demo`,
		`fn osty_demo_double(x: Int) -> Int`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("formatted output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "runtime.cabi") {
		t.Fatalf("formatted output exposed desugared runtime.cabi path:\n%s", out)
	}
}

// TestFormatRuntimeCabiNormalizesToUseC ensures a hand-written
// `use runtime.cabi.<lib>` import is normalized to the canonical
// `use c "<lib>"` surface — both forms produce the same AST so
// the printer picks one canonical representation.
func TestFormatRuntimeCabiNormalizesToUseC(t *testing.T) {
	src := []byte(`use runtime.cabi.osty_demo as demo {
    fn osty_demo_double(x: Int) -> Int
}
`)
	file, diags := parser.ParseDiagnostics(src)
	if len(diags) > 0 {
		t.Fatalf("parse diagnostics: %v", diags[0])
	}
	out := string(File(file))
	if !strings.Contains(out, `use c "osty_demo" as demo`) {
		t.Fatalf("runtime.cabi import not normalized to use c surface:\n%s", out)
	}
}
