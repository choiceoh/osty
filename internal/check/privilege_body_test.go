package check

import (
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
)

// privilegeGateOnly parses the source and runs ONLY the privilege
// gate (no resolver, no other checkers), returning the gate's
// diagnostic list. This isolates the body-walk coverage from unrelated
// resolver noise such as "unknown name `raw`" when the source uses
// an unimported runtime namespace.
func privilegeGateOnly(t *testing.T, src string) []*diag.Diagnostic {
	t.Helper()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse diagnostics: %v", parseDiags)
	}
	return runPrivilegeGate(file, false)
}

func countPrivilegeE0770(ds []*diag.Diagnostic) int {
	n := 0
	for _, d := range ds {
		if d != nil && d.Code == diag.CodeRuntimePrivilegeViolation {
			n++
		}
	}
	return n
}

// --- let-type annotations inside fn bodies ---

func TestPrivilegeBodyLetRawPtr(t *testing.T) {
	src := `
pub fn demo() {
    let p: RawPtr = 0
}
`
	if got := countPrivilegeE0770(privilegeGateOnly(t, src)); got != 1 {
		t.Fatalf("expected 1 E0770 for let: RawPtr in fn body, got %d", got)
	}
}

func TestPrivilegeBodyLetOptionRawPtr(t *testing.T) {
	src := `
pub fn demo() {
    let p: Option<RawPtr> = None
}
`
	if got := countPrivilegeE0770(privilegeGateOnly(t, src)); got != 1 {
		t.Fatalf("expected 1 E0770 for Option<RawPtr> in fn body, got %d", got)
	}
}

func TestPrivilegeBodyLetTupleRawPtr(t *testing.T) {
	src := `
pub fn demo() {
    let p: (Int, RawPtr) = (0, 0)
}
`
	if got := countPrivilegeE0770(privilegeGateOnly(t, src)); got != 1 {
		t.Fatalf("expected 1 E0770 for (Int, RawPtr) in fn body, got %d", got)
	}
}

func TestPrivilegeBodyNestedLetBlocks(t *testing.T) {
	src := `
pub fn demo() {
    if true {
        let p: RawPtr = 0
    }
}
`
	if got := countPrivilegeE0770(privilegeGateOnly(t, src)); got != 1 {
		t.Fatalf("expected 1 E0770 for let: RawPtr inside if-body, got %d", got)
	}
}

func TestPrivilegeBodyForLoopLet(t *testing.T) {
	src := `
pub fn demo() {
    for i in 0..10 {
        let p: RawPtr = 0
    }
}
`
	if got := countPrivilegeE0770(privilegeGateOnly(t, src)); got != 1 {
		t.Fatalf("expected 1 E0770 for let: RawPtr inside for-body, got %d", got)
	}
}

// --- turbofish type args ---

func TestPrivilegeBodyTurbofishRawPtr(t *testing.T) {
	src := `
pub fn demo() {
    foo::<RawPtr>()
}
`
	if got := countPrivilegeE0770(privilegeGateOnly(t, src)); got != 1 {
		t.Fatalf("expected 1 E0770 for foo::<RawPtr>, got %d", got)
	}
}

func TestPrivilegeBodyNestedTurbofishRawPtr(t *testing.T) {
	src := `
pub fn demo() {
    foo::<List<RawPtr>>()
}
`
	if got := countPrivilegeE0770(privilegeGateOnly(t, src)); got != 1 {
		t.Fatalf("expected 1 E0770 for foo::<List<RawPtr>>, got %d", got)
	}
}

// --- closure parameter type annotations ---

func TestPrivilegeBodyClosureRawPtrParam(t *testing.T) {
	src := `
pub fn demo() {
    let f = |p: RawPtr| p
    f(0)
}
`
	if got := countPrivilegeE0770(privilegeGateOnly(t, src)); got != 1 {
		t.Fatalf("expected 1 E0770 for closure |p: RawPtr|, got %d", got)
	}
}

func TestPrivilegeBodyClosureRawPtrReturn(t *testing.T) {
	src := `
pub fn demo() {
    let f = |x: Int| -> RawPtr { 0 }
}
`
	if got := countPrivilegeE0770(privilegeGateOnly(t, src)); got != 1 {
		t.Fatalf("expected 1 E0770 for closure -> RawPtr, got %d", got)
	}
}

// --- false-positive guard: ordinary body contents unaffected ---

func TestPrivilegeBodyOrdinaryLetIntUnaffected(t *testing.T) {
	src := `
pub fn demo() {
    let p: Int = 0
}
`
	if got := countPrivilegeE0770(privilegeGateOnly(t, src)); got != 0 {
		t.Fatalf("expected 0 E0770 for let: Int, got %d", got)
	}
}

func TestPrivilegeBodyOrdinaryTurbofishUnaffected(t *testing.T) {
	src := `
pub fn demo() {
    foo::<Int>()
    bar::<List<String>>()
}
`
	if got := countPrivilegeE0770(privilegeGateOnly(t, src)); got != 0 {
		t.Fatalf("expected 0 E0770 for ordinary turbofish, got %d", got)
	}
}

func TestPrivilegeBodyOrdinaryClosureUnaffected(t *testing.T) {
	src := `
pub fn demo() {
    let f = |p: String| -> Int { 0 }
}
`
	if got := countPrivilegeE0770(privilegeGateOnly(t, src)); got != 0 {
		t.Fatalf("expected 0 E0770 for closure (String) -> Int, got %d", got)
	}
}

// --- privileged bypass path: body contents accepted when privileged ---

func TestPrivilegeBodyAllowedWhenPrivileged(t *testing.T) {
	src := `
pub fn demo() {
    let p: RawPtr = 0
    let q: Option<RawPtr> = None
    foo::<RawPtr>()
}
`
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse diagnostics: %v", parseDiags)
	}
	ds := runPrivilegeGate(file, true)
	if got := countPrivilegeE0770(ds); got != 0 {
		t.Fatalf("privileged body must produce 0 E0770, got %d", got)
	}
}
