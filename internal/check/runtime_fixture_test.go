package check

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// runtimeFixturePath resolves the path to testdata/runtime_fixture.osty
// from the repository root. The test binary's working directory is the
// package under test (`internal/check/`), so we walk up two levels.
func runtimeFixturePath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	// internal/check/ -> repository root
	root := filepath.Join(wd, "..", "..")
	return filepath.Join(root, "testdata", "runtime_fixture.osty")
}

// TestRuntimeFixturePrivilegedPipelineIsClean is the flagship
// end-to-end test for the §19 runtime sublanguage front-end. It loads
// testdata/runtime_fixture.osty — a realistic runtime package that
// exercises every piece the front-end spike series delivered — and
// runs it through parser + resolver + privilege gate (privileged=true)
// + pod shape checker + no_alloc walker. Zero error diagnostics is
// the pass condition.
//
// If this test ever goes red, a regression has landed that breaks the
// GC self-hosting path at the front-end level.
func TestRuntimeFixturePrivilegedPipelineIsClean(t *testing.T) {
	src, err := os.ReadFile(runtimeFixturePath(t))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	file, parseDiags := parser.ParseDiagnostics(src)
	if len(parseDiags) > 0 {
		t.Fatalf("fixture failed to parse: %v", parseDiags)
	}

	reg := stdlib.LoadCached()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)

	var all []*diag.Diagnostic
	all = append(all, res.Diags...)
	all = append(all, runPrivilegeGate(file, true)...)
	all = append(all, runPodShapeChecks(file)...)
	all = append(all, runNoAllocChecks(file, res)...)

	errs := 0
	for _, d := range all {
		if d == nil || d.Severity != diag.Error {
			continue
		}
		errs++
		t.Errorf("  %s: %s", d.Code, d.Message)
	}
	if errs > 0 {
		t.Fatalf("fixture produced %d error diagnostic(s) in privileged mode", errs)
	}
}

// TestRuntimeFixtureUnprivilegedRejected is the symmetry test: the
// same fixture in user mode must produce LOTS of E0770 diagnostics
// because every top-level decl carries runtime-gated annotations
// (`#[pod]`, `#[repr]`, `#[export]`, `#[c_abi]`, `#[no_alloc]`) or
// references runtime-gated type names (`RawPtr`, `Pod`).
//
// The exact count is not pinned — any regression that drops the E0770
// count below a generous floor means the privilege gate coverage
// regressed.
func TestRuntimeFixtureUnprivilegedRejected(t *testing.T) {
	src, err := os.ReadFile(runtimeFixturePath(t))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	file, parseDiags := parser.ParseDiagnostics(src)
	if len(parseDiags) > 0 {
		t.Fatalf("fixture failed to parse: %v", parseDiags)
	}
	ds := runPrivilegeGate(file, false)
	n := 0
	for _, d := range ds {
		if d != nil && d.Code == diag.CodeRuntimePrivilegeViolation {
			n++
		}
	}
	// Floor = 10. The fixture has 5 struct decls (each with 1-2
	// annotations + at least one RawPtr/Pod reference) and 8 fn
	// decls (each with 1-3 annotations), so the true count is well
	// above this. 10 is a deliberately conservative lower bound.
	if n < 10 {
		t.Fatalf("unprivileged fixture produced only %d E0770 diagnostics — privilege gate coverage regressed", n)
	}
}

// TestRuntimeFixtureUsesRuntimeRawStdlib checks that the fixture's
// `use std.runtime.raw` import actually binds to the stdlib module
// landed in #319. If the stdlib loader regresses on nested paths,
// this fails with "undefined name `raw`" rather than silently
// accepting.
func TestRuntimeFixtureUsesRuntimeRawStdlib(t *testing.T) {
	src, err := os.ReadFile(runtimeFixturePath(t))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	file, parseDiags := parser.ParseDiagnostics(src)
	if len(parseDiags) > 0 {
		t.Fatalf("fixture failed to parse: %v", parseDiags)
	}
	reg := stdlib.LoadCached()
	if reg.LookupPackage("std.runtime.raw") == nil {
		t.Fatal("stdlib registry is missing std.runtime.raw (regression in #319)")
	}
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	for _, d := range res.Diags {
		if d == nil {
			continue
		}
		if d.Code == "E0500" {
			t.Errorf("resolver could not bind a name — stdlib or prelude regression: %s", d.Message)
		}
	}
}
