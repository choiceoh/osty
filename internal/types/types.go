// Package types defines the semantic types used by the Osty type checker.
//
// The AST describes syntax; this package describes the semantics the checker
// assigns to that syntax. An expression's type comes from this package; a
// declaration's signature (fields, methods, variants, parameters) is built
// here and consulted during checking.
//
// Type identity
//
//   - Primitive types are compared by Kind.
//   - Named types (struct/enum/interface/alias) are compared by the
//     declaring *resolve.Symbol pointer plus their type arguments
//     element-wise. Two `List<Int>` values built from the same `List`
//     builtin and the same `Int` primitive are equal.
//   - TypeVar (generic) is compared by its declaring symbol pointer. The
//     same `T` bound by the same GenericParam compares equal.
//   - Tuple, FnType, Optional compare structurally.
//   - The sentinel Error is equal to every type (it "taints" cascades so
//     follow-on errors are suppressed).
//
// Type-checking passes read and write to this package only; they do not
// mutate the AST.
package types

import (
	"strings"

	"github.com/osty/osty/internal/resolve"
)

// Type is the semantic type of an expression, binding, field, or
// parameter. Callers test concrete kinds via a type switch.
type Type interface {
	String() string
	typeNode()
}

// ==== Primitive ====

// PrimitiveKind enumerates the built-in scalar types of v0.4 §2.1 plus
// the synthetic Unit and Never tags.
type PrimitiveKind int

const (
	PInvalid PrimitiveKind = iota

	// Numeric.
	PInt
	PInt8
	PInt16
	PInt32
	PInt64
	PUInt8
	PUInt16
	PUInt32
	PUInt64
	PByte // = UInt8 alias
	PFloat
	PFloat32
	PFloat64

	// Other scalars.
	PBool
	PChar
	PString
	PBytes

	// Bottom and unit (not really "primitives" in the spec but modelled
	// the same way for uniform handling).
	PNever
	PUnit
)

func (k PrimitiveKind) IsSignedInt() bool {
	switch k {
	case PInt, PInt8, PInt16, PInt32, PInt64:
		return true
	}
	return false
}

func (k PrimitiveKind) IsUnsignedInt() bool {
	switch k {
	case PUInt8, PUInt16, PUInt32, PUInt64, PByte:
		return true
	}
	return false
}

func (k PrimitiveKind) IsInteger() bool {
	return k.IsSignedInt() || k.IsUnsignedInt()
}

func (k PrimitiveKind) IsFloat() bool {
	switch k {
	case PFloat, PFloat32, PFloat64:
		return true
	}
	return false
}

func (k PrimitiveKind) IsNumeric() bool { return k.IsInteger() || k.IsFloat() }

// IsOrdered reports whether the primitive has a built-in total order
// (v0.4 §2.6.5).
func (k PrimitiveKind) IsOrdered() bool {
	switch k {
	case PBool, PChar, PString, PBytes:
		return true
	}
	return k.IsNumeric()
}

// IsEqual reports whether the primitive supports `==` directly.
func (k PrimitiveKind) IsEqual() bool {
	switch k {
	case PBool, PChar, PString, PBytes, PUnit:
		return true
	}
	return k.IsNumeric()
}

// Primitive is a scalar type (Int, Bool, String, ...) or Unit / Never.
type Primitive struct {
	Kind PrimitiveKind
}

func (*Primitive) typeNode() {}

func (p *Primitive) String() string {
	switch p.Kind {
	case PInt:
		return "Int"
	case PInt8:
		return "Int8"
	case PInt16:
		return "Int16"
	case PInt32:
		return "Int32"
	case PInt64:
		return "Int64"
	case PUInt8:
		return "UInt8"
	case PUInt16:
		return "UInt16"
	case PUInt32:
		return "UInt32"
	case PUInt64:
		return "UInt64"
	case PByte:
		return "Byte"
	case PFloat:
		return "Float"
	case PFloat32:
		return "Float32"
	case PFloat64:
		return "Float64"
	case PBool:
		return "Bool"
	case PChar:
		return "Char"
	case PString:
		return "String"
	case PBytes:
		return "Bytes"
	case PNever:
		return "Never"
	case PUnit:
		return "()"
	}
	return "?"
}

// Singletons for convenience; the checker uses these to avoid allocating
// a fresh Primitive every time.
var (
	Int     = &Primitive{Kind: PInt}
	Int8    = &Primitive{Kind: PInt8}
	Int16   = &Primitive{Kind: PInt16}
	Int32   = &Primitive{Kind: PInt32}
	Int64   = &Primitive{Kind: PInt64}
	UInt8   = &Primitive{Kind: PUInt8}
	UInt16  = &Primitive{Kind: PUInt16}
	UInt32  = &Primitive{Kind: PUInt32}
	UInt64  = &Primitive{Kind: PUInt64}
	Byte    = &Primitive{Kind: PByte}
	Float   = &Primitive{Kind: PFloat}
	Float32 = &Primitive{Kind: PFloat32}
	Float64 = &Primitive{Kind: PFloat64}
	Bool    = &Primitive{Kind: PBool}
	Char    = &Primitive{Kind: PChar}
	String  = &Primitive{Kind: PString}
	Bytes   = &Primitive{Kind: PBytes}
	Never   = &Primitive{Kind: PNever}
	Unit    = &Primitive{Kind: PUnit}
)

// PrimitiveByName resolves a scalar type name ("Int", "Float", "Bool",
// …) to the corresponding Primitive singleton. It is the canonical
// inverse of Primitive.String for every scalar exposed via the prelude
// and is consulted whenever a pass outside the main type-check flow
// needs to interpret a bare type name without a resolver in hand.
//
// Returns nil when the name does not match a scalar kind.
func PrimitiveByName(name string) *Primitive {
	if p, ok := primitiveByName[name]; ok {
		return p
	}
	return nil
}

// PrimitiveByKind returns the Primitive singleton for the given kind,
// or nil when the kind is not a scalar (e.g. PInvalid, PNever, PUnit).
// Paired with PrimitiveByName so callers working with either kinds or
// names reach the same canonical singleton.
func PrimitiveByKind(k PrimitiveKind) *Primitive {
	return primitiveByKind[k]
}

var primitiveByKind = map[PrimitiveKind]*Primitive{
	PInt:     Int,
	PInt8:    Int8,
	PInt16:   Int16,
	PInt32:   Int32,
	PInt64:   Int64,
	PUInt8:   UInt8,
	PUInt16:  UInt16,
	PUInt32:  UInt32,
	PUInt64:  UInt64,
	PByte:    Byte,
	PFloat:   Float,
	PFloat32: Float32,
	PFloat64: Float64,
	PBool:    Bool,
	PChar:    Char,
	PString:  String,
	PBytes:   Bytes,
}

var primitiveByName = map[string]*Primitive{
	"Int":     Int,
	"Int8":    Int8,
	"Int16":   Int16,
	"Int32":   Int32,
	"Int64":   Int64,
	"UInt8":   UInt8,
	"UInt16":  UInt16,
	"UInt32":  UInt32,
	"UInt64":  UInt64,
	"Byte":    Byte,
	"Float":   Float,
	"Float32": Float32,
	"Float64": Float64,
	"Bool":    Bool,
	"Char":    Char,
	"String":  String,
	"Bytes":   Bytes,
}

// ==== Untyped (polymorphic literals) ====

// UntypedKind classifies an untyped numeric literal.
type UntypedKind int

const (
	UntypedInt UntypedKind = iota
	UntypedFloat
)

// Untyped is the type of a numeric literal whose concrete type is not yet
// fixed (§2.2). Context assigns it later; if no context applies, the
// checker defaults Int→Int, Float→Float.
type Untyped struct {
	Kind UntypedKind
}

func (*Untyped) typeNode() {}

func (u *Untyped) String() string {
	if u.Kind == UntypedFloat {
		return "untyped-float"
	}
	return "untyped-int"
}

// Default returns the fallback concrete type for an untyped literal when
// no context fixes it (§2.2 last paragraph).
func (u *Untyped) Default() Type {
	if u.Kind == UntypedFloat {
		return Float
	}
	return Int
}

// Singletons for the two untyped-literal flavours. Callers should reach
// for these instead of allocating a fresh *Untyped at each literal site.
var (
	UntypedIntVal   = &Untyped{Kind: UntypedInt}
	UntypedFloatVal = &Untyped{Kind: UntypedFloat}
)

// ==== Tuple ====

// Tuple is `(T1, T2, ...)`. The zero-element tuple is modelled as
// Primitive(PUnit), not as Tuple{} — Unit is the distinguished "no value"
// type and tuples are expected to have ≥ 2 elements in surface syntax.
type Tuple struct {
	Elems []Type
}

func (*Tuple) typeNode() {}

func (t *Tuple) String() string {
	var b strings.Builder
	b.WriteByte('(')
	for i, e := range t.Elems {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(e.String())
	}
	b.WriteByte(')')
	return b.String()
}

// ==== Optional (T?) ====

// Optional is `T?`, canonical surface form of Option<T> (§2.5).
// Stored distinctly so the checker can recognize it in optional-chain /
// `?` / nil-coalescing contexts without name-matching against the prelude
// symbol.
type Optional struct {
	Inner Type
}

func (*Optional) typeNode() {}

func (o *Optional) String() string { return o.Inner.String() + "?" }

// ==== FnType ====

// FnType is `fn(A, B) -> R`. Closures and top-level functions share this
// representation. Receiver types are NOT recorded here — the receiver is
// modelled as the first parameter at call-synthesis time, but for method
// *references* the receiver has already been bound.
type FnType struct {
	Params []Type
	Return Type // Unit for fn with no return
}

func (*FnType) typeNode() {}

func (f *FnType) String() string {
	var b strings.Builder
	b.WriteString("fn(")
	for i, p := range f.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.String())
	}
	b.WriteByte(')')
	if f.Return != nil && !IsUnit(f.Return) {
		b.WriteString(" -> ")
		b.WriteString(f.Return.String())
	}
	return b.String()
}

// ==== Named ====

// Named references a named type: a user `struct` / `enum` / `interface` /
// `type` alias, OR one of the builtin compound types (List, Map, Set,
// Option, Result, Error). The identity is the declaring Symbol; the type
// arguments are recorded here.
type Named struct {
	Sym  *resolve.Symbol // identity; nil only for the "unbound" state
	Args []Type          // type arguments; empty for non-generic types
}

func (*Named) typeNode() {}

func (n *Named) Name() string {
	if n.Sym == nil {
		return "<unnamed>"
	}
	return n.Sym.Name
}

func (n *Named) String() string {
	if n.Sym == nil {
		return "<unnamed>"
	}
	if len(n.Args) == 0 {
		return n.Sym.Name
	}
	var b strings.Builder
	b.WriteString(n.Sym.Name)
	b.WriteByte('<')
	for i, a := range n.Args {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(a.String())
	}
	b.WriteByte('>')
	return b.String()
}

// IsBuiltinNamed reports whether this Named refers to a prelude builtin
// (List, Map, Set, Option, Result, Error, Equal, Ordered, Hashable). The
// check walks the Sym kind rather than matching strings so the builtin
// set can evolve without breaking this helper.
func (n *Named) IsBuiltinNamed() bool {
	return n.Sym != nil && n.Sym.Kind == resolve.SymBuiltin
}

// ==== TypeVar (generic parameter) ====

// TypeVar references a `T` (or similar) generic type parameter. The
// Decl Symbol uniquely identifies which binding this is — two T's on
// different functions are distinct.
type TypeVar struct {
	Sym    *resolve.Symbol // SymGeneric
	Bounds []Type          // interface constraints (each a Named interface)
}

func (*TypeVar) typeNode() {}

func (v *TypeVar) String() string {
	if v.Sym == nil {
		return "?"
	}
	return v.Sym.Name
}

// ==== Builder (auto-derived) ====

// Builder is the type of an in-flight `Type.builder()` / `value.toBuilder()`
// chain (§3.4 auto-derived members). It carries:
//
//   - Struct: the target struct's Named type.
//   - Set: the set of field names already supplied via `.fieldName(v)`.
//   - Preloaded: true for builders produced by `value.toBuilder()`; in
//     that case every field is already populated and `.build()` has no
//     required-field obligation to check.
//
// Builder is NOT a user-writable type. It appears only in the checker's
// internal type flow: callers read it from the method-chain they are
// inspecting and never see it in source code. The String() form
// mirrors the spec ("Builder<T>") so diagnostics read naturally.
type Builder struct {
	Struct    *Named
	Set       map[string]bool
	Preloaded bool
}

func (*Builder) typeNode() {}

func (b *Builder) String() string {
	if b.Struct == nil {
		return "Builder<?>"
	}
	return "Builder<" + b.Struct.String() + ">"
}

// WithField returns a copy of b with `name` added to Set.
func (b *Builder) WithField(name string) *Builder {
	set := make(map[string]bool, len(b.Set)+1)
	for k := range b.Set {
		set[k] = true
	}
	set[name] = true
	return &Builder{Struct: b.Struct, Set: set, Preloaded: b.Preloaded}
}

// ==== Error (poisoned) ====

// Error is the poisoned / unknown type. It propagates through expressions
// to suppress cascade diagnostics: once one sub-expression is Error, the
// parent doesn't emit a second complaint.
type Error struct{}

func (*Error) typeNode() {}

func (*Error) String() string { return "<error>" }

// ErrorType is the singleton poisoned type.
var ErrorType Type = &Error{}

// ==== Helpers ====

// IsError reports whether t is the poisoned type.
func IsError(t Type) bool {
	_, ok := t.(*Error)
	return ok
}

// IsNever reports whether t is the bottom type.
func IsNever(t Type) bool {
	p, ok := t.(*Primitive)
	return ok && p.Kind == PNever
}

// IsUnit reports whether t is the unit type `()`.
func IsUnit(t Type) bool {
	p, ok := t.(*Primitive)
	return ok && p.Kind == PUnit
}

// IsBool reports whether t is Bool.
func IsBool(t Type) bool {
	p, ok := t.(*Primitive)
	return ok && p.Kind == PBool
}

// IsNumeric reports whether t is numeric (includes Untyped).
func IsNumeric(t Type) bool {
	switch v := t.(type) {
	case *Primitive:
		return v.Kind.IsNumeric()
	case *Untyped:
		return true
	}
	return false
}

// IsInteger reports whether t is an integer type (includes UntypedInt).
func IsInteger(t Type) bool {
	switch v := t.(type) {
	case *Primitive:
		return v.Kind.IsInteger()
	case *Untyped:
		return v.Kind == UntypedInt
	}
	return false
}

// IsFloat reports whether t is a floating-point type (includes UntypedFloat).
func IsFloat(t Type) bool {
	switch v := t.(type) {
	case *Primitive:
		return v.Kind.IsFloat()
	case *Untyped:
		return v.Kind == UntypedFloat
	}
	return false
}

// IsOptional reports whether t is `T?`.
func IsOptional(t Type) bool {
	_, ok := t.(*Optional)
	return ok
}

// IsOrdered reports whether t supports ordering (for `<`, range patterns).
// Conservative: primitives per §2.6.5, plus TypeVar whose bounds include
// Ordered. Collections are not Ordered.
func IsOrdered(t Type) bool {
	switch v := t.(type) {
	case *Primitive:
		return v.Kind.IsOrdered()
	case *TypeVar:
		return hasBound(v, "Ordered")
	}
	return false
}

// IsEqualable reports whether `==`/`!=` are defined on t.
func IsEqualable(t Type) bool {
	switch v := t.(type) {
	case *Primitive:
		return v.Kind.IsEqual()
	case *Untyped:
		return true
	case *Optional:
		return IsEqualable(v.Inner)
	case *Tuple:
		for _, e := range v.Elems {
			if !IsEqualable(e) {
				return false
			}
		}
		return true
	case *Named:
		// Structs/enums with all-equalable members get auto-derived Equal
		// (§2.9). Here we accept Named conservatively; the checker's
		// refinement lives in the builtin-instance tables elsewhere.
		return true
	case *TypeVar:
		return hasBound(v, "Equal") || hasBound(v, "Ordered") || hasBound(v, "Hashable")
	}
	return false
}

func hasBound(v *TypeVar, name string) bool {
	for _, b := range v.Bounds {
		if n, ok := b.(*Named); ok && n.Sym != nil && n.Sym.Name == name {
			return true
		}
	}
	return false
}

// AsOptional unwraps Optional, returning (inner, true) or (nil, false).
func AsOptional(t Type) (Type, bool) {
	if o, ok := t.(*Optional); ok {
		return o.Inner, true
	}
	return nil, false
}

// AsNamedBuiltin returns the Named value and true if t is a built-in
// generic type (List, Map, Set, Result, Option, Error). For `T?` this
// returns false — use AsOptional for the optional surface form.
func AsNamedBuiltin(t Type, name string) (*Named, bool) {
	n, ok := t.(*Named)
	if !ok || n.Sym == nil {
		return nil, false
	}
	if n.Sym.Kind != resolve.SymBuiltin {
		return nil, false
	}
	if n.Sym.Name != name {
		return nil, false
	}
	return n, true
}

// AsNamedByName returns the Named value when t is ANY Named type whose
// symbol has the given name, regardless of whether the symbol is a
// prelude builtin or a user-declared shadow. Intended for constructs
// like the `?` operator that treat `Result` structurally.
func AsNamedByName(t Type, name string) (*Named, bool) {
	n, ok := t.(*Named)
	if !ok || n.Sym == nil {
		return nil, false
	}
	if n.Sym.Name != name {
		return nil, false
	}
	return n, true
}

// AsFn returns the FnType and true if t is one.
func AsFn(t Type) (*FnType, bool) {
	f, ok := t.(*FnType)
	return f, ok
}

// AsNamed returns the Named and true if t is one.
func AsNamed(t Type) (*Named, bool) {
	n, ok := t.(*Named)
	return n, ok
}
