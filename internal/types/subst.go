package types

import "github.com/osty/osty/internal/resolve"

// BindArgs pairs generic TypeVars with their concrete arguments. Short
// argument lists leave the tail unbound; empty inputs return nil so the
// caller can skip substitution work entirely.
func BindArgs(generics []*TypeVar, args []Type) map[*resolve.Symbol]Type {
	if len(generics) == 0 || len(args) == 0 {
		return nil
	}
	m := make(map[*resolve.Symbol]Type, len(generics))
	for i, g := range generics {
		if i >= len(args) {
			break
		}
		m[g.Sym] = args[i]
	}
	return m
}

// Substitute rewrites t by replacing each TypeVar whose Sym is in subs
// with the corresponding concrete type. Nested composites (Optional,
// Tuple, Fn, Named) are descended recursively. Returns t unchanged when
// subs is empty.
func Substitute(t Type, subs map[*resolve.Symbol]Type) Type {
	if t == nil || len(subs) == 0 {
		return t
	}
	switch x := t.(type) {
	case *TypeVar:
		if sub, ok := subs[x.Sym]; ok {
			return sub
		}
		return x
	case *Optional:
		return &Optional{Inner: Substitute(x.Inner, subs)}
	case *Tuple:
		elems := make([]Type, len(x.Elems))
		for i, e := range x.Elems {
			elems[i] = Substitute(e, subs)
		}
		return &Tuple{Elems: elems}
	case *FnType:
		params := make([]Type, len(x.Params))
		for i, p := range x.Params {
			params[i] = Substitute(p, subs)
		}
		return &FnType{Params: params, Return: Substitute(x.Return, subs)}
	case *Named:
		if len(x.Args) == 0 {
			return x
		}
		args := make([]Type, len(x.Args))
		for i, a := range x.Args {
			args[i] = Substitute(a, subs)
		}
		return &Named{Sym: x.Sym, Args: args}
	}
	return t
}
