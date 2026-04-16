package cst

// greenDedup implements content-based structural sharing for GreenNode and
// GreenToken. The design mirrors what toolchain/cst_dedup.osty plans to do
// once the self-host generator can ingest it: keep a hash-to-candidate-list
// map, fall back to field-wise equality on collision.
//
// Why we don't just use a Go map keyed on the struct: GreenNode has a slice
// field (Children) which isn't hashable, and GreenToken has two int slices
// plus a string. Composing a manual fingerprint keeps the dedup cheap
// without depending on reflection.
type greenDedup struct {
	nodeByHash  map[uint64][]int
	tokenByHash map[uint64][]int

	hits   int
	misses int
}

func newGreenDedup() greenDedup {
	return greenDedup{
		nodeByHash:  make(map[uint64][]int),
		tokenByHash: make(map[uint64][]int),
	}
}

// lookupNode searches for an existing node equivalent to n. Returns the
// existing id on hit.
func (d *greenDedup) lookupNode(a *GreenArena, n GreenNode) (int, bool) {
	h := hashNode(n)
	for _, id := range d.nodeByHash[h] {
		if nodesEqual(a.Nodes[id], n) {
			d.hits++
			return id, true
		}
	}
	return 0, false
}

// recordNode registers id under n's hash for future lookups.
func (d *greenDedup) recordNode(n GreenNode, id int) {
	h := hashNode(n)
	d.nodeByHash[h] = append(d.nodeByHash[h], id)
	d.misses++
}

// lookupToken searches for an existing token equivalent to t.
func (d *greenDedup) lookupToken(a *GreenArena, t GreenToken) (int, bool) {
	h := hashToken(t)
	for _, id := range d.tokenByHash[h] {
		if tokensEqual(a.Tokens[id], t) {
			d.hits++
			return id, true
		}
	}
	return 0, false
}

// recordToken registers id under t's hash.
func (d *greenDedup) recordToken(t GreenToken, id int) {
	h := hashToken(t)
	d.tokenByHash[h] = append(d.tokenByHash[h], id)
	d.misses++
}

// -------- Hashing --------
//
// Both hashes use FNV-1a because it's fast, has no external dependency, and
// has adequate distribution for our integer / short-string keys. Collisions
// are handled by full-field comparison, so a perfect hash is unnecessary.

const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

func mixU64(h, v uint64) uint64 {
	h ^= v
	h *= fnvPrime
	return h
}

func mixInt(h uint64, v int) uint64 { return mixU64(h, uint64(v)) }
func mixStr(h uint64, s string) uint64 {
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= fnvPrime
	}
	return h
}

func hashToken(t GreenToken) uint64 {
	h := uint64(fnvOffset)
	h = mixInt(h, int(t.Kind))
	h = mixInt(h, t.TokenKind)
	h = mixInt(h, t.Width)
	h = mixStr(h, t.Text)
	for _, id := range t.LeadingTrivia {
		h = mixInt(h, id)
	}
	h = mixU64(h, sepLeadingTrailing)
	for _, id := range t.TrailingTrivia {
		h = mixInt(h, id)
	}
	return h
}

// Separator constants are mixed into the hash between field groups so trivial
// aliasings like ([1], [2]) vs ([1,2], []) don't collide. Values are chosen
// for high bit-distribution; the exact bits are not load-bearing.
const (
	sepLeadingTrailing uint64 = 0x5EAD_10C_5EAD_10CA
	sepChildren        uint64 = 0xC417_D11D_C417_D11D
)

// hashNode fingerprints a node by its kind + width + child tag+id sequence.
// Two nodes with identical children in the same order always hash the same.
func hashNode(n GreenNode) uint64 {
	h := uint64(fnvOffset)
	h = mixInt(h, int(n.Kind))
	h = mixInt(h, n.Width)
	h = mixU64(h, sepChildren)
	for _, c := range n.Children {
		h = mixInt(h, int(c.Tag))
		h = mixInt(h, c.ID)
	}
	return h
}

// -------- Equality --------

func tokensEqual(a, b GreenToken) bool {
	if a.Kind != b.Kind || a.TokenKind != b.TokenKind || a.Width != b.Width || a.Text != b.Text {
		return false
	}
	if len(a.LeadingTrivia) != len(b.LeadingTrivia) || len(a.TrailingTrivia) != len(b.TrailingTrivia) {
		return false
	}
	for i := range a.LeadingTrivia {
		if a.LeadingTrivia[i] != b.LeadingTrivia[i] {
			return false
		}
	}
	for i := range a.TrailingTrivia {
		if a.TrailingTrivia[i] != b.TrailingTrivia[i] {
			return false
		}
	}
	return true
}

func nodesEqual(a, b GreenNode) bool {
	if a.Kind != b.Kind || a.Width != b.Width || len(a.Children) != len(b.Children) {
		return false
	}
	for i := range a.Children {
		if a.Children[i] != b.Children[i] {
			return false
		}
	}
	return true
}
