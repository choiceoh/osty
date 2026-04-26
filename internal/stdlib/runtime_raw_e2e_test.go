package stdlib

import (
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

// TestRuntimeRawImportResolves end-to-end: a fixture file that
// imports `std.runtime.raw` and references every intrinsic resolves
// cleanly through parser + resolver + stdlib registry.
//
// This is the first test proving the full import path of the runtime
// sublanguage actually works — previous spikes only verified
// individual pieces (annotations, types, pod shape, bound clauses).
func TestRuntimeRawImportResolves(t *testing.T) {
	src := `
use std.runtime.raw

pub fn demo() -> Int {
    let p = raw.alloc(64, 8)
    raw.write::<Int>(p, 42)
    let v = raw.read::<Int>(p)
    raw.free(p)
    v
}
`
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse diagnostics: %v", parseDiags)
	}
	reg := Load()
	res := resolve.ResolveFileDefault(file, reg)

	// The resolver binds `raw.alloc`, `raw.write`, `raw.read`, `raw.free`
	// against the imported package. `Pod` in the turbofish bound is also
	// bound via the prelude (spike #314).
	for _, d := range res.Diags {
		if d == nil || d.Severity != diag.Error {
			continue
		}
		// Allow E0500 "undefined name `abort`" only if it creeps in from
		// stdlib noise unrelated to runtime.raw — but our fixture does
		// not reference abort, so surface everything.
		t.Errorf("resolver rejected runtime.raw fixture: %s: %s",
			d.Code, d.Message)
	}
}

// TestRuntimeRawNoClashWithOrdinaryNames checks that adding
// `std.runtime.raw` did not shadow anything in user scope. A user
// writing a local `raw` identifier in an ordinary file must continue
// to resolve that identifier locally, not against the stdlib module.
func TestRuntimeRawNoClashWithOrdinaryNames(t *testing.T) {
	src := `
pub fn takesLocal(raw: Int) -> Int {
    raw + 1
}
`
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse diagnostics: %v", parseDiags)
	}
	reg := Load()
	res := resolve.ResolveFileDefault(file, reg)
	for _, d := range res.Diags {
		if d == nil || d.Severity != diag.Error {
			continue
		}
		t.Errorf("ordinary code broke: %s: %s", d.Code, d.Message)
	}
}
