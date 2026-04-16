package cst

import "testing"

// buildSample constructs a small tree that mirrors what a parser would
// produce for `a + bc`. Widths are chosen so absolute offsets are easy to
// verify by inspection:
//
//	a    width 1, offset 0
//	(space)    offset 1 width 1 (trivia, not part of node widths here)
//	+    width 1, offset 2
//	(space)    offset 3 width 1
//	bc   width 2, offset 4
//
// For this test we build nodes with just raw tokens, no trivia attachment,
// so the expected widths are 1 + 1 + 2 = 4. Trivia widths add to node width
// only when the builder calls Token() with trivia indices — the Token call
// in this fixture passes nil for leading/trailing.
func buildSample(t *testing.T) *Tree {
	t.Helper()
	b := NewBuilder(nil)
	b.StartNode(GkFile)
	b.StartNode(GkBinary)
	b.Token(GkToken, 0, "a", 1, nil, nil)
	b.Token(GkToken, 0, "+", 1, nil, nil)
	b.Token(GkToken, 0, "bc", 2, nil, nil)
	b.FinishNode() // Binary
	b.FinishNode() // File
	arena, root := b.Finish()
	return NewTree(arena, root)
}

func TestRedRootOffsets(t *testing.T) {
	tree := buildSample(t)
	root := tree.Root()
	if got, want := root.Offset(), 0; got != want {
		t.Errorf("root offset = %d, want %d", got, want)
	}
	if got, want := root.End(), 4; got != want {
		t.Errorf("root end = %d, want %d", got, want)
	}
	if k := root.Kind(); k != GkFile {
		t.Errorf("root kind = %v, want GkFile", k)
	}
}

func TestRedChildOffsetsPropagate(t *testing.T) {
	tree := buildSample(t)
	root := tree.Root()
	if got := root.ChildCount(); got != 1 {
		t.Fatalf("root has %d children, want 1", got)
	}
	binary := root.ChildAt(0)
	if k := binary.Kind(); k != GkBinary {
		t.Fatalf("child kind = %v, want GkBinary", k)
	}
	if got, want := binary.Offset(), 0; got != want {
		t.Errorf("binary offset = %d, want %d", got, want)
	}

	// Three tokens: a, +, bc at offsets 0, 1, 2.
	expectedOffsets := []int{0, 1, 2}
	expectedWidths := []int{1, 1, 2}
	if binary.ChildCount() != 3 {
		t.Fatalf("binary has %d children, want 3", binary.ChildCount())
	}
	for i := 0; i < 3; i++ {
		tok := binary.ChildAt(i)
		if got := tok.Offset(); got != expectedOffsets[i] {
			t.Errorf("child %d offset = %d, want %d", i, got, expectedOffsets[i])
		}
		if got := tok.Width(); got != expectedWidths[i] {
			t.Errorf("child %d width = %d, want %d", i, got, expectedWidths[i])
		}
		if !tok.IsToken() {
			t.Errorf("child %d should be a token", i)
		}
	}
}

func TestRedParentChain(t *testing.T) {
	tree := buildSample(t)
	root := tree.Root()
	binary := root.ChildAt(0)
	tok := binary.ChildAt(1)

	p1, ok := tok.Parent()
	if !ok {
		t.Fatal("token should have a parent")
	}
	if p1.Kind() != GkBinary {
		t.Errorf("token's parent kind = %v, want GkBinary", p1.Kind())
	}

	p2, ok := p1.Parent()
	if !ok {
		t.Fatal("binary should have a parent")
	}
	if p2.Kind() != GkFile {
		t.Errorf("binary's parent kind = %v, want GkFile", p2.Kind())
	}

	if _, ok := p2.Parent(); ok {
		t.Error("root should have no parent")
	}
}

func TestRedFirstLastToken(t *testing.T) {
	tree := buildSample(t)
	root := tree.Root()
	first, ok := root.FirstToken()
	if !ok {
		t.Fatal("root should reach a token")
	}
	if tok := tree.Arena.TokenAt(first.id); tok.Text != "a" {
		t.Errorf("first token text = %q, want %q", tok.Text, "a")
	}
	last, ok := root.LastToken()
	if !ok {
		t.Fatal("root should reach a token")
	}
	if tok := tree.Arena.TokenAt(last.id); tok.Text != "bc" {
		t.Errorf("last token text = %q, want %q", tok.Text, "bc")
	}
}

func TestRedFindCoveringNode(t *testing.T) {
	tree := buildSample(t)
	root := tree.Root()
	// Offset 0 is inside "a" — smallest covering is that token.
	found := root.FindCoveringNode(0)
	if !found.IsToken() {
		t.Fatalf("expected a token at offset 0, got kind=%v", found.Kind())
	}
	// Offset 1 is inside "+" (covers [1, 2) — half-open).
	found = root.FindCoveringNode(1)
	if tok := tree.Arena.TokenAt(found.id); tok.Text != "+" {
		t.Errorf("covering node at offset 1 should be the + token, got %q", tok.Text)
	}
	// Offset 2 is inside "bc" (covers [2, 4)).
	found = root.FindCoveringNode(2)
	if tok := tree.Arena.TokenAt(found.id); tok.Text != "bc" {
		t.Errorf("covering node at offset 2 should be bc token, got %q", tok.Text)
	}
	// Offset 4 is past the end — FindCoveringNode returns the receiver.
	found = root.FindCoveringNode(4)
	if !found.IsNode() || found.Kind() != GkFile {
		t.Errorf("out-of-range offset should return the receiver (File), got kind=%v", found.Kind())
	}
}

func TestRedWalkPreOrder(t *testing.T) {
	tree := buildSample(t)
	kinds := []GreenKind{}
	tree.Root().Walk(func(r Red) bool {
		kinds = append(kinds, r.Kind())
		return true
	})
	want := []GreenKind{GkFile, GkBinary, GkToken, GkToken, GkToken}
	if len(kinds) != len(want) {
		t.Fatalf("walk produced %v, want %v", kinds, want)
	}
	for i := range kinds {
		if kinds[i] != want[i] {
			t.Fatalf("walk[%d] = %v, want %v (full: %v)", i, kinds[i], want[i], kinds)
		}
	}
}

func TestRedChildCacheHits(t *testing.T) {
	tree := buildSample(t)
	root := tree.Root()
	binary := root.ChildAt(0)

	c1 := binary.ChildAt(1)
	c2 := binary.ChildAt(1)
	// Two lookups of the same child must return structurally equal Reds.
	if c1.Offset() != c2.Offset() || c1.id != c2.id {
		t.Errorf("repeated ChildAt should return the same Red; got %+v vs %+v", c1, c2)
	}
	// And the cache should have been consulted — check the underlying
	// size hasn't doubled per call.
	before := len(tree.reds)
	for i := 0; i < 10; i++ {
		binary.ChildAt(1)
	}
	after := len(tree.reds)
	if after != before {
		t.Errorf("reds slice grew from %d to %d across 10 cache hits", before, after)
	}
}
