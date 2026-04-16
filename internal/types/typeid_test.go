package types

import (
	"testing"

	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
)

func mkNamed(name string, args ...Type) *Named {
	return &Named{
		Sym: &resolve.Symbol{
			Name: name,
			Kind: resolve.SymStruct,
			Pos:  token.Pos{Offset: 1},
			Pub:  true,
		},
		Args: args,
	}
}

func TestTypeIDPrimitiveEquality(t *testing.T) {
	a := &Primitive{Kind: PInt}
	b := &Primitive{Kind: PInt}
	if ID(a) != ID(b) {
		t.Fatal("same-kind Primitive must hash equal")
	}
	c := &Primitive{Kind: PString}
	if ID(a) == ID(c) {
		t.Fatal("different-kind Primitive must hash distinctly")
	}
}

func TestTypeIDTupleStructural(t *testing.T) {
	a := &Tuple{Elems: []Type{&Primitive{Kind: PInt}, &Primitive{Kind: PString}}}
	b := &Tuple{Elems: []Type{&Primitive{Kind: PInt}, &Primitive{Kind: PString}}}
	c := &Tuple{Elems: []Type{&Primitive{Kind: PString}, &Primitive{Kind: PInt}}}
	if ID(a) != ID(b) {
		t.Fatal("same-shape Tuple must hash equal")
	}
	if ID(a) == ID(c) {
		t.Fatal("element-order matters for Tuple hash")
	}
}

func TestTypeIDOptional(t *testing.T) {
	a := &Optional{Inner: &Primitive{Kind: PInt}}
	b := &Optional{Inner: &Primitive{Kind: PInt}}
	c := &Optional{Inner: &Primitive{Kind: PString}}
	if ID(a) != ID(b) {
		t.Fatal("same Inner Optional must hash equal")
	}
	if ID(a) == ID(c) {
		t.Fatal("different Inner must hash distinctly")
	}
}

func TestTypeIDFnType(t *testing.T) {
	a := &FnType{
		Params: []Type{&Primitive{Kind: PInt}},
		Return: &Primitive{Kind: PString},
	}
	b := &FnType{
		Params: []Type{&Primitive{Kind: PInt}},
		Return: &Primitive{Kind: PString},
	}
	if ID(a) != ID(b) {
		t.Fatal("structurally equal FnType must hash equal")
	}
	// Swap params / return — must differ.
	c := &FnType{
		Params: []Type{&Primitive{Kind: PString}},
		Return: &Primitive{Kind: PInt},
	}
	if ID(a) == ID(c) {
		t.Fatal("swapped param/return must hash distinctly")
	}
}

func TestTypeIDNamedByDeclarationIdentity(t *testing.T) {
	// Named types compare by declaring Symbol's ID.
	a := mkNamed("List", &Primitive{Kind: PInt})
	b := mkNamed("List", &Primitive{Kind: PInt})
	if ID(a) != ID(b) {
		t.Fatalf("Named with equivalent Sym+Args must hash equal; got %x vs %x", ID(a), ID(b))
	}
	// Different type argument.
	c := mkNamed("List", &Primitive{Kind: PString})
	if ID(a) == ID(c) {
		t.Fatal("Named with different Args must hash distinctly")
	}
	// Different name.
	d := mkNamed("Set", &Primitive{Kind: PInt})
	if ID(a) == ID(d) {
		t.Fatal("Named with different Sym must hash distinctly")
	}
}

func TestTypeIDErrorSingleton(t *testing.T) {
	a := &Error{}
	b := &Error{}
	if ID(a) != ID(b) {
		t.Fatal("Error singleton must hash consistently")
	}
}

func TestTypeIDNilHashesStably(t *testing.T) {
	var a Type
	var b Type
	if ID(a) != ID(b) {
		t.Fatal("nil Type must hash stably")
	}
	// Nil should differ from Error.
	if ID(a) == ID(&Error{}) {
		t.Fatal("nil must differ from Error")
	}
}

func TestTypeIDKindTagsDontCollide(t *testing.T) {
	// A single-element tuple and its element shouldn't collide by
	// accident — tag bytes disambiguate kinds.
	a := &Primitive{Kind: PInt}
	b := &Tuple{Elems: []Type{&Primitive{Kind: PInt}}}
	if ID(a) == ID(b) {
		t.Fatal("Primitive and single-element Tuple must not collide")
	}
}
