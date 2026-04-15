package gen_test

import (
	"os"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/gen"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

// TestRealWorldFixture runs the project-root word_freq.osty sample
// through the transpiler and reports how far it gets. This is a
// depth probe, not a correctness assertion — word_freq uses stdlib
// modules (regex, json, fs, parallel) that Phase 5+ will bring online.
func TestRealWorldFixture(t *testing.T) {
	src, err := os.ReadFile("../../word_freq.osty")
	if err != nil {
		t.Skipf("no word_freq.osty at repo root: %v", err)
	}
	file, parseDiags := parser.ParseDiagnostics(src)
	t.Logf("parse errors: %d", countErrs(parseDiags))
	res := resolve.File(file, resolve.NewPrelude())
	t.Logf("resolve errors: %d", countErrs(res.Diags))
	chk := check.File(file, res)
	t.Logf("check errors: %d", countErrs(chk.Diags))

	goSrc, gerr := gen.Generate("main", file, res, chk)
	if gerr != nil {
		t.Logf("gen error: %v", gerr)
	}
	t.Logf("generated %d bytes of Go", len(goSrc))

	// Breakdown of non-trivial front-end errors so we see where depth
	// is missing.
	for _, d := range res.Diags {
		if d.Severity == diag.Error {
			t.Logf("  resolve: %s", d.Message)
		}
	}
}
