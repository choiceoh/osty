package cst

import "github.com/osty/osty/internal/token"

// Red is a lazy view onto a Green subtree that adds absolute source positions
// and parent pointers without touching the Green data. Two Red handles may
// share the same Green subtree, which is how structural sharing on the Green
// side becomes invisible to consumers: each Red carries its own absolute
// offset.
//
// The zero value is not a valid handle — always obtain one via Tree.Root or
// the traversal helpers on Red.
type Red struct {
	tree           *Tree
	parent         int // index into tree.reds; -1 for root
	indexInParent  int // position among parent's children; -1 for root
	absoluteOffset int
	// green identifies the underlying green entry. We keep both the tag
	// and the id so the node / token dispatch is straightforward.
	tag  GreenChildTag // GctNode or GctToken
	id   int           // arena id
}

// Tree couples a Green arena with a Red side-table. The side-table caches
// materialized Red handles keyed by (parent index, child slot) so absolute
// offsets are computed only once per visit.
//
// Source holds the normalized source bytes the tree was built from. It is
// used by round-trip tests and by consumers (formatter, LSP) that need to
// read trivia text by offset. Trivia records carry only positions, not the
// text itself, so without Source the tree can't be textualized.
type Tree struct {
	Arena  *GreenArena
	Root_  int // root node id (suffixed to avoid the Root() method naming clash)
	Source []byte

	reds []redEntry
	// cache for child red handles: key = (parentRedIdx, childIdx)
	childCache map[redChildKey]int
}

type redEntry struct {
	red Red
}

type redChildKey struct {
	parent int
	child  int
}

// NewTree wraps an arena + root node id in a Tree ready for Red traversal.
// The arena must outlive the Tree; the Tree mutates only its own Red
// side-table. Source is optional — callers that only traverse by offset can
// pass nil, but reconstruction requires it.
func NewTree(arena *GreenArena, rootNodeID int) *Tree {
	return NewTreeFromSource(arena, rootNodeID, nil)
}

// NewTreeFromSource is NewTree that also records the source bytes so
// round-trip reconstruction works.
func NewTreeFromSource(arena *GreenArena, rootNodeID int, source []byte) *Tree {
	t := &Tree{
		Arena:      arena,
		Root_:      rootNodeID,
		Source:     source,
		childCache: make(map[redChildKey]int),
	}
	return t
}

// Root returns a Red handle for the tree's root node.
func (t *Tree) Root() Red {
	if len(t.reds) == 0 {
		t.reds = append(t.reds, redEntry{red: Red{
			tree:          t,
			parent:        -1,
			indexInParent: -1,
			tag:           GctNode,
			id:            t.Root_,
		}})
	}
	return t.reds[0].red
}

// redIndex returns the index of r in the tree's reds slice, allocating it if
// not already there. Used when building child handles so cache keys can use
// parent indices.
func (t *Tree) redIndex(r Red) int {
	// The root is always index 0 (pushed in Root()). Other reds are
	// allocated on demand by ChildAt(). We identify a Red by its
	// (parent, indexInParent) pair plus tag+id; two Reds with identical
	// identifying tuple alias to the same index.
	//
	// For the root, short-circuit.
	if r.parent < 0 {
		return 0
	}
	key := redChildKey{parent: r.parent, child: r.indexInParent}
	if idx, ok := t.childCache[key]; ok {
		return idx
	}
	idx := len(t.reds)
	t.reds = append(t.reds, redEntry{red: r})
	t.childCache[key] = idx
	return idx
}

// Kind returns the green kind of the node or token this Red handle wraps.
func (r Red) Kind() GreenKind {
	switch r.tag {
	case GctNode:
		return r.tree.Arena.Nodes[r.id].Kind
	case GctToken:
		return r.tree.Arena.Tokens[r.id].Kind
	}
	return GkNone
}

// IsToken reports whether this handle wraps a token leaf.
func (r Red) IsToken() bool { return r.tag == GctToken }

// IsNode reports whether this handle wraps an interior node.
func (r Red) IsNode() bool { return r.tag == GctNode }

// GreenID returns the underlying green arena id this handle wraps. Useful
// when callers need to pull fields from the GreenToken / GreenNode directly
// (e.g. a token's LeadingTrivia slice). Combine with IsToken/IsNode to pick
// the right accessor.
func (r Red) GreenID() int { return r.id }

// Token returns the underlying GreenToken. Panics if r is not a token leaf.
func (r Red) Token() GreenToken {
	if r.tag != GctToken {
		panic("cst.Red.Token: handle is not a token leaf")
	}
	return r.tree.Arena.Tokens[r.id]
}

// Node returns the underlying GreenNode. Panics if r is not an interior node.
func (r Red) Node() GreenNode {
	if r.tag != GctNode {
		panic("cst.Red.Node: handle is not an interior node")
	}
	return r.tree.Arena.Nodes[r.id]
}

// Offset returns the absolute byte offset where this node or token begins
// in the source, after CRLF normalization (i.e. the same coordinate system
// used throughout internal/cst).
func (r Red) Offset() int { return r.absoluteOffset }

// Width returns the full byte extent of this node or token in source,
// INCLUDING any attached trivia on token leaves. For nodes this is the sum
// of child widths.
func (r Red) Width() int {
	switch r.tag {
	case GctNode:
		return r.tree.Arena.Nodes[r.id].Width
	case GctToken:
		return r.tree.Arena.Tokens[r.id].TotalWidth()
	}
	return 0
}

// TextWidth returns the byte width of just the token's lexeme (excluding
// trivia). Nodes have no distinct text width; this returns the full Width.
// Use TextWidth when you need the lexeme length without leading/trailing
// trivia, e.g. highlighting just the identifier vs the surrounding trivia.
func (r Red) TextWidth() int {
	switch r.tag {
	case GctToken:
		return r.tree.Arena.Tokens[r.id].Width
	}
	return r.Width()
}

// End returns the absolute offset just past this node or token.
func (r Red) End() int { return r.absoluteOffset + r.Width() }

// TextRange returns the [start, end) offsets in one value for convenience.
func (r Red) TextRange() (int, int) { return r.Offset(), r.End() }

// ChildCount returns the number of direct children. Token leaves have 0.
func (r Red) ChildCount() int {
	if r.tag != GctNode {
		return 0
	}
	return len(r.tree.Arena.Nodes[r.id].Children)
}

// ChildAt returns the i-th child as a Red handle. The child's absolute
// offset is computed from the parent offset plus the sum of preceding
// sibling widths. Child handles are cached by (parent, child) so repeat
// lookups are constant time.
//
// Panics on out-of-range; callers should guard with ChildCount.
func (r Red) ChildAt(i int) Red {
	if r.tag != GctNode {
		panic("cst.Red.ChildAt: leaf has no children")
	}
	node := r.tree.Arena.Nodes[r.id]
	if i < 0 || i >= len(node.Children) {
		panic("cst.Red.ChildAt: index out of range")
	}

	parentIdx := r.tree.redIndex(r)
	key := redChildKey{parent: parentIdx, child: i}
	if idx, ok := r.tree.childCache[key]; ok {
		return r.tree.reds[idx].red
	}

	// Compute the child's absolute offset by summing preceding sibling
	// widths. Linear in child count; in practice most nodes have few
	// children, and even for long lists the sum is cheap.
	//
	// Token widths include attached trivia (via TotalWidth) so sibling
	// offsets reflect the full source extent.
	childOffset := r.absoluteOffset
	for j := 0; j < i; j++ {
		c := node.Children[j]
		switch c.Tag {
		case GctNode:
			childOffset += r.tree.Arena.Nodes[c.ID].Width
		case GctToken:
			childOffset += r.tree.Arena.Tokens[c.ID].TotalWidth()
		}
	}

	c := node.Children[i]
	child := Red{
		tree:           r.tree,
		parent:         parentIdx,
		indexInParent:  i,
		absoluteOffset: childOffset,
		tag:            c.Tag,
		id:             c.ID,
	}
	// Store in cache.
	idx := len(r.tree.reds)
	r.tree.reds = append(r.tree.reds, redEntry{red: child})
	r.tree.childCache[key] = idx
	return child
}

// Parent returns the Red handle of the parent node, or the zero-value Red
// plus false if r is the root.
func (r Red) Parent() (Red, bool) {
	if r.parent < 0 {
		return Red{}, false
	}
	return r.tree.reds[r.parent].red, true
}

// Pos converts Offset to a token.Pos using the tree's associated source. The
// source is provided explicitly because a Green arena has no back-pointer to
// its source bytes — multiple parses of different sources may share Green
// structure via interning.
func (r Red) Pos(li *LineIndex) token.Pos { return li.Locate(r.Offset()) }

// EndPos converts End to a token.Pos using the tree's associated source.
func (r Red) EndPos(li *LineIndex) token.Pos { return li.Locate(r.End()) }

// FirstToken returns the first leaf token reachable from r, or the zero-value
// Red plus false if the subtree contains no tokens (pure-error subtrees can
// have zero-width ErrorMissing leaves but no real tokens).
func (r Red) FirstToken() (Red, bool) {
	if r.IsToken() {
		return r, true
	}
	for i := 0; i < r.ChildCount(); i++ {
		if tok, ok := r.ChildAt(i).FirstToken(); ok {
			return tok, true
		}
	}
	return Red{}, false
}

// LastToken is the symmetric right-edge helper.
func (r Red) LastToken() (Red, bool) {
	if r.IsToken() {
		return r, true
	}
	for i := r.ChildCount() - 1; i >= 0; i-- {
		if tok, ok := r.ChildAt(i).LastToken(); ok {
			return tok, true
		}
	}
	return Red{}, false
}

// FindCoveringNode walks from r toward leaves, returning the smallest node
// whose [offset, end) contains the target byte offset. Returns r itself if
// the target lies outside r's range. Useful for LSP requests (hover,
// definition) that carry a byte offset.
func (r Red) FindCoveringNode(offset int) Red {
	if offset < r.Offset() || offset >= r.End() {
		return r
	}
	for i := 0; i < r.ChildCount(); i++ {
		child := r.ChildAt(i)
		if offset >= child.Offset() && offset < child.End() {
			return child.FindCoveringNode(offset)
		}
	}
	return r
}

// Walk visits r then its descendants in pre-order. The visit function may
// return false to stop descending into the current subtree; returning true
// continues. Returning false from the root effectively aborts the walk.
func (r Red) Walk(visit func(Red) bool) {
	if !visit(r) {
		return
	}
	for i := 0; i < r.ChildCount(); i++ {
		r.ChildAt(i).Walk(visit)
	}
}

// LineIndex is the public lineIndex re-export for callers who need to map
// byte offsets to (line, column) without re-walking source.
type LineIndex = lineIndex

// NewLineIndex builds a line index for src. See lineIndex.locate for the
// query semantics.
func NewLineIndex(src []byte) *LineIndex { return newLineIndex(src) }

// Locate is the exported name for lineIndex.locate.
func (li *lineIndex) Locate(offset int) token.Pos { return li.locate(offset) }
