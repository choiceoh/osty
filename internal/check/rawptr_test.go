package check

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
	"github.com/osty/osty/internal/types"
)

// runEndToEnd parses, resolves, and runs every check-level pass
// (privilege gate + pod shape + no_alloc) against a source snippet,
// returning the combined diagnostic list. Used by the RawPtr spike to
// verify that registering `RawPtr` as a prelude-visible primitive does
// not trigger unexpected resolve or typecheck errors on real fixtures.
func runEndToEnd(t *testing.T, src string, privileged bool) []*diag.Diagnostic {
	t.Helper()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse diagnostics: %v", parseDiags)
	}
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())

	var out []*diag.Diagnostic
	out = append(out, res.Diags...)
	out = append(out, runPrivilegeGate(file, privileged)...)
	out = append(out, runPodShapeChecks(file)...)
	out = append(out, runNoAllocChecks(file, res)...)
	return out
}

// --- RawPtr is a real primitive ---

func TestRawPtrIsPrimitive(t *testing.T) {
	p := types.PrimitiveByName("RawPtr")
	if p == nil {
		t.Fatal("expected `RawPtr` to resolve via PrimitiveByName")
	}
	if p.Kind != types.PRawPtr {
		t.Fatalf("expected PRawPtr kind, got %v", p.Kind)
	}
	if p.String() != "RawPtr" {
		t.Fatalf("expected String() == RawPtr, got %q", p.String())
	}
}

func TestRawPtrSingletonIsStable(t *testing.T) {
	// PrimitiveByName and the exported singleton must be the same pointer.
	if types.PrimitiveByName("RawPtr") != types.RawPtr {
		t.Fatal("PrimitiveByName(\"RawPtr\") must equal the exported types.RawPtr singleton")
	}
}

func TestRawPtrIsEqualNotOrdered(t *testing.T) {
	if !types.PRawPtr.IsEqual() {
		t.Error("PRawPtr must be Equal per §19.3")
	}
	if types.PRawPtr.IsOrdered() {
		t.Error("PRawPtr must NOT be Ordered per §19.3 (tag-bit ordering, not numeric)")
	}
}

func TestRawPtrIsNotNumeric(t *testing.T) {
	if types.PRawPtr.IsNumeric() {
		t.Error("PRawPtr is not a numeric type; runtime code uses raw.bits / raw.fromBits to cross the integer boundary explicitly")
	}
	if types.PRawPtr.IsInteger() || types.PRawPtr.IsFloat() {
		t.Error("PRawPtr must not register as Int or Float — it is opaque")
	}
}

// --- End-to-end: privileged package using RawPtr passes cleanly ---

func TestEndToEndPrivilegedRawPtrFieldResolves(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
pub struct Header {
    pub next: RawPtr,
    pub size: Int32,
}
`
	diags := runEndToEnd(t, src, true)
	errs := errorsOnly(diags)
	if len(errs) > 0 {
		t.Fatalf("expected 0 error diagnostics in privileged mode, got %d:\n%s",
			len(errs), renderDiagList(errs))
	}
}

func TestEndToEndPrivilegedRawPtrInSignature(t *testing.T) {
	src := `
#[export("osty.gc.alloc_v1")]
#[c_abi]
#[no_alloc]
pub fn alloc_v1(bytes: Int, kind: Int) -> RawPtr {
    0
}
`
	diags := runEndToEnd(t, src, true)
	errs := errorsOnly(diags)
	if len(errs) > 0 {
		t.Fatalf("expected 0 error diagnostics in privileged mode, got %d:\n%s",
			len(errs), renderDiagList(errs))
	}
}

func TestEndToEndPrivilegedRawPtrInOption(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
pub struct Slot {
    pub addr: Option<RawPtr>,
}
`
	diags := runEndToEnd(t, src, true)
	errs := errorsOnly(diags)
	if len(errs) > 0 {
		t.Fatalf("expected 0 error diagnostics in privileged mode, got %d:\n%s",
			len(errs), renderDiagList(errs))
	}
}

// --- End-to-end: unprivileged usage still rejected by E0770 ---

func TestEndToEndUnprivilegedRawPtrRejected(t *testing.T) {
	src := `
pub fn takesPtr(p: RawPtr) -> Int {
    0
}
`
	diags := runEndToEnd(t, src, false)
	if countByCode(diags, diag.CodeRuntimePrivilegeViolation) == 0 {
		t.Fatalf("expected at least one E0770 in unprivileged mode, got:\n%s",
			renderDiagList(diags))
	}
}

func TestEndToEndUnprivilegedPodStructWithRawPtrRejected(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
pub struct Header {
    pub next: RawPtr,
    pub size: Int,
}
`
	diags := runEndToEnd(t, src, false)
	// Expect several E0770: the two annotations on the struct + the
	// RawPtr field type.
	n := countByCode(diags, diag.CodeRuntimePrivilegeViolation)
	if n < 3 {
		t.Fatalf("expected at least 3 E0770 (two annotations + RawPtr field), got %d:\n%s",
			n, renderDiagList(diags))
	}
}

// --- RawPtr does not break ordinary user code that never mentions it ---

func TestEndToEndOrdinaryCodeUnaffected(t *testing.T) {
	src := `
pub struct User {
    pub name: String,
    pub age: Int,
}

pub fn greet(u: User) -> String {
    u.name
}
`
	diags := runEndToEnd(t, src, false)
	errs := errorsOnly(diags)
	if len(errs) > 0 {
		t.Fatalf("ordinary user code produced unexpected diagnostics:\n%s",
			renderDiagList(errs))
	}
}

// ---- helpers ----

func errorsOnly(ds []*diag.Diagnostic) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	for _, d := range ds {
		if d != nil && d.Severity == diag.Error {
			out = append(out, d)
		}
	}
	return out
}

func countByCode(ds []*diag.Diagnostic, code string) int {
	n := 0
	for _, d := range ds {
		if d != nil && d.Code == code {
			n++
		}
	}
	return n
}

func renderDiagList(ds []*diag.Diagnostic) string {
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
