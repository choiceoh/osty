package selfhost

import "testing"

// G44 — `#[stability(since = "X.Y")]` format gate (v0.6 §3.14.1).
// Phase 4 entry. Pre-release cut C unified the standalone `#[since]`
// annotation into the `since` keyword on `#[stability(...)]`, so the
// SemVer-shape regex now runs on the keyword position.
//
// Implementation lives in toolchain/resolve.osty
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
	src := []byte(`#[stability(level = "stable", since = "0.6")]
fn ok() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 0 {
		t.Fatalf("E0452 count = %d, want 0; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateAcceptsXYZ(t *testing.T) {
	src := []byte(`#[stability(level = "stable", since = "1.0.0")]
fn ok() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 0 {
		t.Fatalf("E0452 count = %d, want 0; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateAcceptsPreRelease(t *testing.T) {
	src := []byte(`#[stability(level = "experimental", since = "2.0.0-rc.1")]
fn ok() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 0 {
		t.Fatalf("E0452 count = %d, want 0; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateAcceptsPreReleaseHyphenOnly(t *testing.T) {
	src := []byte(`#[stability(level = "experimental", since = "1.0.0-alpha")]
fn ok() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 0 {
		t.Fatalf("E0452 count = %d, want 0; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateRejectsVPrefix(t *testing.T) {
	src := []byte(`#[stability(level = "stable", since = "v0.6")]
fn bad() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 1 {
		t.Fatalf("E0452 count = %d, want 1; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateRejectsWildcardTail(t *testing.T) {
	src := []byte(`#[stability(level = "stable", since = "0.6.x")]
fn bad() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 1 {
		t.Fatalf("E0452 count = %d, want 1; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateRejectsEmptyString(t *testing.T) {
	src := []byte(`#[stability(level = "stable", since = "")]
fn bad() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 1 {
		t.Fatalf("E0452 count = %d, want 1; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateRejectsAbcLiteral(t *testing.T) {
	src := []byte(`#[stability(level = "stable", since = "abc")]
fn bad() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 1 {
		t.Fatalf("E0452 count = %d, want 1; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateRejectsIntegerLiteral(t *testing.T) {
	src := []byte(`#[stability(level = "stable", since = 42)]
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

#[stability(level = "stable", since = "v{x}")]
fn other() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 1 {
		t.Fatalf("E0452 count = %d, want 1; diagnostics=%#v", got, result.Diagnostics)
	}
}

// Cut C: `until` and `remove` keywords on `#[stability(...)]` go through
// the same SemVer-shape gate. Each malformed value fires a separate
// E0452 (one diagnostic per offending keyword).
func TestSinceFormatGateRejectsBadUntilKeyword(t *testing.T) {
	src := []byte(`#[stability(level = "experimental", since = "0.6", until = "0.7.x")]
fn bad() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 1 {
		t.Fatalf("E0452 count = %d, want 1; diagnostics=%#v", got, result.Diagnostics)
	}
}

func TestSinceFormatGateRejectsBadRemoveKeyword(t *testing.T) {
	src := []byte(`#[stability(level = "deprecated", since = "0.6", remove = "v0.8")]
fn bad() {}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 1 {
		t.Fatalf("E0452 count = %d, want 1; diagnostics=%#v", got, result.Diagnostics)
	}
}

// Verify position coverage: `#[stability]` is permitted on fn / struct /
// enum variant / interface method / field. Valid SemVer strings on each
// site must produce zero E0452. (Cut C: stability now covers the field
// and variant positions previously held by the standalone `#[since]`.)
func TestSinceFormatGateAcceptsAllPermittedTargets(t *testing.T) {
	src := []byte(`#[stability(level = "stable", since = "0.6")]
struct A {
    #[stability(level = "stable", since = "0.6.1")]
    a: Int,
}

#[stability(level = "stable", since = "0.7")]
enum B {
    #[stability(level = "stable", since = "0.7.0")]
    Variant,
}

interface C {
    #[stability(level = "stable", since = "1.0")]
    fn m(self) -> Int
}
`)
	result := ResolveSourceStructured(src)
	if got := countSinceFormatCode(result, "E0452"); got != 0 {
		t.Fatalf("E0452 count = %d, want 0; diagnostics=%#v", got, result.Diagnostics)
	}
}
