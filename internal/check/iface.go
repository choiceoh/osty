package check

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/types"
)

// builtinSatisfies reports whether a primitive type satisfies one of the
// built-in marker interfaces per v0.3 §2.6.5. Returns (matched, ok):
// matched=true means the interface name was recognized as built-in;
// ok=true means the primitive satisfies it.
func builtinSatisfies(iface string, t types.Type) (matched, ok bool) {
	p, isPrim := t.(*types.Primitive)
	if !isPrim {
		return false, false
	}
	switch iface {
	case "Equal":
		return true, p.Kind.IsEqual()
	case "Ordered":
		return true, p.Kind.IsOrdered()
	case "Hashable":
		// Float / Float32 / Float64 are not Hashable per §2.6.5‡.
		if p.Kind.IsFloat() {
			return true, false
		}
		return true, p.Kind.IsEqual() && !p.Kind.IsFloat()
	}
	return false, false
}

// satisfies reports whether `concrete` satisfies the interface type
// `iface`. The check is STRUCTURAL: the concrete type must expose every
// method named in the interface, with parameter and return types that
// match after receiver-generic substitution.
//
// Reports diagnostics at `pos` for missing or mismatched methods. The
// returned bool is a summary used by generic-bound enforcement to avoid
// cascading errors on an already-rejected instantiation.
func (c *checker) satisfies(concrete types.Type, iface *types.Named, pos ast.Node) bool {
	if iface == nil || iface.Sym == nil {
		return true
	}
	if types.IsError(concrete) {
		return true // suppressed
	}
	// Unconstrained TypeVars and still-symbolic generics trivially match
	// — the call-site couldn't infer a concrete type, so complaining
	// about the bound would be noise on top of the root cause.
	if _, ok := concrete.(*types.TypeVar); ok {
		return true
	}

	// Built-in marker interfaces first — they have semantic rules that
	// structural method-set matching can't capture.
	if matched, ok := builtinSatisfies(iface.Sym.Name, concrete); matched {
		if !ok {
			c.errNode(pos, diag.CodeTypeMismatch,
				"type `%s` does not implement `%s`", concrete, iface.Sym.Name)
		}
		return ok
	}

	// User or prelude-user interface (Writer, Reader, Error, ...).
	ifDesc, ok := c.result.Descs[iface.Sym]
	if !ok {
		// Unknown interface descriptor (stdlib stub, unresolved) —
		// accept optimistically so downstream checking proceeds.
		return true
	}
	if ifDesc.Kind != resolve.SymInterface {
		// Not actually an interface; identity check is the caller's
		// responsibility.
		return true
	}

	missing := []string{}
	for name := range ifDesc.InterfaceMethods {
		wantMd := ifDesc.InterfaceMethods[name]
		gotMd, sub := c.lookupMethod(concrete, name)
		if gotMd == nil {
			missing = append(missing, name)
			continue
		}
		if !methodSignaturesMatch(wantMd.Fn, gotMd.Fn, sub) {
			c.errNode(pos, diag.CodeTypeMismatch,
				"type `%s` does not satisfy `%s`: method `%s` has a different signature",
				concrete, iface.Sym.Name, name)
			return false
		}
	}
	if len(missing) > 0 {
		c.errNode(pos, diag.CodeTypeMismatch,
			"type `%s` does not implement `%s`: missing method(s): %s",
			concrete, iface.Sym.Name, joinMethods(missing))
		return false
	}
	return true
}

// methodSignaturesMatch compares the declared interface signature with
// a concrete method signature after receiver-generic substitution.
// Both signatures exclude `self`, so it is a straight parameter-list +
// return comparison.
func methodSignaturesMatch(want, got *types.FnType, sub map[*resolve.Symbol]types.Type) bool {
	if want == nil || got == nil {
		return want == got
	}
	wantParams := want.Params
	if len(sub) > 0 {
		wantParams = make([]types.Type, len(want.Params))
		for i, p := range want.Params {
			wantParams[i] = types.Substitute(p, sub)
		}
	}
	if len(wantParams) != len(got.Params) {
		return false
	}
	for i, p := range wantParams {
		if !types.Identical(p, got.Params[i]) {
			return false
		}
	}
	wantRet := want.Return
	if len(sub) > 0 {
		wantRet = types.Substitute(wantRet, sub)
	}
	return types.Identical(wantRet, got.Return)
}

func joinMethods(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += "`" + n + "`"
	}
	return out
}

// checkBounds enforces a generic-parameter's interface bounds against
// its concrete instantiation argument. Called after applyGenericCall
// finishes inference — each pair (T, arg) is passed here so every
// declared bound on T is checked against `arg`.
func (c *checker) checkBounds(g *types.TypeVar, arg types.Type, pos ast.Node) {
	if g == nil || arg == nil {
		return
	}
	for _, b := range g.Bounds {
		n, ok := b.(*types.Named)
		if !ok {
			continue
		}
		c.satisfies(arg, n, pos)
	}
}

// hasToString reports whether a type implements the §17 display
// protocol — the one that makes it legal as an interpolation argument.
// Primitives (all of §2.6.5 row 1-6), Optionals and Results whose
// components are ToString, tuples when every element is ToString, any
// user struct/enum (they carry the auto-derived ToString), generic
// TypeVars where the source happens to be primitive-like, and Never
// all qualify. Function types are deliberately rejected (§2.9).
func hasToString(c *checker, t types.Type) bool {
	switch v := t.(type) {
	case *types.Primitive:
		switch v.Kind {
		case types.PInt, types.PInt8, types.PInt16, types.PInt32, types.PInt64,
			types.PUInt8, types.PUInt16, types.PUInt32, types.PUInt64, types.PByte,
			types.PFloat, types.PFloat32, types.PFloat64,
			types.PBool, types.PChar, types.PString, types.PBytes,
			types.PUnit, types.PNever:
			return true
		}
		return false
	case *types.Untyped:
		return true
	case *types.Optional:
		return hasToString(c, v.Inner)
	case *types.Tuple:
		for _, e := range v.Elems {
			if !hasToString(c, e) {
				return false
			}
		}
		return true
	case *types.Named:
		if v.Sym == nil {
			return false
		}
		// Built-in generic shells (Result, List, Map, Set) forward to
		// their component types' ToString status.
		switch v.Sym.Name {
		case "Result":
			for _, a := range v.Args {
				if !hasToString(c, a) {
					return false
				}
			}
			return true
		case "List", "Set":
			if len(v.Args) == 1 {
				return hasToString(c, v.Args[0])
			}
			return true
		case "Map":
			if len(v.Args) == 2 {
				return hasToString(c, v.Args[0]) && hasToString(c, v.Args[1])
			}
			return true
		}
		// User-defined struct / enum / alias — structurally available.
		if desc, ok := c.result.Descs[v.Sym]; ok {
			switch desc.Kind {
			case resolve.SymStruct, resolve.SymEnum, resolve.SymTypeAlias:
				return true
			}
		}
		return true
	case *types.TypeVar:
		// Generic parameters are assumed ToString-compatible when they
		// carry a bound implying a displayable shape (§17 promises this
		// for `Ordered`, `Equal`, `Hashable` which all primitives do).
		return true
	case *types.FnType:
		return false
	case *types.Builder:
		return false
	}
	return false
}
