package resolve

import (
	"crypto/sha256"
	"encoding/binary"
)

// SymbolID is a content-addressable identity for a [Symbol]. Two
// Symbol pointers allocated by different resolver runs over the same
// source compare equal by ID even though their pointers differ.
//
// Callers that build cross-run indexes (incremental query caches,
// reverse reference tables, on-disk symbol databases) key by ID
// instead of by *Symbol pointer. For in-run lookup during a single
// analysis, the existing pointer-keyed maps (`Result.Refs`,
// `check.Result.SymTypes`, etc.) remain the fast path.
type SymbolID [32]byte

// ID returns the symbol's content-hash identity. Computed lazily on
// first call and cached; subsequent calls are O(1). Safe to call from
// multiple goroutines.
//
// The ID covers the fields that determine semantic identity:
//
//   - Name and Kind — a struct named Foo and an fn named Foo have
//     distinct IDs.
//   - Declaration position (byte offset) — two symbols named Foo
//     declared at different offsets are distinct.
//   - Visibility (Pub) — promoting a private to pub changes the ID.
//   - Owning package directory — the same name defined in two
//     packages has distinct IDs.
//
// The Decl's concrete AST type is NOT hashed separately: Kind is
// 1-to-1 with the Decl type for non-builtin symbols.
func (s *Symbol) ID() SymbolID {
	if s == nil {
		return SymbolID{}
	}
	s.idOnce.Do(func() { s.idCache = computeSymbolID(s) })
	return s.idCache
}

func computeSymbolID(s *Symbol) SymbolID {
	h := sha256.New()
	writeLenBytes(h, []byte(s.Name))
	h.Write([]byte{byte(s.Kind)})
	writeU64(h, uint64(s.Pos.Offset))
	if s.Pub {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
	if s.Package != nil {
		writeLenBytes(h, []byte(s.Package.Dir))
	} else {
		writeLenBytes(h, nil)
	}
	var out SymbolID
	h.Sum(out[:0])
	return out
}

// writeLenBytes writes a length-prefixed byte slice so concatenated
// fields can't collide ("ab" + "c" vs "a" + "bc").
func writeLenBytes(h interface{ Write([]byte) (int, error) }, b []byte) {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], uint32(len(b)))
	_, _ = h.Write(buf[:])
	if len(b) > 0 {
		_, _ = h.Write(b)
	}
}

func writeU64(h interface{ Write([]byte) (int, error) }, v uint64) {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], v)
	_, _ = h.Write(buf[:])
}
