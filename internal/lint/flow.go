package lint

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
)

// Control-flow suggestions: rules that inspect block-level flow rather
// than single expressions.
//
//   L0021  redundant `else` after an unconditional `return`
//   L0024  needless `return` at the tail of a function body
//   L0026  empty loop body

// lintFlow runs L0021 / L0024 / L0026 over every function body in the file.
func (l *linter) lintFlow() {
	for _, d := range l.file.Decls {
		l.flowDecl(d)
	}
	// Script top-level statements are the implicit main body; treat
	// them as a synthetic block.
	if len(l.file.Stmts) > 0 {
		l.flowStmts(l.file.Stmts, true /* is tail: script-level */)
	}
}

func (l *linter) flowDecl(d ast.Decl) {
	switch n := d.(type) {
	case *ast.FnDecl:
		if n.Body != nil {
			l.flowStmts(n.Body.Stmts, true)
		}
	case *ast.StructDecl:
		for _, m := range n.Methods {
			if m.Body != nil {
				l.flowStmts(m.Body.Stmts, true)
			}
		}
	case *ast.EnumDecl:
		for _, m := range n.Methods {
			if m.Body != nil {
				l.flowStmts(m.Body.Stmts, true)
			}
		}
	case *ast.InterfaceDecl:
		for _, m := range n.Methods {
			if m.Body != nil {
				l.flowStmts(m.Body.Stmts, true)
			}
		}
	}
}

// flowStmts inspects a block's statements for the three control-flow
// rules. `isFnTail` is true when the block is directly a function body
// (so the final return / expression is the function's result).
func (l *linter) flowStmts(stmts []ast.Stmt, isFnTail bool) {
	// ---- L0024: needless `return` at tail.
	if isFnTail && len(stmts) > 0 {
		last := stmts[len(stmts)-1]
		if ret, ok := last.(*ast.ReturnStmt); ok && ret.Value != nil {
			l.emit(diag.New(diag.Warning,
				"needless `return` at end of function body").
				Code(diag.CodeNeedlessReturn).
				Primary(diag.Span{Start: ret.PosV, End: ret.EndV},
					"drop `return`, the bare expression is the result").
				Hint("remove the `return` keyword — Osty uses tail-expression returns").
				Build())
		}
	}

	// Recurse into nested blocks and flag redundant else / empty loop.
	for _, s := range stmts {
		l.flowStmt(s)
	}

	// ---- L0021: redundant `else` after an unconditional return.
	// Pattern:
	//   if cond { ... return ... } else { bodyB }
	// means bodyB is only reached when cond is false — hoisting it is
	// always valid and clearer.
	for i, s := range stmts {
		ex, ok := s.(*ast.ExprStmt)
		if !ok {
			continue
		}
		ifExpr, ok := ex.X.(*ast.IfExpr)
		if !ok || ifExpr.IsIfLet {
			continue
		}
		if ifExpr.Then == nil || !lastStmtIsReturn(ifExpr.Then.Stmts) {
			continue
		}
		elseBlock, ok := ifExpr.Else.(*ast.Block)
		if !ok || elseBlock == nil {
			continue
		}
		// If this is not the last statement, hoisting would conflict
		// with subsequent siblings. Only flag when it IS the tail of the
		// containing block — that's the clippy `needless_else` shape.
		if i != len(stmts)-1 {
			continue
		}
		l.emit(diag.New(diag.Warning,
			"redundant `else` — the `if` branch always returns").
			Code(diag.CodeRedundantElse).
			Primary(diag.Span{Start: elseBlock.PosV, End: elseBlock.EndV},
				"this `else` can be removed").
			Hint("drop the `else` and hoist its body one level up").
			Build())
	}
}

func (l *linter) flowStmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.ForStmt:
		// ---- L0026: empty loop body.
		if n.Body != nil && len(n.Body.Stmts) == 0 {
			l.emit(diag.New(diag.Warning,
				"empty loop body").
				Code(diag.CodeEmptyLoopBody).
				Primary(diag.Span{Start: n.PosV, End: n.EndV},
					"loop has no body").
				Hint("fill in the loop body, or remove the loop entirely").
				Build())
		}
		if n.Body != nil {
			l.flowStmts(n.Body.Stmts, false)
		}
	case *ast.ExprStmt:
		l.flowExpr(n.X)
	case *ast.LetStmt:
		l.flowExpr(n.Value)
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			l.flowExpr(t)
		}
		l.flowExpr(n.Value)
	case *ast.ReturnStmt:
		l.flowExpr(n.Value)
	case *ast.DeferStmt:
		l.flowExpr(n.X)
	case *ast.Block:
		l.flowStmts(n.Stmts, false)
	}
}

func (l *linter) flowExpr(e ast.Expr) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.Block:
		l.flowStmts(n.Stmts, false)
	case *ast.IfExpr:
		l.flowExpr(n.Cond)
		if n.Then != nil {
			l.flowStmts(n.Then.Stmts, false)
		}
		l.flowExpr(n.Else)
	case *ast.MatchExpr:
		l.flowExpr(n.Scrutinee)
		for _, arm := range n.Arms {
			l.flowExpr(arm.Guard)
			l.flowExpr(arm.Body)
		}
	case *ast.ClosureExpr:
		// The closure body has its own tail-return context.
		if b, ok := n.Body.(*ast.Block); ok {
			l.flowStmts(b.Stmts, true)
		} else {
			l.flowExpr(n.Body)
		}
	}
}

// lastStmtIsReturn reports whether the statement list ends with a bare
// return (possibly inside nested blocks/ifs that all exit the enclosing
// function). v1 scope: only a direct trailing `*ast.ReturnStmt`. Lift
// later if we want to match clippy's more thorough detection.
func lastStmtIsReturn(stmts []ast.Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	_, ok := stmts[len(stmts)-1].(*ast.ReturnStmt)
	return ok
}
