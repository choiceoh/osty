package cst

// GreenArena owns every GreenNode, GreenToken, and Trivia produced by a
// single parse. The arena is the Go analogue of the Osty structure planned
// for toolchain/cst_green.osty — same shape, same semantics, just using Go
// slices and maps.
//
// Structural sharing: identical subtrees (same kind + same children in the
// same order) are stored once. GreenDedup implements the content-based
// interning that makes this happen.
type GreenArena struct {
	Nodes   []GreenNode
	Tokens  []GreenToken
	Trivias []Trivia

	// dedup handles structural sharing for both nodes and tokens. Access
	// through Arena methods rather than the field directly.
	dedup greenDedup
}

// NewArena returns a fresh, empty arena. Most callers go through a
// GreenBuilder instead of touching the arena directly.
func NewArena() *GreenArena {
	return &GreenArena{
		dedup: newGreenDedup(),
	}
}

// AddTrivia appends a Trivia to the arena and returns its index. Trivias are
// NOT deduplicated today — Trivia records carry source positions that make
// each one unique, so deduping would almost always miss. If memory pressure
// demands it later, switch the key to `(kind, width)` and rebuild absolute
// positions in the Red layer.
func (a *GreenArena) AddTrivia(t Trivia) int {
	id := len(a.Trivias)
	a.Trivias = append(a.Trivias, t)
	return id
}

// AddToken appends a GreenToken to the arena, deduplicating identical entries.
// Returns the token id.
func (a *GreenArena) AddToken(t GreenToken) int {
	if id, ok := a.dedup.lookupToken(a, t); ok {
		return id
	}
	id := len(a.Tokens)
	a.Tokens = append(a.Tokens, t)
	a.dedup.recordToken(t, id)
	return id
}

// AddNode appends a GreenNode to the arena, deduplicating identical entries.
// Returns the node id.
func (a *GreenArena) AddNode(n GreenNode) int {
	if id, ok := a.dedup.lookupNode(a, n); ok {
		return id
	}
	id := len(a.Nodes)
	a.Nodes = append(a.Nodes, n)
	a.dedup.recordNode(n, id)
	return id
}

// TokenAt returns the token at id. Panics on out-of-range — the parser
// controls all ids so a bad id is a programmer error, not a user input issue.
func (a *GreenArena) TokenAt(id int) GreenToken {
	return a.Tokens[id]
}

// NodeAt returns the node at id.
func (a *GreenArena) NodeAt(id int) GreenNode {
	return a.Nodes[id]
}

// TriviaAt returns the trivia at id.
func (a *GreenArena) TriviaAt(id int) Trivia {
	return a.Trivias[id]
}

// DedupMisses reports how many times AddToken / AddNode had to allocate a
// fresh entry because no existing id was equivalent. Useful for measuring
// the sharing ratio under fuzz / real input. The companion DedupHits gives
// reused-id counts.
func (a *GreenArena) DedupMisses() int { return a.dedup.misses }

// DedupHits reports how many reused-id lookups succeeded.
func (a *GreenArena) DedupHits() int { return a.dedup.hits }

// GreenBuilder constructs a Green tree from a stream of startNode / token /
// finishNode events. The builder matches the rust-analyzer rowan builder
// shape: parse code emits structural events; the builder resolves them to
// arena ids.
//
// The builder is NOT thread-safe; one parse uses one builder.
type GreenBuilder struct {
	arena *GreenArena
	stack []builderFrame
	root  int // node id of the final file root, -1 until Finish
}

type builderFrame struct {
	kind     GreenKind
	children []GreenChild
	width    int
}

// NewBuilder returns an empty builder writing into arena. If arena is nil, a
// fresh arena is allocated and can be retrieved via (*GreenBuilder).Arena.
func NewBuilder(arena *GreenArena) *GreenBuilder {
	if arena == nil {
		arena = NewArena()
	}
	return &GreenBuilder{arena: arena, root: -1}
}

// Arena exposes the arena the builder writes into. Consumers typically need
// this after Finish() to traverse the tree.
func (b *GreenBuilder) Arena() *GreenArena { return b.arena }

// StartNode opens a new interior node of the given kind. Every StartNode
// must be matched by FinishNode; children added in between become this
// node's children.
func (b *GreenBuilder) StartNode(kind GreenKind) {
	b.stack = append(b.stack, builderFrame{kind: kind})
}

// Token emits a terminal leaf. Leading/trailing slice are trivia indices
// into the arena; the builder sums their widths into LeadingWidth and
// TrailingWidth so parent widths correctly reflect full source extent.
func (b *GreenBuilder) Token(kind GreenKind, tokenKind int, text string, width int, leading, trailing []int) {
	tok := GreenToken{
		Kind:           kind,
		TokenKind:      tokenKind,
		Text:           text,
		Width:          width,
		LeadingWidth:   sumTriviaWidths(b.arena, leading),
		TrailingWidth:  sumTriviaWidths(b.arena, trailing),
		LeadingTrivia:  leading,
		TrailingTrivia: trailing,
	}
	id := b.arena.AddToken(tok)
	if len(b.stack) == 0 {
		panic("cst: GreenBuilder.Token called outside any StartNode frame")
	}
	top := &b.stack[len(b.stack)-1]
	top.children = append(top.children, GreenChild{Tag: GctToken, ID: id})
	top.width += tok.TotalWidth()
}

func sumTriviaWidths(a *GreenArena, indices []int) int {
	total := 0
	for _, idx := range indices {
		if idx >= 0 && idx < len(a.Trivias) {
			total += a.Trivias[idx].Length
		}
	}
	return total
}

// FinishNode closes the current open node, appending it to its parent's
// children (or recording it as the root when the stack drains).
func (b *GreenBuilder) FinishNode() {
	if len(b.stack) == 0 {
		panic("cst: GreenBuilder.FinishNode called with empty stack")
	}
	top := b.stack[len(b.stack)-1]
	b.stack = b.stack[:len(b.stack)-1]
	node := GreenNode{
		Kind:     top.kind,
		Width:    top.width,
		Children: top.children,
	}
	id := b.arena.AddNode(node)
	if len(b.stack) == 0 {
		b.root = id
		return
	}
	parent := &b.stack[len(b.stack)-1]
	parent.children = append(parent.children, GreenChild{Tag: GctNode, ID: id})
	parent.width += node.Width
}

// Finish finalizes the builder and returns the root node id plus the arena.
// Callers must have exactly one outstanding StartNode at the start (the file
// node) which this method implicitly closes via FinishNode check.
func (b *GreenBuilder) Finish() (arena *GreenArena, rootID int) {
	if len(b.stack) != 0 {
		panic("cst: GreenBuilder.Finish called with unterminated StartNode frames")
	}
	return b.arena, b.root
}
