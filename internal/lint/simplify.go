package lint

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
)

// lintSimplify runs three "common bug / redundant code" checks:
//
//   L0040  if-then-else that just returns a bool literal (`if c { true }
//          else { false }` ≡ `c`)
//   L0041  self-comparison (`x == x`, `x != x`, `x < x`, …) — almost
//          always a copy-paste bug
//   L0042  self-assignment (`x = x`) — a no-op
//
// All three are pure AST checks; only L0041 / L0042 consult the
// resolver's Refs map (via exprsEqual) to answer "is this the same
// binding on both sides?".
func (l *linter) lintSimplify() {
	for _, d := range l.file.Decls {
		l.simplifyDecl(d)
	}
	for _, s := range l.file.Stmts {
		l.simplifyStmt(s)
	}
}

func (l *linter) simplifyDecl(d ast.Decl) {
	switch n := d.(type) {
	case *ast.FnDecl:
		if n.Body != nil {
			l.simplifyBlock(n.Body)
		}
	case *ast.StructDecl:
		for _, m := range n.Methods {
			if m.Body != nil {
				l.simplifyBlock(m.Body)
			}
		}
	case *ast.EnumDecl:
		for _, m := range n.Methods {
			if m.Body != nil {
				l.simplifyBlock(m.Body)
			}
		}
	case *ast.InterfaceDecl:
		for _, m := range n.Methods {
			if m.Body != nil {
				l.simplifyBlock(m.Body)
			}
		}
	case *ast.LetDecl:
		l.simplifyExpr(n.Value)
	}
}

func (l *linter) simplifyBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		l.simplifyStmt(s)
	}
}

func (l *linter) simplifyStmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.LetStmt:
		l.simplifyExpr(n.Value)
	case *ast.ExprStmt:
		l.simplifyExpr(n.X)
	case *ast.AssignStmt:
		// ---- L0042: self-assign (`x = x`) — simple, single-target
		// form only. Compound forms like `x += x` are allowed (could be
		// a doubling).
		if n.Op == token.ASSIGN && len(n.Targets) == 1 {
			if exprsEqual(n.Targets[0], n.Value, l.resolved) {
				l.emit(diag.New(diag.Warning,
					"self-assignment has no effect").
					Code(diag.CodeSelfAssign).
					Primary(diag.Span{Start: n.PosV, End: n.EndV},
						"this assignment is a no-op").
					Hint("remove this statement, or use the correct right-hand side").
					Suggest(diag.Span{Start: n.PosV, End: n.EndV}, "",
						"delete this no-op statement", true).
					Build())
			}
		}
		for _, t := range n.Targets {
			l.simplifyExpr(t)
		}
		l.simplifyExpr(n.Value)
	case *ast.ReturnStmt:
		l.simplifyExpr(n.Value)
	case *ast.DeferStmt:
		l.simplifyExpr(n.X)
	case *ast.ForStmt:
		l.simplifyExpr(n.Iter)
		l.simplifyBlock(n.Body)
	case *ast.ChanSendStmt:
		l.simplifyExpr(n.Channel)
		l.simplifyExpr(n.Value)
	case *ast.Block:
		l.simplifyBlock(n)
	}
}

func (l *linter) simplifyExpr(e ast.Expr) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.Block:
		l.simplifyBlock(n)
	case *ast.IfExpr:
		// ---- L0040: redundant bool (`if c { true } else { false }`
		// / `if c { false } else { true }`). Only if both branches are
		// a single-stmt block holding a BoolLit.
		l.checkRedundantBool(n)
		l.checkConstantCondition(n)
		l.checkEmptyBranch(n)
		l.checkIdenticalBranches(n)
		l.simplifyExpr(n.Cond)
		l.simplifyBlock(n.Then)
		l.simplifyExpr(n.Else)
	case *ast.MatchExpr:
		l.simplifyExpr(n.Scrutinee)
		for _, arm := range n.Arms {
			l.simplifyExpr(arm.Guard)
			l.simplifyExpr(arm.Body)
		}
	case *ast.ClosureExpr:
		l.simplifyExpr(n.Body)
	case *ast.UnaryExpr:
		// ---- L0043: double negation `!!x`. The outer is UnaryExpr with
		// Op=NOT and X is another UnaryExpr with Op=NOT.
		l.checkDoubleNegation(n)
		// ---- L0045: negated bool literal `!true` / `!false`.
		l.checkNegatedBoolLiteral(n)
		l.simplifyExpr(n.X)
	case *ast.BinaryExpr:
		// ---- L0041: self-compare.
		if isComparisonOp(n.Op) && exprsEqual(n.Left, n.Right, l.resolved) {
			l.emit(diag.New(diag.Warning,
				"operand compared with itself").
				Code(diag.CodeSelfCompare).
				Primary(diag.Span{Start: n.PosV, End: n.EndV},
					"comparison always has the same result").
				Hint("replace with a constant, or compare against the intended value").
				Build())
		}
		// ---- L0044: comparison with bool literal.
		l.checkBoolLiteralCompare(n)
		l.simplifyExpr(n.Left)
		l.simplifyExpr(n.Right)
	case *ast.CallExpr:
		l.simplifyExpr(n.Fn)
		for _, a := range n.Args {
			l.simplifyExpr(a.Value)
		}
	case *ast.FieldExpr:
		l.simplifyExpr(n.X)
	case *ast.IndexExpr:
		l.simplifyExpr(n.X)
		l.simplifyExpr(n.Index)
	case *ast.ParenExpr:
		l.simplifyExpr(n.X)
	case *ast.TupleExpr:
		for _, x := range n.Elems {
			l.simplifyExpr(x)
		}
	case *ast.ListExpr:
		for _, x := range n.Elems {
			l.simplifyExpr(x)
		}
	case *ast.MapExpr:
		for _, me := range n.Entries {
			l.simplifyExpr(me.Key)
			l.simplifyExpr(me.Value)
		}
	case *ast.StructLit:
		l.simplifyExpr(n.Type)
		for _, f := range n.Fields {
			l.simplifyExpr(f.Value)
		}
		l.simplifyExpr(n.Spread)
	case *ast.RangeExpr:
		l.simplifyExpr(n.Start)
		l.simplifyExpr(n.Stop)
	case *ast.QuestionExpr:
		l.simplifyExpr(n.X)
	case *ast.TurbofishExpr:
		l.simplifyExpr(n.Base)
	}
}

// checkRedundantBool fires L0040 when both branches of an if-else are
// a single BoolLit and they are opposite values.
func (l *linter) checkRedundantBool(n *ast.IfExpr) {
	if n.Else == nil {
		return
	}
	thenLit, ok := soleBoolLit(n.Then)
	if !ok {
		return
	}
	var elseLit *ast.BoolLit
	switch e := n.Else.(type) {
	case *ast.Block:
		if lit, okx := soleBoolLit(e); okx {
			elseLit = lit
		}
	}
	if elseLit == nil || thenLit.Value == elseLit.Value {
		return
	}
	hint := "replace with the condition directly"
	if !thenLit.Value {
		hint = "replace with `!(cond)`"
	}
	l.emit(diag.New(diag.Warning,
		"redundant `if … { true } else { false }`").
		Code(diag.CodeRedundantBool).
		Primary(diag.Span{Start: n.PosV, End: n.EndV},
			"this can be replaced with the condition").
		Hint(hint).
		Build())
}

// soleBoolLit returns the single BoolLit at the tail of a block body
// (either a bare BoolLit ExprStmt as the only statement, or a trailing
// expression-stmt after no-op code). Returns ok=false if the block is
// anything else.
func soleBoolLit(b *ast.Block) (*ast.BoolLit, bool) {
	if b == nil || len(b.Stmts) != 1 {
		return nil, false
	}
	es, ok := b.Stmts[0].(*ast.ExprStmt)
	if !ok {
		return nil, false
	}
	lit, ok := es.X.(*ast.BoolLit)
	if !ok {
		return nil, false
	}
	return lit, true
}

func isComparisonOp(k token.Kind) bool {
	switch k {
	case token.EQ, token.NEQ, token.LT, token.GT, token.LEQ, token.GEQ:
		return true
	}
	return false
}

// exprsEqual reports whether two expressions are syntactically and
// (for identifiers) referentially equal, under an evaluation model that
// conservatively treats calls, closures, struct literals, and other
// "might have side effects / be expensive" nodes as non-equal to
// themselves. This keeps self-compare / self-assign from flagging
// `f() == f()` which may legitimately differ each call.
func exprsEqual(a, b ast.Expr, rr *resolve.Result) bool {
	if a == nil || b == nil {
		return false
	}
	switch x := a.(type) {
	case *ast.Ident:
		y, ok := b.(*ast.Ident)
		if !ok {
			return false
		}
		if x.Name != y.Name {
			return false
		}
		if rr == nil {
			return true
		}
		// If the resolver saw both, require identical Symbol identity
		// so we don't flag `x` vs `x` that refer to distinct shadowed
		// bindings (the shadow warning handles that separately).
		sx, sy := rr.Refs[x], rr.Refs[y]
		if sx == nil || sy == nil {
			// One unresolved — treat as possibly equal if names match.
			return true
		}
		return sx == sy
	case *ast.FieldExpr:
		y, ok := b.(*ast.FieldExpr)
		if !ok || x.Name != y.Name || x.IsOptional != y.IsOptional {
			return false
		}
		return exprsEqual(x.X, y.X, rr)
	case *ast.IndexExpr:
		y, ok := b.(*ast.IndexExpr)
		if !ok {
			return false
		}
		return exprsEqual(x.X, y.X, rr) && exprsEqual(x.Index, y.Index, rr)
	case *ast.IntLit:
		y, ok := b.(*ast.IntLit)
		return ok && x.Text == y.Text
	case *ast.FloatLit:
		y, ok := b.(*ast.FloatLit)
		return ok && x.Text == y.Text
	case *ast.BoolLit:
		y, ok := b.(*ast.BoolLit)
		return ok && x.Value == y.Value
	case *ast.CharLit:
		y, ok := b.(*ast.CharLit)
		return ok && x.Value == y.Value
	case *ast.ByteLit:
		y, ok := b.(*ast.ByteLit)
		return ok && x.Value == y.Value
	// String literals are too variable (raw / triple / interpolation)
	// to compare meaningfully here — conservatively say no.
	case *ast.ParenExpr:
		y, ok := b.(*ast.ParenExpr)
		if !ok {
			return false
		}
		return exprsEqual(x.X, y.X, rr)
	}
	// Calls, closures, list/map/struct literals, unary/binary on
	// non-idents, etc. — bail out conservatively.
	return false
}

// ---- L0022: constant condition in `if` ----

func (l *linter) checkConstantCondition(n *ast.IfExpr) {
	// if-let bindings test a pattern; they're never "constant" in the
	// sense this rule means.
	if n.IsIfLet {
		return
	}
	val, ok := evalConstantBool(n.Cond)
	if !ok {
		return
	}
	lit := "true"
	if !val {
		lit = "false"
	}
	l.emit(diag.New(diag.Warning,
		"`if` condition is always "+lit).
		Code(diag.CodeConstantCondition).
		Primary(diag.Span{Start: n.Cond.Pos(), End: n.Cond.End()},
			"always "+lit).
		Hint("drop the `if`, or use the real condition instead of a literal").
		Build())
}

// evalConstantBool recognises the tiny set of condition shapes we can
// prove true/false without type information: bare BoolLit, `!BoolLit`,
// and parentheses around either.
func evalConstantBool(e ast.Expr) (bool, bool) {
	for {
		switch n := e.(type) {
		case *ast.BoolLit:
			return n.Value, true
		case *ast.ParenExpr:
			e = n.X
		case *ast.UnaryExpr:
			if n.Op != token.NOT {
				return false, false
			}
			v, ok := evalConstantBool(n.X)
			if !ok {
				return false, false
			}
			return !v, true
		default:
			return false, false
		}
	}
}

// ---- L0023: empty `if` / `else` branch ----

func (l *linter) checkEmptyBranch(n *ast.IfExpr) {
	if n.Then != nil && len(n.Then.Stmts) == 0 {
		l.emit(diag.New(diag.Warning,
			"`if` body is empty").
			Code(diag.CodeEmptyBranch).
			Primary(diag.Span{Start: n.Then.PosV, End: n.Then.EndV},
				"empty block").
			Hint("fill in the branch, or negate the condition and drop the `if`").
			Build())
	}
	if elseBlock, ok := n.Else.(*ast.Block); ok && len(elseBlock.Stmts) == 0 {
		l.emit(diag.New(diag.Warning,
			"`else` body is empty").
			Code(diag.CodeEmptyBranch).
			Primary(diag.Span{Start: elseBlock.PosV, End: elseBlock.EndV},
				"empty block").
			Hint("drop the empty `else`, or fill it in").
			Build())
	}
}

// ---- L0025: identical branches ----

func (l *linter) checkIdenticalBranches(n *ast.IfExpr) {
	if n.Else == nil {
		return
	}
	elseBlock, ok := n.Else.(*ast.Block)
	if !ok || n.Then == nil {
		return
	}
	if len(n.Then.Stmts) == 0 || len(elseBlock.Stmts) == 0 {
		return // handled by L0023
	}
	if !blocksEqual(n.Then, elseBlock, l.resolved) {
		return
	}
	l.emit(diag.New(diag.Warning,
		"both branches of `if` evaluate to the same expression").
		Code(diag.CodeIdenticalBranches).
		Primary(diag.Span{Start: n.PosV, End: n.EndV},
			"condition is dead code").
		Hint("replace the whole `if`/`else` with the shared expression").
		Build())
}

// blocksEqual tests two single-statement blocks for structural equality
// of their sole ExprStmt. Multi-statement equality is intentionally not
// supported — it would have too many false positives for side-effectful
// statements.
func blocksEqual(a, b *ast.Block, rr *resolve.Result) bool {
	if len(a.Stmts) != 1 || len(b.Stmts) != 1 {
		return false
	}
	ea, ok := a.Stmts[0].(*ast.ExprStmt)
	if !ok {
		return false
	}
	eb, ok := b.Stmts[0].(*ast.ExprStmt)
	if !ok {
		return false
	}
	return exprsEqual(ea.X, eb.X, rr)
}

// ---- L0043: double negation `!!x` ----

func (l *linter) checkDoubleNegation(n *ast.UnaryExpr) {
	if n.Op != token.NOT {
		return
	}
	inner, ok := n.X.(*ast.UnaryExpr)
	if !ok || inner.Op != token.NOT {
		return
	}
	l.emit(diag.New(diag.Warning,
		"double negation `!!` is a no-op on `Bool`").
		Code(diag.CodeDoubleNegation).
		Primary(diag.Span{Start: n.PosV, End: n.EndV},
			"drop both `!`").
		Hint("write the operand directly").
		Build())
}

// ---- L0044: comparison with bool literal ----

func (l *linter) checkBoolLiteralCompare(n *ast.BinaryExpr) {
	if n.Op != token.EQ && n.Op != token.NEQ {
		return
	}
	if _, ok := n.Left.(*ast.BoolLit); ok {
		l.emitBoolCompare(n)
		return
	}
	if _, ok := n.Right.(*ast.BoolLit); ok {
		l.emitBoolCompare(n)
	}
}

func (l *linter) emitBoolCompare(n *ast.BinaryExpr) {
	l.emit(diag.New(diag.Warning,
		"comparing `Bool` against a literal is redundant").
		Code(diag.CodeBoolLiteralCompare).
		Primary(diag.Span{Start: n.PosV, End: n.EndV},
			"use the Bool directly").
		Hint("replace `x == true` with `x`, and `x == false` with `!x`").
		Build())
}

// ---- L0045: negated bool literal `!true` / `!false` ----

func (l *linter) checkNegatedBoolLiteral(n *ast.UnaryExpr) {
	if n.Op != token.NOT {
		return
	}
	lit, ok := n.X.(*ast.BoolLit)
	if !ok {
		return
	}
	opposite := "false"
	if !lit.Value {
		opposite = "true"
	}
	l.emit(diag.New(diag.Warning,
		"negated bool literal").
		Code(diag.CodeNegatedBoolLiteral).
		Primary(diag.Span{Start: n.PosV, End: n.EndV},
			"simplify to `"+opposite+"`").
		Hint("replace with the opposite literal directly").
		Build())
}
