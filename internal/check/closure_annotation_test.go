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
