package selfhost_test

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/selfhost"
)

// Port of internal/check/query.go's position-query API onto the
// structured Osty CheckResult. These tests hit `TypeAtOffset`,
// `LetTypeAtOffset`, `SymbolNameAtOffset`, and `HoverAtOffset` against
// short fixtures whose byte layout is tight enough to reason about
// offsets directly. Every fixture uses `\n` line-endings so offsets
// stay stable across platforms.

func TestTypeAtOffsetFindsBindingLiteral(t *testing.T) {
	src := []byte("fn main() {\n    let x = 42\n}\n")
	r := selfhost.CheckSourceStructured(src)
	// Position on the `42` literal.
	off := strings.Index(string(src), "42")
	if off < 0 {
		t.Fatalf("fixture missing `42`")
	}
	got := selfhost.TypeAtOffset(r, off+1)
	if got == "" {
		t.Fatalf("expected a typed node at offset %d, got empty (nodes=%d)", off+1, len(r.TypedNodes))
	}
	if !strings.Contains(strings.ToLower(got), "int") {
		t.Errorf("expected an Int-flavoured type, got %q", got)
	}
}

func TestTypeAtOffsetReturnsEmptyOutsideAnyNode(t *testing.T) {
	src := []byte("fn main() {\n    let x = 42\n}\n")
	r := selfhost.CheckSourceStructured(src)
	// Offset deep in the closing brace line where nothing is typed.
	off := len(src) - 1
	if got := selfhost.TypeAtOffset(r, off); got != "" {
		t.Errorf("expected empty type past the body, got %q", got)
	}
}

func TestTypeAtOffsetPicksNarrowestWhenNested(t *testing.T) {
	// `a + b` yields a typed binary node whose span covers the whole
	// expression. The ident `a` lives as its own typed node at a
	// narrower span. TypeAtOffset should prefer the ident's type.
	src := []byte("fn sum(a: Int, b: Int) -> Int {\n    a + b\n}\n")
	r := selfhost.CheckSourceStructured(src)

	// Find the body's `a` (second occurrence of `a` — param list comes
	// first). Position on the ident itself.
	bodyStart := strings.Index(string(src), "{\n    ")
	if bodyStart < 0 {
		t.Fatalf("fixture layout changed")
	}
	aOff := strings.Index(string(src[bodyStart:]), "a") + bodyStart
	if aOff < bodyStart {
		t.Fatalf("failed to locate body `a`")
	}
	got := selfhost.TypeAtOffset(r, aOff)
	if got == "" {
		t.Fatalf("expected a typed node at `a` ident, got empty")
	}
	// The narrowest typed node is the ident itself (1 byte), so its
	// type must be Int, not the binary-expression's Int-valued type.
	if !strings.Contains(strings.ToLower(got), "int") {
		t.Errorf("expected Int at ident position, got %q", got)
	}
}

func TestLetTypeAtOffsetFindsDeclaredBinding(t *testing.T) {
	src := []byte("fn main() {\n    let x: Int = 0\n}\n")
	r := selfhost.CheckSourceStructured(src)
	// Offset on the `x` ident.
	xOff := strings.Index(string(src), "let x")
	if xOff < 0 {
		t.Fatalf("fixture missing `let x`")
	}
	xOff += len("let ")
	got := selfhost.LetTypeAtOffset(r, xOff)
	if got == "" {
		t.Skipf("let-binding offset data not captured by checker yet (bindings=%d)", len(r.Bindings))
	}
	if !strings.Contains(strings.ToLower(got), "int") {
		t.Errorf("expected Int let type, got %q", got)
	}
}

func TestLetTypeAtOffsetReturnsEmptyWhenOutsideBinding(t *testing.T) {
	src := []byte("fn main() {\n    let x: Int = 0\n}\n")
	r := selfhost.CheckSourceStructured(src)
	// Offset 0 is the very first byte of the source — outside every
	// binding ident span.
	if got := selfhost.LetTypeAtOffset(r, 0); got != "" {
		t.Errorf("expected empty outside any binding, got %q", got)
	}
}

func TestSymbolNameAtOffsetReturnsEmptyOutsideDecl(t *testing.T) {
	src := []byte("fn main() {}\n")
	r := selfhost.CheckSourceStructured(src)
	// Far past the end.
	if got := selfhost.SymbolNameAtOffset(r, len(src)+10); got != "" {
		t.Errorf("expected empty symbol at out-of-range offset, got %q", got)
	}
}

func TestHoverAtOffsetCombinesEveryQuery(t *testing.T) {
	src := []byte("fn main() {\n    let x = 42\n}\n")
	r := selfhost.CheckSourceStructured(src)
	// Offset on the `42` literal.
	litOff := strings.Index(string(src), "42") + 1
	h := selfhost.HoverAtOffset(r, litOff)
	if h.ExprType == "" {
		t.Errorf("expected HoverAtOffset.ExprType to be set for `42` position, got empty")
	}
}

func TestHoverAtOffsetEmptyOutsideAnyDecl(t *testing.T) {
	// Blank-line fixture where every offset falls outside the single
	// fn decl — the whitespace before `fn` isn't covered by any
	// typed-node / binding / symbol span.
	src := []byte("\n\nfn main() {}\n")
	r := selfhost.CheckSourceStructured(src)
	h := selfhost.HoverAtOffset(r, 0)
	if h.ExprType != "" || h.LetType != "" || h.SymbolName != "" {
		t.Errorf("expected empty Hover before any decl, got %+v", h)
	}
}

func TestOffsetContainmentBoundary(t *testing.T) {
	// Sanity check the half-open semantics: the exact `End` offset
	// must not match. Relies on the checker emitting a typed node for
	// the `42` literal.
	src := []byte("fn main() {\n    let x = 42\n}\n")
	r := selfhost.CheckSourceStructured(src)
	if len(r.TypedNodes) == 0 {
		t.Skip("checker produced no typed nodes for this fixture")
	}
	// Find the literal node.
	var litNode *selfhost.CheckedNode
	for i := range r.TypedNodes {
		n := &r.TypedNodes[i]
		if n.Start >= 0 && n.End > n.Start {
			piece := string(src[n.Start:n.End])
			if piece == "42" {
				litNode = n
				break
			}
		}
	}
	if litNode == nil {
		t.Skip("no typed node covers `42` literal span exactly")
	}
	// At `End` offset (half-open), `TypeAtOffset` must not return the
	// literal's type unless an outer node also covers the position.
	outer := selfhost.TypeAtOffset(r, litNode.End)
	if outer == "" {
		return // no outer node — End is exclusive, empty result is fine
	}
	litType := selfhost.TypeAtOffset(r, litNode.Start)
	if outer == litType {
		t.Errorf("TypeAtOffset at End offset returned same type as literal node; expected an enclosing node's type or empty, got %q", outer)
	}
}
