package check

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
)

func runIntrinsicBody(t *testing.T, src string) []*diag.Diagnostic {
	t.Helper()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse: %v", parseDiags)
	}
	return runIntrinsicBodyChecks(file)
}

func countIntrinsicBodyDiags(ds []*diag.Diagnostic) int {
	n := 0
	for _, d := range ds {
		if d != nil && d.Code == diag.CodeIntrinsicNonEmptyBody {
			n++
		}
	}
	return n
}

// --- positive: canonical empty forms accepted ---

func TestIntrinsicBodylessAccepted(t *testing.T) {
	src := `
#[intrinsic]
pub fn raw_null() -> Int

fn main() {}
`
	if got := countIntrinsicBodyDiags(runIntrinsicBody(t, src)); got != 0 {
		t.Fatalf("body-less #[intrinsic] must pass, got %d E0773", got)
	}
}

func TestIntrinsicEmptyBlockAccepted(t *testing.T) {
	src := `
#[intrinsic]
pub fn raw_null() -> Int { }

fn main() {}
`
	if got := countIntrinsicBodyDiags(runIntrinsicBody(t, src)); got != 0 {
		t.Fatalf("#[intrinsic] with `{}` must pass, got %d E0773", got)
	}
}

// --- negative: non-empty body rejected ---

func TestIntrinsicNonEmptyBodyRejected(t *testing.T) {
	src := `
#[intrinsic]
pub fn raw_null() -> Int { 0 }

fn main() {}
`
	if got := countIntrinsicBodyDiags(runIntrinsicBody(t, src)); got != 1 {
		t.Fatalf("expected 1 E0773 for `{ 0 }` body, got %d", got)
	}
}

func TestIntrinsicMultiStmtBodyRejected(t *testing.T) {
	src := `
#[intrinsic]
pub fn raw_complex() -> Int {
    let x = 0
    x + 1
}

fn main() {}
`
	if got := countIntrinsicBodyDiags(runIntrinsicBody(t, src)); got != 1 {
		t.Fatalf("expected 1 E0773 for multi-stmt body, got %d", got)
	}
}

// --- regression guards: ordinary fns / non-#[intrinsic] decorated fns ---

func TestIntrinsicLeavesOrdinaryFnAlone(t *testing.T) {
	src := `
pub fn ordinary() -> Int { 42 }

fn main() {}
`
	if got := countIntrinsicBodyDiags(runIntrinsicBody(t, src)); got != 0 {
		t.Fatalf("ordinary fn must not produce E0773, got %d", got)
	}
}

func TestIntrinsicLeavesNoAllocFnAlone(t *testing.T) {
	// Other runtime annotations on a fn with body are fine — only
	// #[intrinsic] requires emptiness.
	src := `
#[no_alloc]
pub fn pure(a: Int, b: Int) -> Int {
    a + b
}

fn main() {}
`
	if got := countIntrinsicBodyDiags(runIntrinsicBody(t, src)); got != 0 {
		t.Fatalf("#[no_alloc] fn with body must not produce E0773, got %d", got)
	}
}

// --- struct + enum methods ---

func TestIntrinsicStructMethodNonEmptyRejected(t *testing.T) {
	src := `
struct S {
    pub x: Int,

    #[intrinsic]
    pub fn raw_get(self) -> Int { self.x }
}
`
	if got := countIntrinsicBodyDiags(runIntrinsicBody(t, src)); got != 1 {
		t.Fatalf("expected 1 E0773 for #[intrinsic] struct method with body, got %d", got)
	}
}

func TestIntrinsicEnumMethodEmptyAccepted(t *testing.T) {
	src := `
enum E {
    A,
    B,

    #[intrinsic]
    pub fn raw_tag(self) -> Int { }
}
`
	if got := countIntrinsicBodyDiags(runIntrinsicBody(t, src)); got != 0 {
		t.Fatalf("#[intrinsic] enum method with `{}` must pass, got %d", got)
	}
}

// --- diagnostic shape ---

func TestIntrinsicDiagnosticNamesFunctionAndSpec(t *testing.T) {
	src := `
#[intrinsic]
pub fn poison() -> Int { 1 }
`
	out := runIntrinsicBody(t, src)
	if len(out) == 0 {
		t.Fatal("expected at least one diagnostic")
	}
	for _, d := range out {
		if d.Code != diag.CodeIntrinsicNonEmptyBody {
			continue
		}
		if !strings.Contains(d.Message, "poison") {
			t.Fatalf("expected diagnostic to name `poison`, got: %s", d.Message)
		}
		joined := d.Message
		for _, n := range d.Notes {
			joined += " " + n
		}
		if !strings.Contains(joined, "§19.6") {
			t.Fatalf("expected diagnostic notes to reference §19.6, got: %s", joined)
		}
		return
	}
	t.Fatal("no E0773 found")
}

// --- multiple intrinsics in one file ---

func TestIntrinsicMultipleViolations(t *testing.T) {
	src := `
#[intrinsic]
pub fn ok_empty() -> Int { }

#[intrinsic]
pub fn bad_one() -> Int { 1 }

#[intrinsic]
pub fn bad_two() -> Int { 2 }

fn main() {}
`
	if got := countIntrinsicBodyDiags(runIntrinsicBody(t, src)); got != 2 {
		t.Fatalf("expected 2 E0773 (bad_one, bad_two), got %d", got)
	}
}
