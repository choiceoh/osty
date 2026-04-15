package types

import "github.com/osty/osty/internal/resolve"

// Identical reports whether two types are the same type up to structural
// equality. This is the strictest relation: used for method signature
// matching, method receiver checks, and places where no coercion is
// allowed.
//
// Notes
//
//   - Error is identical to every type (poisoning suppresses cascades).
//   - Never is identical to every type in the ASSIGNABLE direction
//     (see Assignable); for Identical it is identical only to itself.
//   - Untyped is identical to a compatible primitive per UntypedFits.
func Identical(a, b Type) bool {
	if a == nil || b == nil {
		return a == b
	}
	if IsError(a) || IsError(b) {
		return true
	}

	switch x := a.(type) {
	case *Primitive:
		y, ok := b.(*Primitive)
		if !ok {
			return false
		}
		return x.Kind == y.Kind
	case *Untyped:
		// An untyped literal is identical to a primitive it can inhabit.
		switch y := b.(type) {
		case *Untyped:
			return x.Kind == y.Kind
		case *Primitive:
			return UntypedFits(x, y)
		}
		return false
	case *Optional:
		y, ok := b.(*Optional)
		if !ok {
			return false
		}
		return Identical(x.Inner, y.Inner)
	case *Tuple:
		y, ok := b.(*Tuple)
		if !ok || len(x.Elems) != len(y.Elems) {
			return false
		}
		for i, e := range x.Elems {
			if !Identical(e, y.Elems[i]) {
				return false
			}
		}
		return true
	case *FnType:
		y, ok := b.(*FnType)
		if !ok || len(x.Params) != len(y.Params) {
			return false
		}
		for i, p := range x.Params {
			if !Identical(p, y.Params[i]) {
				return false
			}
		}
		return Identical(x.Return, y.Return)
	case *Named:
		y, ok := b.(*Named)
		if !ok || !sameNamedSymbol(x.Sym, y.Sym) {
			return false
		}
		if len(x.Args) != len(y.Args) {
			return false
		}
		for i, a := range x.Args {
			if !Identical(a, y.Args[i]) {
				return false
			}
		}
		return true
	case *TypeVar:
		y, ok := b.(*TypeVar)
		return ok && x.Sym == y.Sym
	}
	// Untyped on the right
	if uy, ok := b.(*Untyped); ok {
		if px, pok := a.(*Primitive); pok {
			return UntypedFits(uy, px)
		}
	}
	return false
}

func sameNamedSymbol(a, b *resolve.Symbol) bool {
	if a == b {
		return true
	}
	return a != nil &&
		b != nil &&
		a.IsBuiltin() &&
		b.IsBuiltin() &&
		a.Name == b.Name
}

// Assignable reports whether a value of type `src` can be assigned to a
// destination of type `dst`. The rules track v0.3:
//
//   - Identical types are assignable.
//   - Never is assignable to any type (bottom).
//   - Untyped literals are assignable to any primitive they fit in.
//   - Otherwise: no implicit conversions (§2.2). Numeric widening must
//     go through .toXx() methods; struct/enum compatibility requires
//     identity.
//   - Error (poisoned) is assignable in both directions to avoid
//     cascades.
//   - TypeVar destinations accept any source: a generic parameter
//     represents "any type" until monomorphized. Arg-to-param unification
//     that *should* narrow across arguments is handled at the call site;
//     the pure Assignable relation is deliberately permissive here.
func Assignable(dst, src Type) bool {
	if dst == nil || src == nil {
		return false
	}
	if IsError(dst) || IsError(src) {
		return true
	}
	if IsNever(src) {
		return true
	}
	// Generic target accepts any value type. Per-call inference would
	// tighten this by unifying all arg positions before deciding; the
	// MVP checker defers that step.
	if _, ok := dst.(*TypeVar); ok {
		return true
	}
	if _, ok := src.(*TypeVar); ok {
		return true
	}
	// Untyped integer fits in any integer primitive it can represent;
	// untyped float fits in any float primitive.
	if u, ok := src.(*Untyped); ok {
		if p, pok := dst.(*Primitive); pok {
			return UntypedFits(u, p)
		}
		// Untyped literal can also flow into an Optional if its inner
		// accepts it: `let x: Int? = 5` → 5 is UntypedInt, Inner is Int.
		if o, ook := dst.(*Optional); ook {
			if p, pok := o.Inner.(*Primitive); pok {
				return UntypedFits(u, p)
			}
		}
		// Untyped → Untyped (rare; e.g., propagating through block)
		if u2, ok := dst.(*Untyped); ok {
			return u.Kind == u2.Kind
		}
		return false
	}
	// A concrete value flows into an Optional: `let x: Int? = 5_i32` if
	// the int type matches inner. This is the implicit `Some(v)` wrap
	// that v0.3 semantically supports via inference at the literal level.
	if o, ok := dst.(*Optional); ok {
		if Identical(o.Inner, src) {
			return true
		}
		// None literal surface: None has type Option<?> / unresolved
		// Optional — handled at the checker level, not here.
	}
	return Identical(dst, src)
}

// UntypedFits reports whether an Untyped literal can inhabit a given
// primitive type (§2.2 "Literal inference").
func UntypedFits(u *Untyped, p *Primitive) bool {
	switch u.Kind {
	case UntypedInt:
		return p.Kind.IsInteger() || p.Kind.IsFloat()
	case UntypedFloat:
		return p.Kind.IsFloat()
	}
	return false
}

// Unify tries to produce a single type that fits both a and b, used by
// `if`/`match`/expression-level block merging. Strategy:
//
//  1. If either is Error, the other wins (error already reported).
//  2. If either is Never, the other wins.
//  3. If both are Untyped of the same kind, keep Untyped.
//  4. If one is Untyped and the other is a primitive it fits, take the
//     primitive (context-driven).
//  5. Otherwise require Identical.
//
// Returns (merged, true) on success, (nil, false) on incompatibility.
func Unify(a, b Type) (Type, bool) {
	if IsError(a) {
		return b, true
	}
	if IsError(b) {
		return a, true
	}
	if IsNever(a) {
		return b, true
	}
	if IsNever(b) {
		return a, true
	}
	if ua, ok := a.(*Untyped); ok {
		if ub, ok := b.(*Untyped); ok {
			if ua.Kind == ub.Kind {
				return a, true
			}
			// untypedInt + untypedFloat → UntypedFloat (§2.2 implied
			// by literal flexibility).
			return UntypedFloatVal, true
		}
		if pb, ok := b.(*Primitive); ok && UntypedFits(ua, pb) {
			return b, true
		}
	}
	if ub, ok := b.(*Untyped); ok {
		if pa, ok := a.(*Primitive); ok && UntypedFits(ub, pa) {
			return a, true
		}
	}
	if Identical(a, b) {
		return a, true
	}
	return nil, false
}
