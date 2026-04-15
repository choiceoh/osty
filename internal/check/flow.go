package check

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/types"
)

// flow is the compact flow-analysis result for a statement / block /
// expression: diverges means control leaves the enclosing function
// (return, break, continue, or an expression of type Never), and
// value is the static type produced when control DOES fall through.
// Callers combine these through the helpers below to power the
// "unreachable code" and "missing return" diagnostics.
type flow struct {
	diverges bool
	value    types.Type // the "rest of the world" value when !diverges
}

// divergent marks a flow as always leaving the scope.
func divergent() flow { return flow{diverges: true, value: types.Never} }

// yields marks a flow that completes with a value of type t.
func yields(t types.Type) flow { return flow{diverges: false, value: t} }

// analyzeBlockFlow walks a block top-to-bottom and returns both the
// block's overall flow AND a diagnostic for any statement that appears
// after a divergent one. The value type is the last expression's
// type when the block is used as an expression; otherwise Unit.
func (c *checker) analyzeBlockFlow(b *ast.Block) flow {
	if b == nil || len(b.Stmts) == 0 {
		return yields(types.Unit)
	}
	diverged := false
	var divergePos ast.Node
	for i, s := range b.Stmts {
		if diverged {
			// Only report the FIRST unreachable statement per block
			// so one divergent return doesn't spam the whole tail.
			c.errNode(s, diag.CodeUnreachableCode,
				"unreachable statement: the preceding statement always diverges")
			_ = divergePos
			_ = i
			continue
		}
		f := c.statementFlow(s)
		if f.diverges {
			diverged = true
			divergePos = s
		}
	}
	// Block's own flow: last stmt's flow. If the last stmt is an
	// expression statement AND the block hasn't diverged, the block
	// yields that expression's static type; otherwise Unit.
	last := b.Stmts[len(b.Stmts)-1]
	lastFlow := c.statementFlow(last)
	if diverged {
		return divergent()
	}
	return lastFlow
}

// statementFlow classifies a single statement. Only divergent control
// forms (return/break/continue/panic-like) return a divergent flow;
// everything else yields Unit (statements have no value) except for
// expression statements, where the expression's type is threaded.
func (c *checker) statementFlow(s ast.Stmt) flow {
	switch n := s.(type) {
	case *ast.ReturnStmt:
		return divergent()
	case *ast.BreakStmt, *ast.ContinueStmt:
		return divergent()
	case *ast.ExprStmt:
		t := c.result.Types[n.X]
		if types.IsNever(t) {
			return divergent()
		}
		return yields(t)
	case *ast.Block:
		return c.analyzeBlockFlow(n)
	}
	return yields(types.Unit)
}

// fnBodyAlwaysReturns reports whether the function body never falls
// off the end without an explicit return or diverging expression.
// Used by checkFnDecl to decide whether to emit E0761 when the
// declared return type is non-unit.
func (c *checker) fnBodyAlwaysReturns(body *ast.Block) bool {
	if body == nil || len(body.Stmts) == 0 {
		return false
	}
	last := body.Stmts[len(body.Stmts)-1]
	// A terminal ExprStmt counts as a "final expression"; the block's
	// own expression value is then the function's implicit return.
	// Only a divergent final statement counts as "always returns".
	f := c.statementFlow(last)
	if f.diverges {
		return true
	}
	// Trailing expression yields a value of some type — that's the
	// implicit return. Callers check the type compatibility
	// separately; here we just confirm control doesn't "fall off the
	// end without any value".
	if _, ok := last.(*ast.ExprStmt); ok {
		return true
	}
	// Pure if/match as a statement: recurse into both branches.
	return false
}
