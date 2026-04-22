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

func TestVectorizeRejectsArgs(t *testing.T) {
	src := `
#[vectorize(width = 4)]
pub fn hot(n: Int) -> Int {
    n
}
`
	if got := countArgBad(runAnnotArgs(t, src)); got != 1 {
		t.Fatalf("expected 1 E0739 on #[vectorize(...)], got %d:\n%s",
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
