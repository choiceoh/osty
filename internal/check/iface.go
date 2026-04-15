package check

import (
	"sort"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/types"
)

// builtinSatisfies reports whether `t` satisfies one of the built-in
// marker interfaces per v0.3 §2.6.5. Returns (matched, ok):
// matched=true means the interface name was recognized as built-in;
// ok=true means `t` satisfies it.
//
// Coverage:
//   - Primitives row 1–6 of §2.6.5
//   - Tuple, Optional, Result: per-component derivation
//   - List<T>, Set<T>, Map<K,V>: per-component derivation
//     (Equal/Hashable only — §2.6.5 explicitly excludes Ordered for
//     collections)
//   - Error: requires `message(self) -> String`; `source` is optional
//     (default body provided per §7.1)
//
// Anything not matching one of these falls through with matched=false
// so the caller can apply the structural-method-set rule.
func builtinSatisfies(c *checker, iface string, t types.Type) (matched, ok bool) {
	switch iface {
	case "Equal":
		return true, builtinEqual(c, t)
	case "Ordered":
		return true, builtinOrdered(c, t)
	case "Hashable":
		return true, builtinHashable(c, t)
	case "Error":
		return true, builtinError(c, t)
	}
	return false, false
}

// builtinEqual implements the Equal column of §2.6.5 plus the
// auto-derivation rules of §2.9. Float satisfies Equal (NaN is the
// documented exception, not a removal from the row).
func builtinEqual(c *checker, t types.Type) bool {
	switch v := t.(type) {
	case *types.Primitive:
		return v.Kind.IsEqual()
	case *types.Untyped:
		return true
	case *types.Optional:
		return builtinEqual(c, v.Inner)
	case *types.Tuple:
		for _, e := range v.Elems {
			if !builtinEqual(c, e) {
				return false
			}
		}
		return true
	case *types.Named:
		if v.Sym == nil {
			return false
		}
		switch v.Sym.Name {
		case "Result":
			for _, a := range v.Args {
				if !builtinEqual(c, a) {
					return false
				}
			}
			return true
		case "List", "Set":
			if len(v.Args) == 1 {
				return builtinEqual(c, v.Args[0])
			}
			return true
		case "Map":
			if len(v.Args) == 2 {
				return builtinEqual(c, v.Args[0]) && builtinEqual(c, v.Args[1])
			}
			return true
		}
		// User struct/enum: §2.9 auto-derives Equal when every component
		// is Equal. We approximate by recursing into fields/variant
		// payloads when available; opaque user types default to true so
		// generic bounds aren't pessimistic.
		if desc, ok := c.result.Descs[v.Sym]; ok {
			switch desc.Kind {
			case resolve.SymStruct:
				return allFieldsSatisfy(c, desc, v.Args, builtinEqual)
			case resolve.SymEnum:
				return allVariantPayloadsSatisfy(c, desc, v.Args, builtinEqual)
			case resolve.SymTypeAlias:
				if desc.Alias != nil {
					return builtinEqual(c, desc.Alias)
				}
			}
		}
		return true
	case *types.TypeVar:
		return typeVarHasBound(v, "Equal", "Ordered", "Hashable")
	}
	return false
}

// builtinOrdered implements the Ordered column. §2.6.5 explicitly
// excludes collections (List/Map/Set) from auto-derivation.
func builtinOrdered(c *checker, t types.Type) bool {
	switch v := t.(type) {
	case *types.Primitive:
		return v.Kind.IsOrdered()
	case *types.Untyped:
		return true
	case *types.Optional:
		return builtinOrdered(c, v.Inner)
	case *types.Tuple:
		// Tuples are not in the Ordered column of §2.6.5 — left blank.
		return false
	case *types.Named:
		if v.Sym == nil {
			return false
		}
		switch v.Sym.Name {
		case "List", "Set", "Map", "Result":
			return false
		}
		if desc, ok := c.result.Descs[v.Sym]; ok && desc.Kind == resolve.SymTypeAlias && desc.Alias != nil {
			return builtinOrdered(c, desc.Alias)
		}
		// User types are not Ordered unless they explicitly implement it
		// (handled by the structural path, not here).
		return false
	case *types.TypeVar:
		return typeVarHasBound(v, "Ordered")
	}
	return false
}

// builtinHashable mirrors §2.6.5 with the Float exception.
func builtinHashable(c *checker, t types.Type) bool {
	switch v := t.(type) {
	case *types.Primitive:
		if v.Kind.IsFloat() {
			return false
		}
		return v.Kind.IsEqual()
	case *types.Untyped:
		// Untyped numeric defaults to Int (Hashable) or Float (not).
		// Conservatively accept; the literal is always promoted before
		// reaching a hashing context.
		return v.Kind == types.UntypedInt
	case *types.Optional:
		return builtinHashable(c, v.Inner)
	case *types.Tuple:
		for _, e := range v.Elems {
			if !builtinHashable(c, e) {
				return false
			}
		}
		return true
	case *types.Named:
		if v.Sym == nil {
			return false
		}
		switch v.Sym.Name {
		case "Result":
			for _, a := range v.Args {
				if !builtinHashable(c, a) {
					return false
				}
			}
			return true
		case "List", "Set":
			if len(v.Args) == 1 {
				return builtinHashable(c, v.Args[0])
			}
			return true
		case "Map":
			if len(v.Args) == 2 {
				return builtinHashable(c, v.Args[0]) && builtinHashable(c, v.Args[1])
			}
			return true
		}
		if desc, ok := c.result.Descs[v.Sym]; ok {
			switch desc.Kind {
			case resolve.SymStruct:
				return allFieldsSatisfy(c, desc, v.Args, builtinHashable)
			case resolve.SymEnum:
				return allVariantPayloadsSatisfy(c, desc, v.Args, builtinHashable)
			case resolve.SymTypeAlias:
				if desc.Alias != nil {
					return builtinHashable(c, desc.Alias)
				}
			}
		}
		return true
	case *types.TypeVar:
		return typeVarHasBound(v, "Hashable")
	}
	return false
}

// builtinError implements §7.1: a type satisfies Error iff it provides
// `message(self) -> String`. `source(self) -> Error?` carries a default
// body so its absence on the concrete type is acceptable.
func builtinError(c *checker, t types.Type) bool {
	// The prelude Error itself trivially satisfies Error.
	if n, ok := t.(*types.Named); ok && n.Sym != nil && n.Sym.Name == "Error" && n.Sym.Kind == resolve.SymBuiltin {
		return true
	}
	md, _ := c.lookupMethod(t, "message")
	if md == nil {
		return false
	}
	if md.Fn == nil {
		return false
	}
	if len(md.Fn.Params) != 0 {
		return false
	}
	return types.Identical(md.Fn.Return, types.String)
}

// allFieldsSatisfy applies pred to every struct field, after
// substituting the struct's generics with v.Args.
func allFieldsSatisfy(c *checker, desc *typeDesc, args []types.Type, pred func(*checker, types.Type) bool) bool {
	sub := bindArgs(desc.Generics, args)
	for _, f := range desc.Fields {
		ft := f.Type
		if len(sub) > 0 {
			ft = types.Substitute(ft, sub)
		}
		if !pred(c, ft) {
			return false
		}
	}
	return true
}

// allVariantPayloadsSatisfy applies pred to every payload type across
// every variant of an enum.
func allVariantPayloadsSatisfy(c *checker, desc *typeDesc, args []types.Type, pred func(*checker, types.Type) bool) bool {
	sub := bindArgs(desc.Generics, args)
	for _, vd := range desc.Variants {
		for _, ft := range vd.Fields {
			x := ft
			if len(sub) > 0 {
				x = types.Substitute(x, sub)
			}
			if !pred(c, x) {
				return false
			}
		}
	}
	return true
}

// typeVarHasBound reports whether the TypeVar carries any of `names`
// as a built-in marker bound. Mirrors the spec rule that Ordered
// implies Equal, Hashable implies Equal.
func typeVarHasBound(v *types.TypeVar, names ...string) bool {
	for _, b := range v.Bounds {
		nm, ok := b.(*types.Named)
		if !ok || nm.Sym == nil {
			continue
		}
		for _, want := range names {
			if nm.Sym.Name == want {
				return true
			}
		}
	}
	return false
}

// satisfies reports whether `concrete` satisfies the interface type
// `iface`. The check is STRUCTURAL: the concrete type must expose every
// method named in the interface, with parameter and return types that
// match after Self / receiver-generic / interface-generic substitution.
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
	// structural method-set matching can't capture (Equal/Ordered/
	// Hashable on primitives & composites; Error's "message + optional
	// source" shape).
	if matched, ok := builtinSatisfies(c, iface.Sym.Name, concrete); matched {
		if !ok {
			c.errNode(pos, diag.CodeTypeMismatch,
				"type `%s` does not implement `%s`", concrete, iface.Sym.Name)
		}
		return ok
	}

	// User or prelude-user interface (Writer, Reader, ...). Walk
	// composition graph to flatten the required method set per §2.6.1.
	required, ifaceSubs, ok := c.flattenInterface(iface)
	if !ok {
		// Unknown interface descriptor (stdlib stub, unresolved) —
		// accept optimistically so downstream checking proceeds.
		return true
	}

	missing := []string{}
	mismatched := []string{}
	// Sort for deterministic diagnostic order.
	names := make([]string, 0, len(required))
	for n := range required {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		req := required[name]
		gotMd, sub := c.lookupMethod(concrete, name)
		if gotMd == nil {
			// Default-bodied interface methods (§2.6.2) don't have to be
			// re-implemented by the concrete type.
			if req.md != nil && req.md.HasBody {
				continue
			}
			missing = append(missing, name)
			continue
		}
		// Build the substitution map seen by methodSignaturesMatch:
		//   1. concrete-side owner generics → concrete args
		//      (already returned by lookupMethod as `sub`)
		//   2. interface-side generics → interface args (ifaceSubs)
		//   3. Self → concrete (handled inside methodSignaturesMatch)
		merged := mergeSubs(sub, ifaceSubs[req.ifaceSym])
		if !methodSignaturesMatch(req.md.Fn, gotMd.Fn, merged, req.ifaceSym, concrete) {
			mismatched = append(mismatched, name)
		}
	}

	if len(missing) == 0 && len(mismatched) == 0 {
		return true
	}
	if len(missing) > 0 {
		c.errNode(pos, diag.CodeTypeMismatch,
			"type `%s` does not implement `%s`: missing method(s): %s",
			concrete, iface.Sym.Name, joinMethods(missing))
	}
	if len(mismatched) > 0 {
		c.errNode(pos, diag.CodeTypeMismatch,
			"type `%s` does not satisfy `%s`: method(s) with different signature: %s",
			concrete, iface.Sym.Name, joinMethods(mismatched))
	}
	return false
}

// requiredMethod records one method the satisfying type must provide,
// along with the iface symbol it originated from (used for Self
// substitution when matching signatures).
type requiredMethod struct {
	md       *methodDesc
	ifaceSym *resolve.Symbol
}

// flattenInterface walks the §2.6.1 composition chain and returns:
//   - required: name → requiredMethod, the union of every interface's
//     own methods (later definitions DON'T override — every interface
//     contributes its own signature; conflicts produce a missing-from-
//     concrete diagnostic only if the concrete type doesn't satisfy any
//     of them).
//   - ifaceSubs: map from each visited interface symbol to its
//     generic-arg substitution map. Used by satisfies to substitute
//     interface-side generics in the want signature.
//   - ok: false iff the root interface has no descriptor (stdlib stub).
func (c *checker) flattenInterface(iface *types.Named) (map[string]requiredMethod, map[*resolve.Symbol]map[*resolve.Symbol]types.Type, bool) {
	if iface == nil || iface.Sym == nil {
		return nil, nil, false
	}
	ifDesc, ok := c.result.Descs[iface.Sym]
	if !ok {
		return nil, nil, false
	}
	if ifDesc.Kind != resolve.SymInterface {
		return nil, nil, false
	}

	required := map[string]requiredMethod{}
	ifaceSubs := map[*resolve.Symbol]map[*resolve.Symbol]types.Type{}
	visited := map[*resolve.Symbol]bool{}

	var walk func(n *types.Named)
	walk = func(n *types.Named) {
		if n == nil || n.Sym == nil || visited[n.Sym] {
			return
		}
		visited[n.Sym] = true
		desc, ok := c.result.Descs[n.Sym]
		if !ok || desc.Kind != resolve.SymInterface {
			return
		}
		ifaceSubs[n.Sym] = bindArgs(desc.Generics, n.Args)
		for name, md := range desc.InterfaceMethods {
			// First wins; an earlier-collected interface owns the slot.
			if _, exists := required[name]; !exists {
				required[name] = requiredMethod{md: md, ifaceSym: n.Sym}
			}
		}
		for _, ext := range desc.Extends {
			walk(ext)
		}
	}
	walk(iface)
	return required, ifaceSubs, true
}

// mergeSubs returns a shallow copy of a + b. b wins on key collisions.
// Either argument may be nil.
func mergeSubs(a, b map[*resolve.Symbol]types.Type) map[*resolve.Symbol]types.Type {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[*resolve.Symbol]types.Type, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// methodSignaturesMatch compares the declared interface signature with
// a concrete method signature after Self / generic substitution.
// Both signatures exclude `self`, so it is a straight parameter-list +
// return comparison.
//
// `ifaceSym` and `concreteSelf` are passed so occurrences of `Self`
// inside the want signature (which the resolver pre-bound to the
// interface's symbol) are rewritten to the concrete type before
// identity comparison.
func methodSignaturesMatch(want, got *types.FnType, sub map[*resolve.Symbol]types.Type, ifaceSym *resolve.Symbol, concreteSelf types.Type) bool {
	if want == nil || got == nil {
		return want == got
	}
	rewriteSelf := func(t types.Type) types.Type {
		t = types.Substitute(t, sub)
		if ifaceSym != nil && concreteSelf != nil {
			t = substituteSelf(t, ifaceSym, concreteSelf)
		}
		return t
	}
	if len(want.Params) != len(got.Params) {
		return false
	}
	for i, p := range want.Params {
		if !types.Identical(rewriteSelf(p), got.Params[i]) {
			return false
		}
	}
	return types.Identical(rewriteSelf(want.Return), got.Return)
}

// substituteSelf walks t and rewrites every `Named{Sym: ifaceSym}`
// occurrence to `concrete`. Inside an interface body the resolver
// resolves `Self` to the interface's symbol; structural matching
// against a concrete type's method must replace those occurrences with
// the concrete type to be consistent with §2.6.3.
//
// The walk descends into Optional, Tuple, FnType, and Named.Args.
// TypeVars and Primitives pass through. Any nominal `Named` whose Sym
// is not the interface's is left intact, so a method declared in
// `interface Foo` that returns `Bar` (where Bar is a distinct named
// type) is not mistakenly rewritten.
func substituteSelf(t types.Type, ifaceSym *resolve.Symbol, concrete types.Type) types.Type {
	if t == nil || ifaceSym == nil || concrete == nil {
		return t
	}
	switch x := t.(type) {
	case *types.Named:
		if x.Sym == ifaceSym && len(x.Args) == 0 {
			return concrete
		}
		if len(x.Args) == 0 {
			return x
		}
		args := make([]types.Type, len(x.Args))
		for i, a := range x.Args {
			args[i] = substituteSelf(a, ifaceSym, concrete)
		}
		return &types.Named{Sym: x.Sym, Args: args}
	case *types.Optional:
		return &types.Optional{Inner: substituteSelf(x.Inner, ifaceSym, concrete)}
	case *types.Tuple:
		elems := make([]types.Type, len(x.Elems))
		for i, e := range x.Elems {
			elems[i] = substituteSelf(e, ifaceSym, concrete)
		}
		return &types.Tuple{Elems: elems}
	case *types.FnType:
		params := make([]types.Type, len(x.Params))
		for i, p := range x.Params {
			params[i] = substituteSelf(p, ifaceSym, concrete)
		}
		return &types.FnType{Params: params, Return: substituteSelf(x.Return, ifaceSym, concrete)}
	}
	return t
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
// components are ToString, tuples when every element is ToString, user
// struct/enum (when every component is ToString — auto-derivation
// rule), generic TypeVars, and Never all qualify. Function types and
// in-flight Builders are deliberately rejected (§2.9, §3.4).
func hasToString(c *checker, t types.Type) bool {
	return hasToStringV(c, t, map[*resolve.Symbol]bool{})
}

// hasToStringV is the recursive worker. `seen` breaks cycles in
// recursive user types (`struct Node<T> { value: T, next: Node<T>? }`).
// A type currently mid-recursion is treated as ToString-compatible —
// the auto-derived implementation for the enclosing type is only
// blocked when a *non-recursive* component fails the predicate.
func hasToStringV(c *checker, t types.Type, seen map[*resolve.Symbol]bool) bool {
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
		return hasToStringV(c, v.Inner, seen)
	case *types.Tuple:
		for _, e := range v.Elems {
			if !hasToStringV(c, e, seen) {
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
				if !hasToStringV(c, a, seen) {
					return false
				}
			}
			return true
		case "List", "Set":
			if len(v.Args) == 1 {
				return hasToStringV(c, v.Args[0], seen)
			}
			return true
		case "Map":
			if len(v.Args) == 2 {
				return hasToStringV(c, v.Args[0], seen) && hasToStringV(c, v.Args[1], seen)
			}
			return true
		}
		desc, ok := c.result.Descs[v.Sym]
		if !ok {
			return true
		}
		// Cycle break: treat self-recursive references as compatible —
		// the recursion will be resolved by a base case at another
		// node of the structure.
		if seen[v.Sym] {
			return true
		}
		// User-provided toString always wins over auto-derivation.
		if desc.Methods != nil {
			if md, has := desc.Methods["toString"]; has && md.Fn != nil &&
				len(md.Fn.Params) == 0 && types.Identical(md.Fn.Return, types.String) {
				return true
			}
		}
		nextSeen := copyAndMark(seen, v.Sym)
		switch desc.Kind {
		case resolve.SymStruct:
			for _, f := range desc.Fields {
				ft := f.Type
				if len(v.Args) > 0 {
					ft = types.Substitute(ft, bindArgs(desc.Generics, v.Args))
				}
				if !hasToStringV(c, ft, nextSeen) {
					return false
				}
			}
			return true
		case resolve.SymEnum:
			for _, vd := range desc.Variants {
				for _, ft := range vd.Fields {
					x := ft
					if len(v.Args) > 0 {
						x = types.Substitute(x, bindArgs(desc.Generics, v.Args))
					}
					if !hasToStringV(c, x, nextSeen) {
						return false
					}
				}
			}
			return true
		case resolve.SymTypeAlias:
			if desc.Alias != nil {
				return hasToStringV(c, desc.Alias, nextSeen)
			}
			return true
		case resolve.SymInterface:
			// Interface values carry a vtable; the dispatch landing on
			// the underlying concrete value's toString is guaranteed
			// when the interface's methods include toString or it
			// composes ToString. Accept conservatively.
			return true
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

func copyAndMark(seen map[*resolve.Symbol]bool, sym *resolve.Symbol) map[*resolve.Symbol]bool {
	out := make(map[*resolve.Symbol]bool, len(seen)+1)
	for k, v := range seen {
		out[k] = v
	}
	out[sym] = true
	return out
}
