package resolve

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
)

// runAnnotArgs parses + resolves the given source and returns every
// annotation-related diagnostic. Used by the arg-validator tests to
// assert the exact E0739 shape of the runtime annotation arg rules
// added in this spike.
func runAnnotArgs(t *testing.T, src string) []*diag.Diagnostic {
	t.Helper()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse diagnostics: %v", parseDiags)
	}
	res := File(file, NewPrelude())
	return res.Diags
}

func countArgBad(ds []*diag.Diagnostic) int {
	n := 0
	for _, d := range ds {
		if d != nil && d.Code == diag.CodeAnnotationBadArg {
			n++
		}
	}
	return n
}

func renderDiags(ds []*diag.Diagnostic) string {
	var b strings.Builder
	for _, d := range ds {
		if d == nil {
			continue
		}
		b.WriteString("  ")
		b.WriteString(d.Code)
		b.WriteString(": ")
		b.WriteString(d.Message)
		b.WriteString("\n")
	}
	return b.String()
}

// --- #[repr] ---

func TestReprAcceptsC(t *testing.T) {
	src := `
#[repr(c)]
pub struct S {
    pub x: Int,
}
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 0 {
		t.Fatalf("expected 0 E0739, got %d:\n%s", got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestReprRejectsMissingArg(t *testing.T) {
	src := `
#[repr]
pub struct S {
    pub x: Int,
}
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739 (missing arg), got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestReprRejectsUnknownLayout(t *testing.T) {
	src := `
#[repr(foo)]
pub struct S {
    pub x: Int,
}
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739 (unknown layout), got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestReprRejectsPacked(t *testing.T) {
	// `packed` is a common C layout request but v0.5 accepts only `c`.
	src := `
#[repr(packed)]
pub struct S {
    pub x: Int,
}
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739 (packed not accepted), got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestReprRejectsExtraArg(t *testing.T) {
	src := `
#[repr(c, packed)]
pub struct S {
    pub x: Int,
}
`
	got := countArgBad(runAnnotArgs(t, src))
	if got < 1 {
		t.Fatalf("expected at least 1 E0739 (extra arg), got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

// --- #[export] ---

func TestExportAcceptsStringLiteral(t *testing.T) {
	src := `
#[export("osty.gc.alloc_v1")]
pub fn f() -> Int {
    0
}
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 0 {
		t.Fatalf("expected 0 E0739, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestExportRejectsMissingArg(t *testing.T) {
	src := `
#[export]
pub fn f() -> Int {
    0
}
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739 (missing arg), got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestExportRejectsEmptyStringName(t *testing.T) {
	src := `
#[export("")]
pub fn f() -> Int {
    0
}
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739 (empty name), got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestExportRejectsKeyValue(t *testing.T) {
	src := `
#[export(name = "foo")]
pub fn f() -> Int {
    0
}
`
	got := countArgBad(runAnnotArgs(t, src))
	if got < 1 {
		t.Fatalf("expected at least 1 E0739 (key=value form), got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

// --- no-arg annotations ---

func TestIntrinsicAcceptsBare(t *testing.T) {
	src := `
#[intrinsic]
pub fn f() -> Int {
    0
}
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 0 {
		t.Fatalf("expected 0 E0739, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestIntrinsicRejectsArgs(t *testing.T) {
	src := `
#[intrinsic(foo)]
pub fn f() -> Int {
    0
}
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestPodRejectsArgs(t *testing.T) {
	src := `
#[pod(tight)]
#[repr(c)]
pub struct S {
    pub x: Int,
}
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestCAbiRejectsArgs(t *testing.T) {
	src := `
#[c_abi(fast)]
pub fn f() -> Int {
    0
}
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestNoAllocRejectsArgs(t *testing.T) {
	src := `
#[no_alloc(strict)]
pub fn f() -> Int {
    0
}
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

// --- #[vectorize] (v0.6 A5) ---

func TestVectorizeAcceptsBareFlag(t *testing.T) {
	src := `
#[vectorize]
pub fn hot(n: Int) -> Int {
    let mut a = 0
    for i in 0..n {
        a = a + i
    }
    a
}
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 0 {
		t.Fatalf("expected 0 E0739 on bare #[vectorize], got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestVectorizeAcceptsWidth(t *testing.T) {
	src := `
#[vectorize(width = 8)]
pub fn hot(n: Int) -> Int { n }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 0 {
		t.Fatalf("expected 0 E0739 on `#[vectorize(width = 8)]`, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestVectorizeAcceptsAllThreeTuning(t *testing.T) {
	src := `
#[vectorize(scalable, predicate, width = 8)]
pub fn hot(n: Int) -> Int { n }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 0 {
		t.Fatalf("expected 0 E0739 on full tuning combo, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestVectorizeRejectsUnknownKey(t *testing.T) {
	src := `
#[vectorize(foo = 1)]
pub fn hot(n: Int) -> Int { n }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739 on unknown key, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestVectorizeRejectsWidthZero(t *testing.T) {
	src := `
#[vectorize(width = 0)]
pub fn hot(n: Int) -> Int { n }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739 on width = 0, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestVectorizeRejectsWidthTooLarge(t *testing.T) {
	src := `
#[vectorize(width = 99999)]
pub fn hot(n: Int) -> Int { n }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739 on width out of range, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestVectorizeRejectsDuplicateScalable(t *testing.T) {
	src := `
#[vectorize(scalable, scalable)]
pub fn hot(n: Int) -> Int { n }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739 on duplicate scalable, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

// --- #[no_vectorize] (v0.6 A5.2) ---

func TestNoVectorizeAcceptsBareFlag(t *testing.T) {
	src := `
#[no_vectorize]
pub fn cold(n: Int) -> Int { n }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 0 {
		t.Fatalf("expected 0 E0739 on bare #[no_vectorize], got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestNoVectorizeRejectsArgs(t *testing.T) {
	src := `
#[no_vectorize(reason = "gc-cooperation")]
pub fn cold(n: Int) -> Int { n }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739 on #[no_vectorize(...)], got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestNoVectorizeRejectsOnField(t *testing.T) {
	src := `
pub struct Bad {
    #[no_vectorize]
    pub count: Int,
}
`
	if got := countBadTarget(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0607 on `#[no_vectorize]` over struct field, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

// --- #[parallel] (v0.6 A6) ---

func TestParallelAcceptsBareFlag(t *testing.T) {
	src := `
#[parallel]
pub fn hot(n: Int) -> Int { n }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 0 {
		t.Fatalf("expected 0 E0739 on bare #[parallel], got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestParallelRejectsArgs(t *testing.T) {
	src := `
#[parallel(level = 2)]
pub fn hot(n: Int) -> Int { n }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739 on #[parallel(...)], got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestParallelRejectsOnStructField(t *testing.T) {
	src := `
pub struct Bad {
    #[parallel]
    pub n: Int,
}
`
	if got := countBadTarget(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0607 on `#[parallel]` over struct field, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

// --- #[unroll] / #[unroll(count = N)] (v0.6 A7) ---

func TestUnrollAcceptsBareFlag(t *testing.T) {
	src := `
#[unroll]
pub fn hot(n: Int) -> Int { n }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 0 {
		t.Fatalf("expected 0 E0739 on bare #[unroll], got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestUnrollAcceptsCount(t *testing.T) {
	src := `
#[unroll(count = 4)]
pub fn hot(n: Int) -> Int { n }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 0 {
		t.Fatalf("expected 0 E0739 on `#[unroll(count = 4)]`, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestUnrollRejectsUnknownKey(t *testing.T) {
	src := `
#[unroll(factor = 4)]
pub fn hot(n: Int) -> Int { n }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739 on unknown `factor` key, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestUnrollRejectsCountZero(t *testing.T) {
	src := `
#[unroll(count = 0)]
pub fn hot(n: Int) -> Int { n }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739 on count = 0, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

// Retained for historical context — the v0.6 A5 bare-flag form is
// still the one that appears throughout the stdlib, so losing the
// "bare form stays legal" signal would be a regression.
func TestVectorizeRejectsPositional(t *testing.T) {
	src := `
#[vectorize(foobar)]
pub fn hot(n: Int) -> Int { n }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739 on unknown positional, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func countBadTarget(ds []*diag.Diagnostic) int {
	n := 0
	for _, d := range ds {
		if d != nil && d.Code == diag.CodeAnnotationBadTarget {
			n++
		}
	}
	return n
}

// TestVectorizeRejectsOnStructField pins the target-mask contract:
// `#[vectorize]` is top-level-fn + method only, so attaching it to a
// struct field must raise E0607 rather than silently accepting it. If
// the target mask in `ast/ast.go` widens to include TargetStructField,
// this test must fail — the annotation's semantics are fn-body-scoped
// and don't make sense on data-layout targets.
func TestVectorizeRejectsOnStructField(t *testing.T) {
	src := `
pub struct Bad {
    #[vectorize]
    pub count: Int,
}
`
	if got := countBadTarget(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0607 on `#[vectorize]` over struct field, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

// TestVectorizeRejectsOnEnumVariant — parallel to the struct-field
// case: `#[vectorize]` attached to an enum variant is also E0607.
func TestVectorizeRejectsOnEnumVariant(t *testing.T) {
	src := `
pub enum Shape {
    #[vectorize]
    Circle(Int),
    Square(Int),
}
`
	if got := countBadTarget(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0607 on `#[vectorize]` over enum variant, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

// TestVectorizeAcceptsOnMethod rounds out the target-mask: methods on
// struct/enum bodies are explicitly permitted targets (mirroring the
// codegen path that reads the annotation from the method's FnDecl).
func TestVectorizeAcceptsOnMethod(t *testing.T) {
	src := `
pub struct Vec {
    total: Int,

    #[vectorize]
    pub fn addRange(mut self, n: Int) {
        for i in 0..n {
            self.total = self.total + i
        }
    }
}
`
	diags := runAnnotArgs(t, src)
	if got := countBadTarget(diags); got != 0 {
		t.Fatalf("expected 0 E0607 on `#[vectorize]` method, got %d:\n%s",
			got, renderDiags(diags))
	}
	if got := countArgBad(diags); got != 0 {
		t.Fatalf("expected 0 E0739 on `#[vectorize]` method, got %d:\n%s",
			got, renderDiags(diags))
	}
}

// --- #[inline] family (v0.6 A8) ---

func TestInlineAcceptsBareFlag(t *testing.T) {
	src := `
#[inline]
pub fn tiny(n: Int) -> Int { n + 1 }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 0 {
		t.Fatalf("expected 0 E0739 on bare #[inline], got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestInlineAcceptsAlways(t *testing.T) {
	src := `
#[inline(always)]
pub fn tiny(n: Int) -> Int { n + 1 }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 0 {
		t.Fatalf("expected 0 E0739 on #[inline(always)], got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestInlineAcceptsNever(t *testing.T) {
	src := `
#[inline(never)]
pub fn big(n: Int) -> Int { n + 1 }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 0 {
		t.Fatalf("expected 0 E0739 on #[inline(never)], got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestInlineRejectsUnknownFlag(t *testing.T) {
	src := `
#[inline(sometimes)]
pub fn mid(n: Int) -> Int { n + 1 }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739 on unknown #[inline] flag, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

// --- #[hot] / #[cold] (v0.6 A9) ---

func TestHotAcceptsBareFlag(t *testing.T) {
	src := `
#[hot]
pub fn f() -> Int { 1 }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 0 {
		t.Fatalf("expected 0 E0739 on #[hot], got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestColdAcceptsBareFlag(t *testing.T) {
	src := `
#[cold]
pub fn errPath() -> Int { -1 }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 0 {
		t.Fatalf("expected 0 E0739 on #[cold], got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestHotRejectsArgs(t *testing.T) {
	src := `
#[hot(level = 3)]
pub fn f() -> Int { 1 }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739 on #[hot(...)], got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

// --- #[target_feature(...)] (v0.6 A10) ---

func TestTargetFeatureAcceptsSingleFeature(t *testing.T) {
	src := `
#[target_feature(avx512f)]
pub fn hot() -> Int { 1 }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 0 {
		t.Fatalf("expected 0 E0739 on single-feature target_feature, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestTargetFeatureAcceptsMultipleFeatures(t *testing.T) {
	src := `
#[target_feature(avx512f, avx512bw, avx512vl)]
pub fn hot() -> Int { 1 }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 0 {
		t.Fatalf("expected 0 E0739 on multi-feature target_feature, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestTargetFeatureRejectsEmpty(t *testing.T) {
	src := `
#[target_feature()]
pub fn hot() -> Int { 1 }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739 on empty target_feature, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

func TestTargetFeatureRejectsDuplicate(t *testing.T) {
	src := `
#[target_feature(avx512f, avx512f)]
pub fn hot() -> Int { 1 }
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739 on duplicate feature, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}

// --- combined: full canonical runtime annotation stack ---

func TestCanonicalRuntimeStackValidates(t *testing.T) {
	// The canonical stacking from LANG_SPEC §19.6 — all args well-formed.
	src := `
#[export("osty.gc.alloc_v1")]
#[c_abi]
#[no_alloc]
pub fn alloc_v1(bytes: Int, kind: Int) -> Int {
    0
}

#[pod]
#[repr(c)]
pub struct Header {
    pub next: Int,
    pub size: Int32,
}
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 0 {
		t.Fatalf("canonical stack must produce 0 E0739, got %d:\n%s",
			got, renderDiags(runAnnotArgs(t, src)))
	}
}
