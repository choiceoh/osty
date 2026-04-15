package check

import (
	"sort"

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
	if iface.Sym.Name == "Hashable" {
		if c.typeIsHashable(concrete) {
			return true
		}
		if tv, ok := concrete.(*types.TypeVar); ok {
			c.errNode(pos, diag.CodeTypeMismatch,
				"type parameter `%s` is not known to implement `Hashable`", tv)
			return false
		}
		c.errNode(pos, diag.CodeTypeMismatch,
			"type `%s` does not implement `Hashable`", concrete)
		return false
	}
	if tv, ok := concrete.(*types.TypeVar); ok {
		if c.typeVarKnownSatisfies(tv, iface) {
			return true
		}
		c.errNode(pos, diag.CodeTypeMismatch,
			"type parameter `%s` is not known to implement `%s`", tv, iface.Sym.Name)
		return false
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

	methods := c.interfaceMethodSet(iface)
	missing := []string{}
	for name, wantMd := range methods {
		gotMd, sub := c.lookupMethod(concrete, name)
		if gotMd == nil {
			missing = append(missing, name)
			continue
		}
		if !methodSignaturesMatch(wantMd, gotMd, concrete, sub) {
			c.errNode(pos, diag.CodeTypeMismatch,
				"type `%s` does not satisfy `%s`: method `%s` has a different signature",
				concrete, iface.Sym.Name, name)
			return false
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		c.errNode(pos, diag.CodeTypeMismatch,
			"type `%s` does not implement `%s`: missing method(s): %s",
			concrete, iface.Sym.Name, joinMethods(missing))
		return false
	}
	return true
}

func (c *checker) typeVarKnownSatisfies(tv *types.TypeVar, iface *types.Named) bool {
	if tv == nil || iface == nil || iface.Sym == nil {
		return true
	}
	for _, b := range tv.Bounds {
		n, ok := b.(*types.Named)
		if !ok || n.Sym == nil {
			continue
		}
		if c.interfaceImplies(n, iface) {
			return true
		}
	}
	return false
}

func (c *checker) typeIsHashable(t types.Type) bool {
	switch v := t.(type) {
	case *types.Primitive:
		return v.Kind.IsEqual() && !v.Kind.IsFloat()
	case *types.Untyped:
		return c.typeIsHashable(v.Default())
	case *types.Optional:
		return c.typeIsHashable(v.Inner)
	case *types.Tuple:
		for _, e := range v.Elems {
			if !c.typeIsHashable(e) {
				return false
			}
		}
		return true
	case *types.TypeVar:
		return c.typeVarKnownSatisfies(v, c.hashableInterface())
	case *types.Named:
		if v.Sym == nil {
			return false
		}
		switch v.Sym.Name {
		case "List", "Map", "Set", "Result", "Chan", "Channel":
			return false
		case "Hashable":
			return true
		}
		if desc, ok := c.result.Descs[v.Sym]; ok {
			if desc.Kind == resolve.SymTypeAlias && desc.Alias != nil {
				return c.typeIsHashable(types.Substitute(desc.Alias, bindArgs(desc.Generics, v.Args)))
			}
			if desc.Kind == resolve.SymInterface {
				return c.interfaceImplies(v, c.hashableInterface())
			}
		}
		return c.hasHashableMethods(v)
	}
	return false
}

func (c *checker) hashableInterface() *types.Named {
	if sym := c.lookupBuiltin("Hashable"); sym != nil {
		return &types.Named{Sym: sym}
	}
	return nil
}

func (c *checker) hasHashableMethods(t types.Type) bool {
	hash, hashSub := c.lookupMethod(t, "hash")
	if hash == nil || hash.Fn == nil {
		return false
	}
	hashFn := specializeMethodDesc(hash, hashSub).Fn
	if len(hashFn.Params) != 0 || !types.Identical(hashFn.Return, types.Int) {
		return false
	}
	eq, eqSub := c.lookupMethod(t, "eq")
	if eq == nil || eq.Fn == nil {
		return false
	}
	eqFn := specializeMethodDesc(eq, eqSub).Fn
	return len(eqFn.Params) == 1 &&
		types.Identical(eqFn.Params[0], t) &&
		types.Identical(eqFn.Return, types.Bool)
}

func (c *checker) requireHashable(t types.Type, pos ast.Node, role string) {
	if t == nil || types.IsError(t) || c.typeIsHashable(t) {
		return
	}
	if tv, ok := t.(*types.TypeVar); ok {
		c.errNode(pos, diag.CodeTypeMismatch,
			"%s type parameter `%s` is not known to implement `Hashable`", role, tv)
		return
	}
	c.errNode(pos, diag.CodeTypeMismatch,
		"%s type `%s` must implement `Hashable`", role, t)
}

func (c *checker) interfaceImplies(have, want *types.Named) bool {
	if have == nil || want == nil || have.Sym == nil || want.Sym == nil {
		return false
	}
	if types.Identical(have, want) {
		return true
	}
	if want.Sym.Name == "Equal" && (have.Sym.Name == "Ordered" || have.Sym.Name == "Hashable") {
		return true
	}
	haveMethods := c.interfaceMethodSet(have)
	wantMethods := c.interfaceMethodSet(want)
	if len(wantMethods) == 0 {
		return false
	}
	for name, wantMd := range wantMethods {
		haveMd, ok := haveMethods[name]
		if !ok {
			return false
		}
		if !methodSignaturesMatch(wantMd, haveMd, have, nil) {
			return false
		}
	}
	return true
}

// interfaceMethodSet returns every method required by iface, including
// methods inherited through interface composition. Generic arguments on
// composed interfaces are substituted before the method is exposed.
func (c *checker) interfaceMethodSet(iface *types.Named) map[string]*methodDesc {
	out := map[string]*methodDesc{}
	c.interfaceMethodsInto(iface, out, map[*resolve.Symbol]bool{})
	return out
}

func (c *checker) interfaceMethodsInto(
	iface *types.Named,
	out map[string]*methodDesc,
	visiting map[*resolve.Symbol]bool,
) {
	if iface == nil || iface.Sym == nil {
		return
	}
	if visiting[iface.Sym] {
		return
	}
	desc, ok := c.result.Descs[iface.Sym]
	if !ok || desc.Kind != resolve.SymInterface {
		return
	}
	visiting[iface.Sym] = true
	defer delete(visiting, iface.Sym)

	sub := types.BindArgs(desc.Generics, iface.Args)
	for _, ext := range desc.InterfaceExtends {
		extT := types.Substitute(ext, sub)
		if extN, ok := types.AsNamed(extT); ok {
			c.interfaceMethodsInto(extN, out, visiting)
		}
	}
	for name, md := range desc.InterfaceMethods {
		out[name] = specializeMethodDesc(md, sub)
	}
}

func specializeMethodDesc(md *methodDesc, sub map[*resolve.Symbol]types.Type) *methodDesc {
	if md == nil || len(sub) == 0 || md.Fn == nil {
		return md
	}
	fn := &types.FnType{
		Params: make([]types.Type, len(md.Fn.Params)),
		Return: types.Substitute(md.Fn.Return, sub),
	}
	for i, p := range md.Fn.Params {
		fn.Params[i] = types.Substitute(p, sub)
	}
	cp := *md
	cp.Fn = fn
	return &cp
}

// methodSignaturesMatch compares the declared interface signature with
// a concrete method signature after two substitutions:
//   - interface `Self` becomes the concrete receiver type;
//   - concrete receiver generics become the receiver's actual args.
//
// Both signatures exclude `self`, so the final comparison is a straight
// parameter-list + return comparison.
func methodSignaturesMatch(
	wantMd, gotMd *methodDesc,
	concrete types.Type,
	gotSub map[*resolve.Symbol]types.Type,
) bool {
	if wantMd == nil || gotMd == nil {
		return wantMd == gotMd
	}
	want := wantMd.Fn
	got := gotMd.Fn
	if want == nil || got == nil {
		return want == got
	}
	if wantMd.Owner != nil && wantMd.Owner.Sym != nil {
		want = substituteSelfInFn(want, wantMd.Owner.Sym, concrete)
	}
	if len(gotSub) > 0 {
		got = &types.FnType{
			Params: make([]types.Type, len(got.Params)),
			Return: types.Substitute(got.Return, gotSub),
		}
		for i, p := range gotMd.Fn.Params {
			got.Params[i] = types.Substitute(p, gotSub)
		}
	}
	if len(want.Params) != len(got.Params) {
		return false
	}
	for i, p := range want.Params {
		if !types.Identical(p, got.Params[i]) {
			return false
		}
	}
	return types.Identical(want.Return, got.Return)
}

func substituteSelfInFn(fn *types.FnType, selfSym *resolve.Symbol, concrete types.Type) *types.FnType {
	if fn == nil {
		return nil
	}
	out := &types.FnType{
		Params: make([]types.Type, len(fn.Params)),
		Return: substituteSelfType(fn.Return, selfSym, concrete),
	}
	for i, p := range fn.Params {
		out.Params[i] = substituteSelfType(p, selfSym, concrete)
	}
	return out
}

func substituteSelfType(t types.Type, selfSym *resolve.Symbol, concrete types.Type) types.Type {
	if t == nil || selfSym == nil || concrete == nil {
		return t
	}
	switch x := t.(type) {
	case *types.Named:
		if x.Sym == selfSym {
			return concrete
		}
		if len(x.Args) == 0 {
			return x
		}
		args := make([]types.Type, len(x.Args))
		for i, a := range x.Args {
			args[i] = substituteSelfType(a, selfSym, concrete)
		}
		return &types.Named{Sym: x.Sym, Args: args}
	case *types.Optional:
		return &types.Optional{Inner: substituteSelfType(x.Inner, selfSym, concrete)}
	case *types.Tuple:
		elems := make([]types.Type, len(x.Elems))
		for i, e := range x.Elems {
			elems[i] = substituteSelfType(e, selfSym, concrete)
		}
		return &types.Tuple{Elems: elems}
	case *types.FnType:
		return substituteSelfInFn(x, selfSym, concrete)
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

// accepts is the checker-level assignment relation. It extends the
// pure types.Assignable relation with structural interface satisfaction
// because only the checker has access to method descriptors.
func (c *checker) accepts(dst, src types.Type, pos ast.Node) bool {
	if ifaceN, isIface := interfaceNamed(c, dst); isIface {
		return c.satisfies(src, ifaceN, pos)
	}
	return types.Assignable(dst, src)
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
