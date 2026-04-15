package docgen

import (
	"strings"
	"testing"
)

// TestExamplesExtracted ensures Examples() walks top-level decls AND
// their nested methods so a struct with per-method examples surfaces
// them in a flat list the verifier can iterate.
func TestExamplesExtracted(t *testing.T) {
	pkg := parseSource(t, `
/// A type.
///
/// Example:
///     let u = User.new("a")
pub struct User {
    pub name: String,

    /// Greet.
    ///
    /// Example:
    ///     u.greet()
    pub fn greet(self) -> String { "hi" }
}
`)
	got := Examples(pkg)
	if len(got) != 2 {
		t.Fatalf("expected 2 examples (one per-decl), got %d: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Code, "User.new") {
		t.Errorf("first example should be the struct's: %q", got[0].Code)
	}
	if !strings.Contains(got[1].Code, "u.greet()") {
		t.Errorf("second example should be the method's: %q", got[1].Code)
	}
}

// TestVerifyExamplesHappyPath — a syntactically valid example must
// not produce an error. This is the "passing CI" case.
func TestVerifyExamplesHappyPath(t *testing.T) {
	pkg := parseSource(t, `
/// Add.
///
/// Example:
///     let z = 1 + 2
pub fn add(x: Int, y: Int) -> Int { x + y }
`)
	errs := VerifyExamples(pkg)
	if len(errs) != 0 {
		t.Errorf("expected no errors, got %d: %+v", len(errs), errs)
	}
}

// TestVerifyExamplesCatchesBreakage — if an example has a syntax
// error the verifier surfaces it with enough context (module, decl
// name, line positions) for the user to find the source of the drift.
func TestVerifyExamplesCatchesBreakage(t *testing.T) {
	pkg := parseSource(t, `
/// Broken.
///
/// Example:
///     let x ===== broken
pub fn bad() -> Int { 0 }
`)
	errs := VerifyExamples(pkg)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d: %+v", len(errs), errs)
	}
	msg := errs[0].Format()
	if !strings.Contains(msg, "function bad") {
		t.Errorf("error msg should mention the decl: %q", msg)
	}
	if len(errs[0].Diags) == 0 {
		t.Errorf("expected at least one parse diag attached")
	}
}
