package check

import (
	"github.com/osty/osty/internal/ast"
)

// isAllowedDefaultExpr enforces v0.3 §3.1's rule that default
// arguments must be simple literals. Allowed shapes:
//
//   - Numeric / string / char / byte / bool literals
//   - `None`
//   - `Ok(literal)` / `Err(literal)`
//   - Empty list `[]` and empty map `{:}`
//   - Unit `()`
//
// Anything else (function calls, identifier references, arithmetic,
// string interpolation, non-empty collection literals, struct literals)
// is rejected. The rule exists so default values are cheap to copy at
// each call site without re-running arbitrary code.
func isAllowedDefaultExpr(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.CharLit, *ast.ByteLit, *ast.BoolLit:
		return true
	case *ast.StringLit:
		// Plain string literals only — interpolation is computation.
		for _, p := range x.Parts {
			if !p.IsLit {
				return false
			}
		}
		return true
	case *ast.Ident:
		// The literal set includes `None` and the builtin booleans
		// `true`/`false` (handled by parser as BoolLit, but the
		// resolver occasionally re-wraps them as Idents in odd
		// contexts). Accept only these specific names.
		return x.Name == "None" || x.Name == "true" || x.Name == "false"
	case *ast.CallExpr:
		// `Ok(literal)` / `Err(literal)` / `Some(literal)` are the
		// allowed variant literals. The callee is an Ident naming the
		// variant; the single argument must itself be a literal.
		id, ok := x.Fn.(*ast.Ident)
		if !ok {
			return false
		}
		if id.Name != "Ok" && id.Name != "Err" && id.Name != "Some" {
			return false
		}
		if len(x.Args) != 1 {
			return false
		}
		return isAllowedDefaultExpr(x.Args[0].Value)
	case *ast.ListExpr:
		return len(x.Elems) == 0
	case *ast.MapExpr:
		return x.Empty || len(x.Entries) == 0
	case *ast.TupleExpr:
		return len(x.Elems) == 0
	case *ast.ParenExpr:
		return isAllowedDefaultExpr(x.X)
	case *ast.UnaryExpr:
		// Negative literal: `-42`, `-3.14`.
		if _, ok := x.X.(*ast.IntLit); ok {
			return true
		}
		if _, ok := x.X.(*ast.FloatLit); ok {
			return true
		}
		return false
	}
	return false
}
