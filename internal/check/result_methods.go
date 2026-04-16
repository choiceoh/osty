//go:build selfhostgen

package check

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/types"
)

func (c *checker) resultMethodFromStdlib(n *types.Named, name string) *methodDesc {
	if len(c.resultMethods) == 0 || n == nil || len(n.Args) != 2 {
		return nil
	}
	fn := c.resultMethods[name]
	if fn == nil {
		return nil
	}
	conv := resultMethodConverter{
		checker: c,
		owner: map[string]types.Type{
			"T": n.Args[0],
			"E": n.Args[1],
		},
		method: map[string]*types.TypeVar{},
	}
	generics := make([]*types.TypeVar, 0, len(fn.Generics))
	for _, g := range fn.Generics {
		tv := &types.TypeVar{Sym: &resolve.Symbol{Name: g.Name, Kind: resolve.SymGeneric}}
		for _, b := range g.Constraints {
			tv.Bounds = append(tv.Bounds, conv.typ(b))
		}
		conv.method[g.Name] = tv
		generics = append(generics, tv)
	}
	params := make([]types.Type, 0, len(fn.Params))
	for _, p := range fn.Params {
		params = append(params, conv.typ(p.Type))
	}
	ret := types.Type(types.Unit)
	if fn.ReturnType != nil {
		ret = conv.typ(fn.ReturnType)
	}
	return &methodDesc{
		Name:     fn.Name,
		Pub:      fn.Pub,
		Recv:     fn.Recv,
		Fn:       &types.FnType{Params: params, Return: ret},
		HasBody:  fn.Body != nil,
		Params:   fn.Params,
		Decl:     fn,
		Generics: generics,
	}
}

type resultMethodConverter struct {
	checker *checker
	owner   map[string]types.Type
	method  map[string]*types.TypeVar
}

func (c resultMethodConverter) typ(t ast.Type) types.Type {
	if t == nil {
		return types.Unit
	}
	switch x := t.(type) {
	case *ast.NamedType:
		if len(x.Path) == 1 {
			name := x.Path[0]
			if t, ok := c.owner[name]; ok && len(x.Args) == 0 {
				return t
			}
			if tv, ok := c.method[name]; ok && len(x.Args) == 0 {
				return tv
			}
			if scalar, ok := c.checker.builtinScalarType(name); ok && len(x.Args) == 0 {
				return scalar
			}
			args := make([]types.Type, len(x.Args))
			for i, a := range x.Args {
				args[i] = c.typ(a)
			}
			return &types.Named{Sym: c.builtinSym(name), Args: args}
		}
		return types.ErrorType
	case *ast.OptionalType:
		return &types.Optional{Inner: c.typ(x.Inner)}
	case *ast.TupleType:
		if len(x.Elems) == 0 {
			return types.Unit
		}
		elems := make([]types.Type, len(x.Elems))
		for i, e := range x.Elems {
			elems[i] = c.typ(e)
		}
		return &types.Tuple{Elems: elems}
	case *ast.FnType:
		params := make([]types.Type, len(x.Params))
		for i, p := range x.Params {
			params[i] = c.typ(p)
		}
		ret := types.Type(types.Unit)
		if x.ReturnType != nil {
			ret = c.typ(x.ReturnType)
		}
		return &types.FnType{Params: params, Return: ret}
	}
	return types.ErrorType
}

func (c resultMethodConverter) builtinSym(name string) *resolve.Symbol {
	if sym := c.checker.lookupBuiltin(name); sym != nil {
		return sym
	}
	return syntheticBuiltinSym(name)
}
