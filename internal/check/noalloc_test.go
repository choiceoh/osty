package check

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// runNoAlloc parses + resolves + runs only the noalloc walker (the
// native-checker boundary is bypassed via parseResolvedFile + a direct
// runNoAllocChecks call). This keeps the spike's tests self-contained:
// they exercise just the new code path without depending on the rest
// of the checker being green for unrelated reasons.
func runNoAlloc(t *testing.T, src string) []*diag.Diagnostic {
	t.Helper()
	file, diags := parser.ParseDiagnostics([]byte(src))
	if len(diags) != 0 {
		t.Fatalf("parse diagnostics: %v", diags)
	}
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	// Resolver may still emit diagnostics (e.g. unknown identifiers in
	// our stripped fixtures); we only care about the noalloc pass here.
	_ = res
	return runNoAllocChecks(file, res)
}

func expectNoAllocCode(t *testing.T, src string, wantCount int) []*diag.Diagnostic {
	t.Helper()
	out := runNoAlloc(t, src)
	got := 0
	for _, d := range out {
		if d.Code == diag.CodeNoAllocViolation {
			got++
		}
	}
	if got != wantCount {
		t.Fatalf("expected %d E0772 diagnostics, got %d:\n%s",
			wantCount, got, formatDiags(out))
	}
	return out
}

func formatDiags(ds []*diag.Diagnostic) string {
	var b strings.Builder
	for _, d := range ds {
		b.WriteString("  ")
		b.WriteString(d.Code)
		b.WriteString(": ")
		b.WriteString(d.Message)
		b.WriteString("\n")
	}
	return b.String()
}

func TestNoAllocAcceptsPlainArithmetic(t *testing.T) {
	src := `
#[no_alloc]
fn add(a: Int, b: Int) -> Int {
    a + b
}
`
	expectNoAllocCode(t, src, 0)
}

func TestNoAllocAcceptsControlFlow(t *testing.T) {
	src := `
#[no_alloc]
fn pick(a: Int, b: Int) -> Int {
    if a > b {
        a
    } else {
        b
    }
}
`
	expectNoAllocCode(t, src, 0)
}

func TestNoAllocAcceptsPlainStringLiteral(t *testing.T) {
	src := `
#[no_alloc]
fn label() -> String {
    "ok"
}
`
	// A plain "ok" has no interpolation and is not raw/triple-quoted,
	// so the walker treats it as compile-time-interned static data.
	expectNoAllocCode(t, src, 0)
}

func TestNoAllocAcceptsCallToOtherNoAllocFn(t *testing.T) {
	src := `
#[no_alloc]
fn double(x: Int) -> Int {
    x + x
}

#[no_alloc]
fn quadruple(x: Int) -> Int {
    double(double(x))
}
`
	expectNoAllocCode(t, src, 0)
}

func TestNoAllocAcceptsRawIntrinsicCalls(t *testing.T) {
	// `raw.alloc` / `raw.write` etc. are accepted by the spike's
	// callee gate even without a resolved std.runtime.raw symbol.
	// This is a deliberate spike shortcut documented in the walker.
	src := `
#[no_alloc]
fn manualAlloc() -> Int {
    let p = raw.alloc(64, 8)
    raw.bits(p)
}
`
	expectNoAllocCode(t, src, 0)
}

func TestNoAllocRejectsListLiteral(t *testing.T) {
	src := `
#[no_alloc]
fn makeList() -> List<Int> {
    [1, 2, 3]
}
`
	expectNoAllocCode(t, src, 1)
}

func TestNoAllocRejectsMapLiteral(t *testing.T) {
	src := `
#[no_alloc]
fn makeMap() -> Map<String, Int> {
    {"a": 1, "b": 2}
}
`
	expectNoAllocCode(t, src, 1)
}

func TestNoAllocRejectsStringInterpolation(t *testing.T) {
	src := `
#[no_alloc]
fn greet(name: String) -> String {
    "hi, {name}"
}
`
	expectNoAllocCode(t, src, 1)
}

func TestNoAllocRejectsTripleQuotedString(t *testing.T) {
	src := "\n#[no_alloc]\nfn poem() -> String {\n    \"\"\"\n    rose\n    \"\"\"\n}\n"
	expectNoAllocCode(t, src, 1)
}

func TestNoAllocRejectsRawString(t *testing.T) {
	src := `
#[no_alloc]
fn pattern() -> String {
    r"\d+"
}
`
	expectNoAllocCode(t, src, 1)
}

func TestNoAllocRejectsStructLiteral(t *testing.T) {
	src := `
struct Point { x: Int, y: Int }

#[no_alloc]
fn origin() -> Point {
    Point { x: 0, y: 0 }
}
`
	expectNoAllocCode(t, src, 1)
}

func TestNoAllocRejectsClosure(t *testing.T) {
	src := `
#[no_alloc]
fn pickClosure() -> Int {
    let f = |x: Int| -> Int { x + 1 }
    f(5)
}
`
	// Two errors: closure construction itself, and the indirect call.
	expectNoAllocCode(t, src, 2)
}

func TestNoAllocRejectsCallToOrdinaryFn(t *testing.T) {
	src := `
fn ordinary(x: Int) -> Int {
    x + 1
}

#[no_alloc]
fn caller() -> Int {
    ordinary(5)
}
`
	expectNoAllocCode(t, src, 1)
}

func TestNoAllocRejectsListLiteralInLetBinding(t *testing.T) {
	src := `
#[no_alloc]
fn alloc_in_let() -> Int {
    let xs = [1, 2, 3]
    xs[0]
}
`
	out := runNoAlloc(t, src)
	got := 0
	for _, d := range out {
		if d.Code == diag.CodeNoAllocViolation {
			got++
		}
	}
	if got < 1 {
		t.Fatalf("expected at least 1 E0772, got %d:\n%s", got, formatDiags(out))
	}
}

func TestNoAllocReportsViolationInsideIfBranch(t *testing.T) {
	src := `
#[no_alloc]
fn branchAllocs(flag: Bool) -> List<Int> {
    if flag {
        [1, 2, 3]
    } else {
        [4, 5]
    }
}
`
	expectNoAllocCode(t, src, 2)
}

func TestNoAllocReportsViolationInsideMatchArm(t *testing.T) {
	src := `
#[no_alloc]
fn matchAllocs(n: Int) -> List<Int> {
    match n {
        0 -> [10],
        _ -> [20, 30],
    }
}
`
	expectNoAllocCode(t, src, 2)
}

func TestNoAllocReportsViolationInsideForBody(t *testing.T) {
	src := `
#[no_alloc]
fn loopAllocs() -> Int {
    let mut acc = 0
    for x in 0..10 {
        let temp = [x, x + 1]
        acc = acc + 1
    }
    acc
}
`
	expectNoAllocCode(t, src, 1)
}

func TestNoAllocLeavesFunctionsWithoutAnnotationAlone(t *testing.T) {
	src := `
fn ordinary() -> List<Int> {
    [1, 2, 3]
}
`
	expectNoAllocCode(t, src, 0)
}

func TestNoAllocDiagnosticMessageMentionsFunctionName(t *testing.T) {
	src := `
#[no_alloc]
fn boom() -> List<Int> {
    [1]
}
`
	out := runNoAlloc(t, src)
	if len(out) == 0 {
		t.Fatalf("expected at least one diagnostic")
	}
	for _, d := range out {
		if d.Code == diag.CodeNoAllocViolation {
			if !strings.Contains(d.Message, "boom") {
				t.Fatalf("expected diagnostic to name function `boom`, got: %s", d.Message)
			}
			return
		}
	}
	t.Fatalf("no E0772 diagnostic found:\n%s", formatDiags(out))
}
