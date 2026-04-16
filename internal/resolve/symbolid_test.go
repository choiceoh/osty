package resolve

import (
	"testing"

	"github.com/osty/osty/internal/token"
)

// mkSym is a tiny helper for building Symbol values in this file's
// ID tests. We deliberately don't use the resolver's own construction
// paths — the point is to exercise ID() over known fields.
func mkSym(name string, kind SymbolKind, offset int, pub bool, pkgDir string) *Symbol {
	s := &Symbol{
		Name: name,
		Kind: kind,
		Pos:  token.Pos{Offset: offset, Line: 1, Column: 1},
		Pub:  pub,
	}
	if pkgDir != "" {
		s.Package = &Package{Dir: pkgDir}
	}
	return s
}

func TestSymbolIDStableAcrossInstances(t *testing.T) {
	a := mkSym("Foo", SymStruct, 42, true, "/pkg/a")
	b := mkSym("Foo", SymStruct, 42, true, "/pkg/a")
	if a.ID() != b.ID() {
		t.Fatalf("same fields must produce same ID; got %x vs %x", a.ID(), b.ID())
	}
}

func TestSymbolIDDistinguishesName(t *testing.T) {
	a := mkSym("Foo", SymStruct, 42, true, "/pkg")
	b := mkSym("Bar", SymStruct, 42, true, "/pkg")
	if a.ID() == b.ID() {
		t.Fatal("different Name must produce different ID")
	}
}

func TestSymbolIDDistinguishesKind(t *testing.T) {
	a := mkSym("Foo", SymStruct, 42, true, "/pkg")
	b := mkSym("Foo", SymFn, 42, true, "/pkg")
	if a.ID() == b.ID() {
		t.Fatal("different Kind must produce different ID")
	}
}

func TestSymbolIDDistinguishesPosition(t *testing.T) {
	a := mkSym("Foo", SymStruct, 42, true, "/pkg")
	b := mkSym("Foo", SymStruct, 99, true, "/pkg")
	if a.ID() == b.ID() {
		t.Fatal("different Pos.Offset must produce different ID")
	}
}

func TestSymbolIDDistinguishesPub(t *testing.T) {
	a := mkSym("Foo", SymStruct, 42, true, "/pkg")
	b := mkSym("Foo", SymStruct, 42, false, "/pkg")
	if a.ID() == b.ID() {
		t.Fatal("different Pub must produce different ID")
	}
}

func TestSymbolIDDistinguishesPackage(t *testing.T) {
	a := mkSym("Foo", SymStruct, 42, true, "/pkg/a")
	b := mkSym("Foo", SymStruct, 42, true, "/pkg/b")
	if a.ID() == b.ID() {
		t.Fatal("different Package.Dir must produce different ID")
	}
}

func TestSymbolIDHandlesNilPackage(t *testing.T) {
	a := mkSym("Foo", SymBuiltin, 0, true, "")
	b := mkSym("Foo", SymBuiltin, 0, true, "")
	if a.ID() != b.ID() {
		t.Fatal("builtins with no package must still produce consistent ID")
	}
	// And differ from same-name symbol with a package.
	c := mkSym("Foo", SymBuiltin, 0, true, "/pkg")
	if a.ID() == c.ID() {
		t.Fatal("nil Package must produce different ID than non-nil Package")
	}
}

func TestSymbolIDNilSafe(t *testing.T) {
	var s *Symbol
	if got := s.ID(); got != (SymbolID{}) {
		t.Fatalf("nil Symbol.ID() should be zero value, got %x", got)
	}
}

func TestSymbolIDCachesResult(t *testing.T) {
	s := mkSym("Foo", SymStruct, 42, true, "/pkg")
	first := s.ID()
	second := s.ID()
	if first != second {
		t.Fatal("repeat ID() calls must return identical cached value")
	}
	// Mutating fields after first ID() should NOT change cached ID —
	// the contract is: ID reflects the symbol at the moment of first
	// observation. Callers who want to re-hash a mutated symbol need
	// to construct a fresh Symbol.
	s.Name = "Bar"
	third := s.ID()
	if third != first {
		t.Fatal("cached ID must not change after mutation; construct a new Symbol to re-hash")
	}
}
