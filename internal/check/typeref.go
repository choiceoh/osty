//go:build selfhostgen

package check

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/types"
)

// typeOf converts an AST type node to a semantic types.Type.
//
// The resolver has already attached Symbols to NamedType nodes via
// TypeRefs; this function uses those symbols to dispatch:
//
//   - prelude primitives ("Int", "Bool", ...) → *types.Primitive
//   - prelude generics ("List", "Option", "Result", ...) → *types.Named
//     with the type arguments populated
//   - generic parameters (SymGeneric) → *types.TypeVar
//   - user struct/enum/interface/alias → *types.Named
//
// Errors (unresolved head, wrong symbol kind) are already reported by
// the resolver; this function returns types.ErrorType so downstream
// cascade is suppressed.
func (c *checker) typeOf(n ast.Type) types.Type {
	if n == nil {
		return types.Unit
	}
	switch x := n.(type) {
	case *ast.NamedType:
		return c.namedType(x)
	case *ast.OptionalType:
		inner := c.typeOf(x.Inner)
		return &types.Optional{Inner: inner}
	case *ast.TupleType:
		if len(x.Elems) == 0 {
			return types.Unit
		}
		if len(x.Elems) == 1 {
			return c.typeOf(x.Elems[0])
		}
		elems := make([]types.Type, len(x.Elems))
		for i, e := range x.Elems {
			elems[i] = c.typeOf(e)
		}
		return &types.Tuple{Elems: elems}
	case *ast.FnType:
		params := make([]types.Type, len(x.Params))
		for i, p := range x.Params {
			params[i] = c.typeOf(p)
		}
		var ret types.Type = types.Unit
		if x.ReturnType != nil {
			ret = c.typeOf(x.ReturnType)
		}
		return &types.FnType{Params: params, Return: ret}
	}
	return types.ErrorType
}

// namedType converts a NamedType to a semantic type using the resolver's
// symbol. `Self` was already pre-resolved by the resolver to point at
// the enclosing type's symbol.
func (c *checker) namedType(n *ast.NamedType) types.Type {
	sym := c.namedSymbol(n)
	if sym == nil {
		return types.ErrorType
	}

	// Case 1: a generic parameter.
	if sym.Kind == resolve.SymGeneric {
		// Recover the TypeVar created in pass 1 when the enclosing
		// declaration was collected.
		if i, ok := c.syms[sym]; ok && i.Type != nil {
			return i.Type
		}
		// Fresh TypeVar if we haven't built one yet (e.g. a direct
		// annotation in a nested context).
		return &types.TypeVar{Sym: sym}
	}

	// Case 2: a prelude builtin scalar.
	if sym.Kind == resolve.SymBuiltin {
		if canonical := c.lookupBuiltin(sym.Name); canonical != nil {
			sym = canonical
		}
		if t, ok := c.builtinScalarType(sym.Name); ok {
			if len(n.Args) != 0 {
				c.errNode(n, diag.CodeGenericArgCount,
					"`%s` does not take type arguments", sym.Name)
			}
			return t
		}
		// Generic prelude types.
		return c.builtinGenericType(n, sym)
	}

	// Case 3: user struct / enum / interface / type alias.
	args := make([]types.Type, 0, len(n.Args))
	for _, a := range n.Args {
		args = append(args, c.typeOf(a))
	}

	// The std.option module declares the canonical Option<T> enum, but
	// the checker represents all Option<T> spellings as Optional so
	// `option.Option<Int>` and `Int?` remain fully interchangeable.
	if sym.Name == "Option" {
		if len(args) != 1 {
			c.errNode(n, diag.CodeGenericArgCount,
				"`Option` expects exactly 1 type argument, got %d", len(args))
			return types.ErrorType
		}
		return &types.Optional{Inner: args[0]}
	}

	// Expand type aliases transparently (§3.7).
	if desc, ok := c.result.Descs[sym]; ok && desc.Kind == resolve.SymTypeAlias {
		if desc.Alias != nil && len(args) == 0 {
			return desc.Alias
		}
		// Parameterized alias: apply args by substituting into the target.
		if desc.Alias != nil {
			return substituteTypeVars(desc.Alias, bindArgs(desc.Generics, args))
		}
	}

	// Check generic arity when we know the declaration's arity.
	if desc, ok := c.result.Descs[sym]; ok && desc.Kind != resolve.SymTypeAlias {
		if want, got := len(desc.Generics), len(args); want != got {
			c.errNode(n, diag.CodeGenericArgCount,
				"type `%s` expects %d type argument(s), got %d",
				sym.Name, want, got)
		}
	}
	c.checkCollectionHashableArgs(n, sym.Name, args)
	return &types.Named{Sym: sym, Args: args}
}

// builtinScalarType returns the Primitive singleton for a prelude scalar
// type name (shared with initBuiltins), or (nil, false) for non-scalars.
func (c *checker) builtinScalarType(name string) (types.Type, bool) {
	t, ok := scalarByName[name]
	return t, ok
}

// builtinTypeArity is the expected type-argument count for every
// non-scalar prelude name that is valid in type position.
var builtinTypeArity = map[string]int{
	"List":      1,
	"Set":       1,
	"Map":       2,
	"Result":    2,
	"Chan":      1,
	"Channel":   1,
	"Handle":    1,
	"TaskGroup": 0,
	"Error":     0,
	"Equal":     0,
	"Ordered":   0,
	"Hashable":  0,
	"Option":    1,
}

// builtinGenericType handles List<T>, Map<K,V>, Set<T>, Option<T>,
// Result<T, E>, Error, and the three built-in marker interfaces.
func (c *checker) builtinGenericType(n *ast.NamedType, sym *resolve.Symbol) types.Type {
	args := make([]types.Type, 0, len(n.Args))
	for _, a := range n.Args {
		args = append(args, c.typeOf(a))
	}

	want, ok := builtinTypeArity[sym.Name]
	if !ok {
		c.errNode(n, diag.CodeWrongSymbolKind,
			"`%s` is a builtin value, not a type", sym.Name)
		return types.ErrorType
	}
	if want != len(args) {
		c.errNode(n, diag.CodeGenericArgCount,
			"`%s` expects %d type argument(s), got %d", sym.Name, want, len(args))
		return types.ErrorType
	}

	// Option<T> is just Optional(T) — canonicalize at the boundary so
	// the rest of the checker only sees one form.
	if sym.Name == "Option" {
		return &types.Optional{Inner: args[0]}
	}

	c.checkCollectionHashableArgs(n, sym.Name, args)
	return &types.Named{Sym: sym, Args: args}
}

func (c *checker) checkCollectionHashableArgs(n *ast.NamedType, name string, args []types.Type) {
	switch name {
	case "Map":
		if len(args) >= 1 {
			pos := ast.Node(n)
			if len(n.Args) >= 1 {
				pos = n.Args[0]
			}
			c.requireHashable(args[0], pos, "Map key")
		}
	case "Set":
		if len(args) >= 1 {
			pos := ast.Node(n)
			if len(n.Args) >= 1 {
				pos = n.Args[0]
			}
			c.requireHashable(args[0], pos, "Set element")
		}
	}
}

// bindArgs / substituteTypeVars are aliases so the checker reads
// naturally; the implementations live in the types package where they
// are reusable by future passes (monomorphization, interface solve).
var (
	bindArgs           = types.BindArgs
	substituteTypeVars = types.Substitute
)
