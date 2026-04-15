package check

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/types"
)

// applyDeclaredCall is the keyword-aware / default-aware call
// application. Given the full param list from the declaration (names +
// default markers), it matches positional and keyword arguments to
// parameters, validates arity accounting for defaults, and dispatches
// to applyGenericCallWithArgs for the positional check + generic
// inference.
//
// Rules per §3.1:
//   - Positional args must precede every keyword arg.
//   - A parameter may be supplied at most once.
//   - Unknown keyword names are rejected (E0732).
//   - A parameter without a supplied argument or default value is
//     rejected (E0701).
func (c *checker) applyDeclaredCall(
	e *ast.CallExpr, fn *types.FnType, generics []*types.TypeVar,
	params []*ast.Param, hint types.Type, env *env,
) types.Type {
	return c.applyDeclaredCallWithExplicit(e, fn, generics, params, nil, hint, env)
}

func (c *checker) applyDeclaredCallWithExplicit(
	e *ast.CallExpr, fn *types.FnType, generics []*types.TypeVar,
	params []*ast.Param, explicit []types.Type, hint types.Type, env *env,
) types.Type {
	// Fast path: no param metadata → fall back to the positional-only
	// generic-aware path.
	if len(params) == 0 {
		return c.applyGenericCallWithArgs(e, fn, generics, explicit, hint, env)
	}

	// Walk the surface-call arguments once to classify positional vs
	// keyword and validate ordering.
	resolved := make([]*ast.Arg, len(params))
	seenKeyword := false
	for i, a := range e.Args {
		if a.Name == "" { // positional
			if seenKeyword {
				c.errNode(a.Value, diag.CodePositionalAfterKw,
					"positional argument after keyword argument")
				continue
			}
			if i >= len(params) {
				c.errNode(a.Value, diag.CodeWrongArgCount,
					"too many arguments: expected %d, got %d",
					len(params), len(e.Args))
				continue
			}
			resolved[i] = a
			continue
		}
		seenKeyword = true
		idx := paramIndex(params, a.Name)
		if idx < 0 {
			c.errNode(a.Value, diag.CodeKeywordArgUnknown,
				"no parameter `%s` on this call", a.Name)
			continue
		}
		if resolved[idx] != nil {
			c.errNode(a.Value, diag.CodeDuplicateArg,
				"parameter `%s` is supplied more than once", a.Name)
			continue
		}
		resolved[idx] = a
	}

	// Every param without a default must have been supplied.
	for i, p := range params {
		if resolved[i] != nil || p.Default != nil {
			continue
		}
		c.errNode(e, diag.CodeWrongArgCount,
			"missing argument for parameter `%s`", p.Name)
	}

	// Now drive the generic-aware positional check using `resolved` as
	// the effective argument list. Missing slots (defaults) are filled
	// with nil and skipped.
	return c.applyGenericCallResolved(e, fn, generics, params, resolved, explicit, hint, env)
}

// applyGenericCallResolved mirrors applyGenericCallWithArgs but pulls
// arguments from a pre-ordered slice (positional + keyword already
// matched to their parameter slots). nil slots correspond to defaults
// that weren't passed — they are skipped but the parameter's declared
// type still participates in return-type inference.
func (c *checker) applyGenericCallResolved(
	e *ast.CallExpr, fn *types.FnType, generics []*types.TypeVar,
	params []*ast.Param, resolved []*ast.Arg, explicit []types.Type, hint types.Type, env *env,
) types.Type {
	c.checkExplicitGenericArity(e, len(generics), explicit)
	if len(generics) == 0 {
		// Simple type-check loop without substitution work.
		for i, a := range resolved {
			if a == nil || i >= len(fn.Params) {
				continue
			}
			pt := fn.Params[i]
			at := c.checkExpr(a.Value, pt, env)
			if pt != nil && !types.IsError(pt) && !c.accepts(pt, at, a.Value) {
				c.errMismatch(a.Value, pt, at)
			}
		}
		return fn.Return
	}

	sub := make(map[*resolve.Symbol]types.Type, len(generics))
	for i, g := range generics {
		if i < len(explicit) {
			sub[g.Sym] = explicit[i]
		}
	}
	if hint != nil && !types.IsError(hint) {
		inferFromArg(fn.Return, hint, sub)
	}
	// First pass: check each supplied argument with the current
	// substitution as hint; update sub from the concrete type.
	for i, a := range resolved {
		if a == nil || i >= len(fn.Params) {
			continue
		}
		pt := types.Substitute(fn.Params[i], sub)
		at := c.checkExpr(a.Value, pt, env)
		inferFromArg(fn.Params[i], at, sub)
	}
	// Default-untyped generics: if inference landed on Untyped, settle
	// on its default concrete type.
	for _, g := range generics {
		if t, have := sub[g.Sym]; have {
			if u, ok := t.(*types.Untyped); ok {
				sub[g.Sym] = u.Default()
			}
		}
	}
	// Second pass: verify every supplied argument against the
	// now-concrete param type.
	for i, a := range resolved {
		if a == nil || i >= len(fn.Params) {
			continue
		}
		pt := types.Substitute(fn.Params[i], sub)
		at := c.result.Types[a.Value]
		if pt != nil && !types.IsError(pt) && at != nil && !c.accepts(pt, at, a.Value) {
			c.errMismatch(a.Value, pt, at)
		}
	}

	// Record the concrete instantiation.
	instArgs := make([]types.Type, len(generics))
	for i, g := range generics {
		if t, ok := sub[g.Sym]; ok && t != nil {
			instArgs[i] = t
		} else {
			instArgs[i] = g
		}
	}
	c.result.Instantiations[e] = instArgs
	for i, g := range generics {
		c.checkBounds(g, instArgs[i], e)
	}
	_ = params
	return types.Substitute(fn.Return, sub)
}

// paramIndex returns the index of `name` in params or -1 when absent.
func paramIndex(params []*ast.Param, name string) int {
	for i, p := range params {
		if p.Name == name {
			return i
		}
	}
	return -1
}
