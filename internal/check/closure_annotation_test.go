package check_test

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// TestE0752FiresOnUnannotatedClosureInUnconstrainedContext verifies the
// self-host `elabInferClosure` emits E0752 when a closure parameter
// lacks an explicit type annotation AND the surrounding context does
// not supply an expected `fn(...)` type to seed it.
//
// Mirrors the negative-corpus case at
// `testdata/spec/negative/reject.osty` E0752/closure param lacks
// annotation — that fixture is currently waivered because
// `internal/speccorpus` runs `pipeline.Run` without the managed
// subprocess checker installed, so check-level diags surface as the
// generic "type checking unavailable" `error:-`. This package's
// TestMain DOES install the subprocess (see main_test.go), so we get
// the real E0752 emit here.
func TestE0752FiresOnUnannotatedClosureInUnconstrainedContext(t *testing.T) {
	src := `fn no_expected_fn_type() {
    let g = |x| x + 1
    let _ = g
}
`
	if !containsCheckCode(t, src, "E0752") {
		t.Fatalf("expected E0752 to fire for unannotated closure param in unconstrained context")
	}
}

// TestE0752FiresOnMixedAnnotationClosure verifies E0752 fires on the
// un-annotated parameter even when sibling parameters have explicit
// types — partial annotation does not satisfy the requirement for the
// un-annotated param.
func TestE0752FiresOnMixedAnnotationClosure(t *testing.T) {
	src := `fn mixed_annotation() {
    let g = |x: Int, y| x + y
    let _ = g
}
`
	if !containsCheckCode(t, src, "E0752") {
		t.Fatalf("expected E0752 to fire for un-annotated `y` in mixed-annotation closure")
	}
}

// TestE0752SkippedWhenExpectedFnTypeSeeds verifies that an unannotated
// closure parameter is OK when the surrounding context provides an
// expected `fn(T) -> R` type (`let f: fn(Int) -> Int = |x| x + 1`).
// Guards against the diag accidentally firing in the param-seeding
// success path.
func TestE0752SkippedWhenExpectedFnTypeSeeds(t *testing.T) {
	src := `fn expected_seeds_param() {
    let f: fn(Int) -> Int = |x| x + 1
    let _ = f
}
`
	if containsCheckCode(t, src, "E0752") {
		t.Fatalf("E0752 unexpectedly fired when expected fn type should have seeded the param")
	}
}

// TestE0752SkippedWhenAllParamsAnnotated verifies the diag stays quiet
// when every closure parameter has an explicit type annotation.
func TestE0752SkippedWhenAllParamsAnnotated(t *testing.T) {
	src := `fn fully_annotated() {
    let g = |x: Int, y: Int| x + y
    let _ = g
}
`
	if containsCheckCode(t, src, "E0752") {
		t.Fatalf("E0752 unexpectedly fired when every closure param had an explicit annotation")
	}
}

// TestClosureParamAnnotationMismatchFiresPerParamDiag is the spec
// for the per-param annotation-vs-expected check landed in
// `toolchain/elab.osty::elabInferClosure`. The change calls
// `checkExpectAssignable(env, expectedParam, declaredTy, ...)` when
// both the closure's declared param type AND the surrounding `fn(...)
// -> R` expected type are available, surfacing the mismatch at the
// param annotation site rather than only at the outer let-decl.
//
// **Currently skipped — architectural dormancy**: the test binary
// uses `cmd/osty-native-checker` built from `internal/selfhost/
// generated.go` (the frozen seed; per CLAUDE.md no regen path exists).
// `toolchain/elab.osty` changes only reach production via the LLVM
// self-hosting flip. Once that flips, drop the `t.Skip` and the
// per-param diag will fire.
func TestClosureParamAnnotationMismatchFiresPerParamDiag(t *testing.T) {
	t.Skip("dormant until LLVM self-hosting flip propagates toolchain/elab.osty into the production native checker")
	src := `fn mismatch() {
    let f: fn(String) -> Int = |x: Int| 1
    let _ = f
}
`
	chk := selfhostCheck(t, src)
	count := 0
	for _, d := range chk.Diags {
		if d != nil && d.Severity == diag.Error && d.Code == "E0700" {
			count++
		}
	}
	if count < 2 {
		t.Fatalf("expected at least two E0700 emissions (per-param + outer closure mismatch); got %d in:\n%v", count, errorCodes(chk.Diags))
	}
}

// TestClosureParamAnnotationMatchHasNoExtraDiag verifies the
// per-param assignability check stays silent when the annotation
// matches the expected fn type's parameter — no spurious diags.
// Survives the dormancy because no diag should ever fire in either
// pre- or post-self-hosting state for matching annotations.
func TestClosureParamAnnotationMatchHasNoExtraDiag(t *testing.T) {
	src := `fn match_ok() {
    let f: fn(Int) -> Int = |x: Int| x + 1
    let _ = f
}
`
	chk := selfhostCheck(t, src)
	for _, d := range chk.Diags {
		if d != nil && d.Severity == diag.Error {
			t.Fatalf("unexpected error diag for matching annotation: %s %q\nall codes: %v",
				d.Code, d.Message, errorCodes(chk.Diags))
		}
	}
}

func selfhostCheck(t *testing.T, src string) *check.Result {
	t.Helper()
	file, _ := parser.ParseDiagnostics([]byte(src))
	res := resolve.ResolveFileSourceDefault([]byte(src), file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
	})
	if chk == nil {
		t.Fatalf("check.SelfhostFile returned nil")
	}
	return chk
}

func errorCodes(diags []*diag.Diagnostic) []string {
	out := make([]string, 0, len(diags))
	for _, d := range diags {
		if d == nil || d.Severity != diag.Error {
			continue
		}
		out = append(out, d.Code)
	}
	return out
}

func containsCheckCode(t *testing.T, src, code string) bool {
	t.Helper()
	file, _ := parser.ParseDiagnostics([]byte(src))
	res := resolve.ResolveFileSourceDefault([]byte(src), file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
	})
	if chk == nil {
		t.Fatalf("check.SelfhostFile returned nil")
	}
	for _, d := range chk.Diags {
		if d == nil {
			continue
		}
		if d.Severity != diag.Error {
			continue
		}
		if strings.EqualFold(d.Code, code) {
			return true
		}
	}
	return false
}
