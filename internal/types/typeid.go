package types

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"

	"github.com/osty/osty/internal/resolve"
)

// TypeID is a content-addressable identity for a [Type]. Structurally
// equal types (two `List<Int>` values built from the same prelude
// symbols) share an ID; structurally distinct types differ.
//
// TypeID is intended for cross-run comparison: an incremental query
// cache can compare two check results by walking their type maps and
// comparing IDs, without keeping the original pointers alive.
type TypeID [32]byte

// ID returns the type's content-hash identity. Computed on every
// call — types are internally pointer-shared in many cases, so
// caching inside the type value would bloat every allocation. Callers
// that hash many types in a loop should collect IDs into a slice
// once and reuse them.
func ID(t Type) TypeID {
	h := sha256.New()
	writeType(h, t)
	var out TypeID
	h.Sum(out[:0])
	return out
}

// writeType serializes a type's structure into h. The tag bytes
// (1..9) distinguish the kinds so different kinds with coincidentally
// identical field writes can't collide.
func writeType(h hashWriter, t Type) {
	if t == nil {
		h.Write([]byte{0})
		return
	}
	switch tt := t.(type) {
	case *Primitive:
		h.Write([]byte{1, byte(tt.Kind)})
	case *Untyped:
		h.Write([]byte{2, byte(tt.Kind)})
	case *Tuple:
		h.Write([]byte{3})
		writeU32(h, uint32(len(tt.Elems)))
		for _, e := range tt.Elems {
			writeType(h, e)
		}
	case *Optional:
		h.Write([]byte{4})
		writeType(h, tt.Inner)
	case *FnType:
		h.Write([]byte{5})
		writeU32(h, uint32(len(tt.Params)))
		for _, p := range tt.Params {
			writeType(h, p)
		}
		writeType(h, tt.Return)
	case *Named:
		h.Write([]byte{6})
		writeSymbolID(h, tt.Sym)
		writeU32(h, uint32(len(tt.Args)))
		for _, a := range tt.Args {
			writeType(h, a)
		}
	case *TypeVar:
		h.Write([]byte{7})
		writeSymbolID(h, tt.Sym)
		writeU32(h, uint32(len(tt.Bounds)))
		for _, b := range tt.Bounds {
			writeType(h, b)
		}
	case *Builder:
		h.Write([]byte{8})
		// Struct is a *Named; delegating captures its symbol + args.
		writeType(h, tt.Struct)
		names := make([]string, 0, len(tt.Set))
		for n := range tt.Set {
			names = append(names, n)
		}
		sort.Strings(names)
		writeU32(h, uint32(len(names)))
		for _, n := range names {
			writeLenStr(h, n)
		}
		if tt.Preloaded {
			h.Write([]byte{1})
		} else {
			h.Write([]byte{0})
		}
	case *Error:
		h.Write([]byte{9})
	default:
		// Unknown type — write a sentinel tag so at least the ID is
		// stable for this value, even if structurally opaque.
		h.Write([]byte{255})
	}
}

func writeSymbolID(h hashWriter, s *resolve.Symbol) {
	id := s.ID()
	h.Write(id[:])
}

func writeU32(h hashWriter, v uint32) {
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], v)
	_, _ = h.Write(buf[:])
}

func writeLenStr(h hashWriter, s string) {
	writeU32(h, uint32(len(s)))
	_, _ = h.Write([]byte(s))
}

type hashWriter interface{ Write([]byte) (int, error) }
