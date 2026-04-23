package check

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/types"
)

// runEndToEnd drives the self-host checker (which runs §19 gates +
// resolve + elab internally via toolchain/check_gates.osty::runCheckGates)
// against src and returns the gate-band plus resolver diagnostics lifted
// into diag.Diagnostic form. This narrows the view to what the retired
// Go-side `runPrivilegeGate + runPodShapeChecks + runNoAllocChecks +
// res.Diags` stack used to surface — full elab adds its own error
// classes (E0700 type mismatches etc.) that the RawPtr tests never
// asserted against. When privileged is true, E0770 is stripped after
// the fact so callers observing privileged-mode output see the same
// shape the host boundary produces (host_boundary.go strips the code
// via shouldSuppressNativeDiag + nativeCheckerSummary).
func runEndToEnd(t *testing.T, src string, privileged bool) []*diag.Diagnostic {
	t.Helper()
	result := selfhost.CheckSourceStructured([]byte(src))
	diags := selfhost.CheckDiagnosticsAsDiag([]byte(src), result.Diagnostics)
	out := make([]*diag.Diagnostic, 0, len(diags))
	for _, d := range diags {
		if d == nil {
			continue
		}
		if privileged && d.Code == diag.CodeRuntimePrivilegeViolation {
			continue
		}
		if isGateOrResolveDiagCode(d.Code) {
			out = append(out, d)
		}
	}
	return out
}

// isGateOrResolveDiagCode matches the diagnostic codes the original
// runEndToEnd harness surfaced: the four §19 policy gates + the
// resolver band. Elab-level errors (E0700 family) are intentionally
// omitted — the original path did not run elab.
func isGateOrResolveDiagCode(code string) bool {
	switch code {
	case "E0770", "E0771", "E0772", "E0773":
		return true
	}
	// Resolver codes are E0400–E0506 (see internal/diag/codes.go).
	if len(code) == 5 && code[0] == 'E' {
		if code >= "E0400" && code <= "E0506" {
			return true
		}
	}
	return false
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
