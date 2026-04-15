package check

import (
	"sort"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/types"
)

// Built-in marker interface names. Centralised so the structural
// dispatch in builtinSatisfies and the prelude-binding table in
// check.go agree by reference rather than by string.
const (
	builtinEqualName    = "Equal"
	builtinOrderedName  = "Ordered"
	builtinHashableName = "Hashable"
	builtinErrorName    = "Error"
)

// builtinSatisfies reports whether `t` satisfies one of the built-in
// marker interfaces per v0.3 §2.6.5 / §7.1. Returns (matched, ok):
// matched=true means the interface name was recognized as built-in;
// ok=true means `t` satisfies it.
//
// Anything not matching one of these falls through with matched=false
// so the caller can apply the structural-method-set rule.
func builtinSatisfies(c *checker, iface string, t types.Type) (matched, ok bool) {
	switch iface {
	case builtinEqualName:
		return true, builtinEqual(c, t)
	case builtinOrderedName:
		return true, builtinOrdered(c, t)
	case builtinHashableName:
		return true, builtinHashable(c, t)
	case builtinErrorName:
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
		return types.HasBound(v, builtinEqualName) ||
			types.HasBound(v, builtinOrderedName) ||
			types.HasBound(v, builtinHashableName)
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
		return types.HasBound(v, builtinOrderedName)
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
		return types.HasBound(v, builtinHashableName)
	}
	return false
}

// builtinError implements §7.1: a type satisfies Error iff it provides
// `message(self) -> String`. `source(self) -> Error?` carries a default
// body so its absence on the concrete type is acceptable.
func builtinError(c *checker, t types.Type) bool {
	if _, ok := types.AsNamedBuiltin(t, builtinErrorName); ok {
		return true
	}
	md, _ := c.lookupMethod(t, "message")
	if md == nil || md.Fn == nil {
		return false
	}
	if len(md.Fn.Params) != 0 {
		return false
	}
	return types.Identical(md.Fn.Return, types.String)
}

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

	var missing []string
	var mismatched []sigMismatch
	for name, req := range required {
		gotMd, sub := c.lookupMethod(concrete, name)
		if gotMd == nil {
			if req.md != nil && req.md.HasBody {
				// §2.6.2 default body — concrete type may omit it.
				continue
			}
			missing = append(missing, name)
			continue
		}
		merged := mergeSubs(sub, ifaceSubs[req.ifaceSym])
		if !methodSignaturesMatch(req.md.Fn, gotMd.Fn, merged, req.ifaceSym, concrete) {
			mismatched = append(mismatched, sigMismatch{
				name: name,
				want: substituteForDisplay(req.md.Fn, merged, req.ifaceSym, concrete),
				got:  gotMd.Fn,
			})
		}
	}

	if len(missing) == 0 && len(mismatched) == 0 {
		return true
	}
	// Sort only the failing slices so the success path skips the work.
	sort.Strings(missing)
	sort.Slice(mismatched, func(i, j int) bool { return mismatched[i].name < mismatched[j].name })

	if len(missing) > 0 {
		c.errNode(pos, diag.CodeTypeMismatch,
			"type `%s` does not implement `%s`: missing method(s): %s",
			concrete, iface.Sym.Name, joinMethods(missing))
	}
	for _, m := range mismatched {
		c.errNode(pos, diag.CodeTypeMismatch,
			"type `%s` does not satisfy `%s`: method `%s` has signature `%s`, expected `%s`",
			concrete, iface.Sym.Name, m.name, m.got, m.want)
	}
	return false
}

// sigMismatch captures one method whose concrete signature didn't
// match the interface's required shape, retained so the diagnostic
// can show want-vs-got per method instead of a comma-joined name list.
type sigMismatch struct {
	name string
	want *types.FnType
	got  *types.FnType
}

// substituteForDisplay returns the want signature with all currently
// known substitutions applied so the diagnostic shows the post-
// substitution shape the concrete method was being matched against.
// Errors during substitution fall back to the raw want.
func substituteForDisplay(fn *types.FnType, sub map[*resolve.Symbol]types.Type, ifaceSym *resolve.Symbol, concrete types.Type) *types.FnType {
	if fn == nil {
		return nil
	}
	out := &types.FnType{
		Params: make([]types.Type, len(fn.Params)),
		Return: fn.Return,
	}
	rewrite := func(t types.Type) types.Type {
		t = types.Substitute(t, sub)
		if ifaceSym != nil && concrete != nil {
			t = substituteSelf(t, ifaceSym, concrete)
		}
		return t
	}
	for i, p := range fn.Params {
		out.Params[i] = rewrite(p)
	}
	out.Return = rewrite(fn.Return)
	return out
}

// requiredMethod pairs a method the satisfying type must provide with
// the interface that demanded it; ifaceSym is needed so substituteSelf
// can rewrite the right `Self` occurrences when comparing signatures.
type requiredMethod struct {
	md       *methodDesc
	ifaceSym *resolve.Symbol
}

// flattenInterface walks the §2.6.1 composition chain and returns:
//   - required: name → requiredMethod, the union of every reachable
//     interface's own methods. Composition uses first-wins on name
//     collisions (the iface walked earlier owns the slot); the spec
//     does not define cross-iface signature conflict resolution and
//     none arises in practice on the prelude shapes.
//   - ifaceSubs: map from each visited interface symbol to its
//     generic-arg substitution map; only populated for ifaces with
//     non-empty generics so the common case carries no entry.
//   - ok: false iff the root interface has no descriptor.
func (c *checker) flattenInterface(iface *types.Named) (map[string]requiredMethod, map[*resolve.Symbol]map[*resolve.Symbol]types.Type, bool) {
	if iface == nil || iface.Sym == nil {
		return nil, nil, false
	}
	ifDesc, ok := c.result.Descs[iface.Sym]
	if !ok || ifDesc.Kind != resolve.SymInterface {
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
		if len(desc.Generics) > 0 {
			ifaceSubs[n.Sym] = bindArgs(desc.Generics, n.Args)
		}
		for name, md := range desc.InterfaceMethods {
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

// mergeSubs unions two substitution maps. The two callers (concrete-
// owner generics and iface-owner generics) bind disjoint key sets, so
// the merge is just a shallow union; if it ever weren't, b would win.
// Returns the input directly when one side is empty.
func mergeSubs(a, b map[*resolve.Symbol]types.Type) map[*resolve.Symbol]types.Type {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
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

// methodSignaturesMatch compares an interface signature against a
// concrete method's, after applying generic substitutions and
// rewriting `Self` (resolver-bound to ifaceSym) to concreteSelf. Both
// signatures exclude the receiver, so it's a straight param/return
// comparison post-substitution.
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

// substituteSelf rewrites Named{Sym: ifaceSym} occurrences in t to
// `concrete` (per §2.6.3) and leaves any other Named intact so a
// signature returning a distinct named type isn't accidentally
// rewritten. Descends through Optional/Tuple/FnType/Named args.
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

// hasToStringV is the recursive worker. `seen` is mutated as it
// descends into user types and restored on return so cycles in
// recursive types (`struct Node<T> { next: Node<T>? }`) terminate.
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
		if seen[v.Sym] {
			return true
		}
		// User-provided toString overrides auto-derivation.
		if md, has := desc.Methods["toString"]; has && md.Fn != nil &&
			len(md.Fn.Params) == 0 && types.Identical(md.Fn.Return, types.String) {
			return true
		}
		seen[v.Sym] = true
		defer delete(seen, v.Sym)
		sub := bindArgs(desc.Generics, v.Args)
		switch desc.Kind {
		case resolve.SymStruct:
			for _, f := range desc.Fields {
				ft := f.Type
				if len(sub) > 0 {
					ft = types.Substitute(ft, sub)
				}
				if !hasToStringV(c, ft, seen) {
					return false
				}
			}
			return true
		case resolve.SymEnum:
			for _, vd := range desc.Variants {
				for _, ft := range vd.Fields {
					x := ft
					if len(sub) > 0 {
						x = types.Substitute(x, sub)
					}
					if !hasToStringV(c, x, seen) {
						return false
					}
				}
			}
			return true
		case resolve.SymTypeAlias:
			if desc.Alias != nil {
				return hasToStringV(c, desc.Alias, seen)
			}
			return true
		}
		// Interfaces (vtable dispatch) and other named shapes default true.
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

