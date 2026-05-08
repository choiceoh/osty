package selfhost

import "testing"

// G44 — `#[since("X.Y")]` format gate (v0.6 §3.14.1).
// Phase 4 entry. Implementation lives in toolchain/resolve.osty
// (mirrored in internal/selfhost/generated.go); these tests pin the
// exact diagnostic surface the resolver produces.

func countSinceFormatCode(result ResolveResult, code string) int {
	count := 0
	for _, d := range result.Diagnostics {
		if d.Code == code {
			count++
		}
	}
	return count
}

func TestSinceFormatGateAcceptsXY(t *testing.T) {
	src := []byte(`#[since("0.6")]
fn ok() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 0 {
		t.Fatalf("E0452 count = %d, want 0; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateAcceptsXYZ(t *testing.T) {
	src := []byte(`#[since("1.0.0")]
fn ok() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 0 {
		t.Fatalf("E0452 count = %d, want 0; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateAcceptsPreRelease(t *testing.T) {
	src := []byte(`#[since("2.0.0-rc.1")]
fn ok() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 0 {
		t.Fatalf("E0452 count = %d, want 0; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateAcceptsPreReleaseHyphenOnly(t *testing.T) {
	src := []byte(`#[since("1.0.0-alpha")]
fn ok() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 0 {
		t.Fatalf("E0452 count = %d, want 0; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateRejectsVPrefix(t *testing.T) {
	src := []byte(`#[since("v0.6")]
fn bad() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 1 {
		t.Fatalf("E0452 count = %d, want 1; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateRejectsWildcardTail(t *testing.T) {
	src := []byte(`#[since("0.6.x")]
fn bad() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 1 {
		t.Fatalf("E0452 count = %d, want 1; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateRejectsEmptyString(t *testing.T) {
	src := []byte(`#[since("")]
fn bad() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 1 {
		t.Fatalf("E0452 count = %d, want 1; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateRejectsAbcLiteral(t *testing.T) {
	src := []byte(`#[since("abc")]
fn bad() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 1 {
		t.Fatalf("E0452 count = %d, want 1; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateRejectsIntegerLiteral(t *testing.T) {
	src := []byte(`#[since(42)]
fn bad() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 1 {
		t.Fatalf("E0452 count = %d, want 1; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateRejectsBareFlag(t *testing.T) {
	// `#[since]` with no parens — no args at all.
	src := []byte(`#[since]
fn bad() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 1 {
		t.Fatalf("E0452 count = %d, want 1; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateRejectsTwoArgs(t *testing.T) {
	src := []byte(`#[since("0.6", "0.7")]
fn bad() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 1 {
		t.Fatalf("E0452 count = %d, want 1; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateRejectsKeywordArg(t *testing.T) {
	src := []byte(`#[since(version = "0.6")]
fn bad() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 1 {
		t.Fatalf("E0452 count = %d, want 1; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateRejectsInterpolatedString(t *testing.T) {
	src := []byte(`fn bad() {
    let x = 1
    let _ = x
}

#[since("v{x}")]
fn other() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 1 {
		t.Fatalf("E0452 count = %d, want 1; diagnostics=%#v", got, result.Diagnostics)
	}
}

// Verify position coverage: #[since] is permitted on fn / struct /
// enum variant / interface method / field. Valid SemVer strings on
// each site must produce zero E0452.
func TestSinceFormatGateAcceptsAllPermittedTargets(t *testing.T) {
	src := []byte(`#[since("0.6")]
struct A {
    #[since("0.6.1")]
    a: Int,
}

#[since("0.7")]
enum B {
    #[since("0.7.0")]
    Variant,
}

interface C {
    #[since("1.0")]
    fn m(self) -> Int
}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 0 {
		t.Fatalf("E0452 count = %d, want 0; diagnostics=%#v", got, result.Diagnostics)
	}
}
