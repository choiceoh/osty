package cst

import "testing"

// TestGreenBuilderBasic verifies the minimal build cycle: StartNode → Token →
// FinishNode produces a File node with one token child, correct width, and a
// reachable arena root.
func TestGreenBuilderBasic(t *testing.T) {
	b := NewBuilder(nil)
	b.StartNode(GkFile)
	b.Token(GkToken, 0 /* token.IDENT placeholder */, "x", 1, nil, nil)
	b.FinishNode()

	arena, root := b.Finish()
	if root < 0 {
		t.Fatal("expected a valid root id, got -1")
	}
	if len(arena.Nodes) != 1 {
		t.Fatalf("arena should have 1 node, got %d", len(arena.Nodes))
	}
	if len(arena.Tokens) != 1 {
		t.Fatalf("arena should have 1 token, got %d", len(arena.Tokens))
	}
	n := arena.NodeAt(root)
	if n.Kind != GkFile {
		t.Errorf("root kind = %v, want GkFile", n.Kind)
	}
	if n.Width != 1 {
		t.Errorf("root width = %d, want 1", n.Width)
	}
	if len(n.Children) != 1 || n.Children[0].Tag != GctToken {
		t.Fatalf("expected single token child, got %+v", n.Children)
	}
	tok := arena.TokenAt(n.Children[0].ID)
	if tok.Text != "x" {
		t.Errorf("child token text = %q, want %q", tok.Text, "x")
	}
}

// TestGreenBuilderNested checks multi-level construction and width
// aggregation. A Block containing two ExprStmts each containing an Ident
// should have width equal to the sum of all its descendants.
func TestGreenBuilderNested(t *testing.T) {
	b := NewBuilder(nil)
	b.StartNode(GkBlock)
	for _, name := range []string{"a", "bc"} {
		b.StartNode(GkExprStmt)
		b.Token(GkToken, 0, name, len(name), nil, nil)
		b.FinishNode()
	}
	b.FinishNode()
	arena, root := b.Finish()

	want := 1 + 2 // len("a") + len("bc")
	if got := arena.NodeAt(root).Width; got != want {
		t.Errorf("block width = %d, want %d", got, want)
	}
	if n := len(arena.NodeAt(root).Children); n != 2 {
		t.Fatalf("block should have 2 children, got %d", n)
	}
}

// TestGreenDedupTokens verifies identical tokens dedupe to one id. Parsing a
// source full of `,` punctuation is the common case where this matters.
func TestGreenDedupTokens(t *testing.T) {
	arena := NewArena()
	id1 := arena.AddToken(GreenToken{Kind: GkToken, TokenKind: 42, Text: ",", Width: 1})
	id2 := arena.AddToken(GreenToken{Kind: GkToken, TokenKind: 42, Text: ",", Width: 1})
	id3 := arena.AddToken(GreenToken{Kind: GkToken, TokenKind: 42, Text: ",", Width: 1})
	if id1 != id2 || id2 != id3 {
		t.Fatalf("expected identical tokens to share id, got %d %d %d", id1, id2, id3)
	}
	if got := len(arena.Tokens); got != 1 {
		t.Errorf("arena should hold 1 token, got %d", got)
	}
	if hits := arena.DedupHits(); hits != 2 {
		t.Errorf("dedup hits = %d, want 2", hits)
	}
	if misses := arena.DedupMisses(); misses != 1 {
		t.Errorf("dedup misses = %d, want 1", misses)
	}
}

// TestGreenDedupTokensDistinguishedByTriviaIndices ensures tokens with
// different attached trivia indices are NOT deduplicated, even if the text
// matches. Leading / trailing trivia carry source-specific positions, so the
// arena must keep them separate.
func TestGreenDedupTokensDistinguishedByTriviaIndices(t *testing.T) {
	arena := NewArena()
	id1 := arena.AddToken(GreenToken{Kind: GkToken, Text: "x", Width: 1, LeadingTrivia: []int{0}})
	id2 := arena.AddToken(GreenToken{Kind: GkToken, Text: "x", Width: 1, LeadingTrivia: []int{1}})
	if id1 == id2 {
		t.Fatalf("tokens with different leading trivia should have different ids; both got %d", id1)
	}
}

// TestGreenDedupNodes verifies structural sharing: the same subtree built
// twice shares one node id.
func TestGreenDedupNodes(t *testing.T) {
	arena := NewArena()
	mk := func() int {
		tid := arena.AddToken(GreenToken{Kind: GkToken, Text: "x", Width: 1})
		return arena.AddNode(GreenNode{
			Kind:     GkIdent,
			Width:    1,
			Children: []GreenChild{{Tag: GctToken, ID: tid}},
		})
	}
	a := mk()
	b := mk()
	if a != b {
		t.Fatalf("identical subtrees should share id, got %d vs %d", a, b)
	}
	if got := len(arena.Nodes); got != 1 {
		t.Errorf("arena should hold 1 node, got %d", got)
	}
}

// TestGreenKindNameCoverage catches forgotten-entry mistakes in
// greenKindNames. Every iota in the const block above must have a label.
func TestGreenKindNameCoverage(t *testing.T) {
	// Iterate through the declared range. If a new kind is added without
	// updating greenKindNames, its String() will fall back to GreenKind(N),
	// which the eye catches in snapshot diffs; this test makes the failure
	// explicit.
	for k := GkNone; k <= GkErrorExtra; k++ {
		if _, ok := greenKindNames[k]; !ok {
			t.Errorf("greenKindNames missing label for kind %d (GreenKind(%d))", int(k), int(k))
		}
	}
}
