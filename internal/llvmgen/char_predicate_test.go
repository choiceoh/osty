package llvmgen

import (
	"strings"
	"testing"
)

// Char predicates and case conversion (`c.isDigit()`, `c.toUpper()`,
// etc.) declared as `#[intrinsic_methods]` placeholders in
// primitives/char.osty had no LLVM dispatcher, so any injected stdlib
// body that used them (e.g. `strings.toUpper(s)` walks each char with
// `c.toUpper()`) tripped LLVM015 "call target *ast.FieldExpr".
//
// emitCharPredicateCall lowers the ASCII fast path entirely in IR:
// `c - low <u count` for is-checks (`(c-'A') <u 26` is `c >= 'A' &&
// c < 'A'+26`), `select` for the case shifts. No new C runtime
// symbols — Unicode case folding is a separate follow-up.

func TestCharIsDigitLowersToAsciiRangeCheck(t *testing.T) {
	file := parseLLVMGenFile(t, `fn check(c: Char) -> Bool {
    c.isDigit()
}

fn main() {
    let _ = check('5')
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/char_isdigit.osty"})
	if err != nil {
		t.Fatalf("Char.isDigit errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"sub i32 ",      // c - '0'
		"icmp ult i32 ", // <u 10
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Char.isDigit missing %q in IR:\n%s", want, got)
		}
	}
}

func TestCharIsAlphaLowersAsOrOfTwoRanges(t *testing.T) {
	file := parseLLVMGenFile(t, `fn check(c: Char) -> Bool {
    c.isAlpha()
}

fn main() {
    let _ = check('A')
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/char_isalpha.osty"})
	if err != nil {
		t.Fatalf("Char.isAlpha errored: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, "or i1 ") {
		t.Fatalf("Char.isAlpha did not emit `or i1` for upper||lower:\n%s", got)
	}
	// Two unsigned-range checks (one for 'A'..'Z', one for 'a'..'z').
	if strings.Count(got, "icmp ult i32 ") < 2 {
		t.Fatalf("Char.isAlpha did not emit two unsigned range checks:\n%s", got)
	}
}

func TestCharIsWhitespaceLowersAsFourEqChecks(t *testing.T) {
	file := parseLLVMGenFile(t, `fn check(c: Char) -> Bool {
    c.isWhitespace()
}

fn main() {
    let _ = check(' ')
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/char_iswhitespace.osty"})
	if err != nil {
		t.Fatalf("Char.isWhitespace errored: %v", err)
	}
	got := string(ir)
	if strings.Count(got, "icmp eq i32 ") < 4 {
		t.Fatalf("Char.isWhitespace did not emit four eq checks (SP/TAB/LF/CR):\n%s", got)
	}
}

func TestCharToLowerUsesSelectShift(t *testing.T) {
	file := parseLLVMGenFile(t, `fn lower(c: Char) -> Char {
    c.toLower()
}

fn main() {
    let _ = lower('A')
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/char_tolower.osty"})
	if err != nil {
		t.Fatalf("Char.toLower errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"icmp ult i32 ", // upper-range check
		"add i32 ",      // c + 32
		"select i1 ",    // pick shifted vs original
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Char.toLower missing %q in IR:\n%s", want, got)
		}
	}
}

func TestCharToUpperUsesSelectShiftBackwards(t *testing.T) {
	file := parseLLVMGenFile(t, `fn upper(c: Char) -> Char {
    c.toUpper()
}

fn main() {
    let _ = upper('a')
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/char_toupper.osty"})
	if err != nil {
		t.Fatalf("Char.toUpper errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"icmp ult i32 ",
		"sub i32 ", // c - 32 (the shift down for lower → upper)
		"select i1 ",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Char.toUpper missing %q in IR:\n%s", want, got)
		}
	}
}

// End-to-end injection (the case that actually motivated this PR —
// `strings.toUpper(s)`'s body walks chars and calls `c.toUpper()`)
// is exercised at the backend layer where PrepareEntry runs the
// stdlib injection pipeline; the llvmgen layer only sees the
// already-lowered body. The e2e contract is locked in by
// TestPrepareEntryInjectsStdlibWhenFlagOn under internal/backend.
//
// The unit tests above cover this PR's actual surface: every Char
// predicate / case method now lowers to its expected LLVM IR shape.
// Verified manually via `osty build --backend llvm --emit llvm-ir`
// on a `use std.strings; strings.toUpper("hello world")` program —
// the resulting main.ll contains both
// `define ptr @osty_std_strings__toUpper(ptr %s)` and the inner
// `select i1 %t16, i32 %t17, i32 %t13` from `c.toUpper()`.
