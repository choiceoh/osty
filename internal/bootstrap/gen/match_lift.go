package gen

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/types"
)

// preLiftMatches walks `e` and lifts each MatchExpr whose arm bodies
// contain a `return` (or other escape that must reach the enclosing
// Osty function) out of expression position into a statement-position
// lowering. Each lifted match is recorded in g.matchSubs so the
// subsequent emitMatch on that node writes the result-var name instead
// of the IIFE form.
//
// Why: the default match emission wraps arms in an IIFE
// (`func() T { ... }()`). A bare `return X` inside an arm body
// returns from the IIFE — not from the enclosing Osty function. Used
// in non-tail position (e.g. `let x = match e { Pat -> { return X } }`)
// the value X is silently bound to x and the outer function keeps
// running. Lifting the match into a statement-form lowering puts the
// arm bodies directly in the outer Go function, so `return` propagates
// correctly.
//
// The walk skips control-flow / closure boundaries to mirror
// preLiftQuestions: a match nested inside an `if` arm, closure, or
// block is not lifted from this call. Each consumer runs its own
// preLiftMatches when it emits its arm bodies (see
// emitArmTrailerLifted and emitStmt's per-kind paths).
func (g *gen) preLiftMatches(e ast.Expr) {
	if e == nil {
		return
	}
	var ms []*ast.MatchExpr
	g.walkDirectMatches(e, &ms)
	if len(ms) == 0 {
		return
	}
	if g.matchSubs == nil {
		g.matchSubs = map[*ast.MatchExpr]string{}
	}
	for _, m := range ms {
		if _, already := g.matchSubs[m]; already {
			continue
		}
		if !matchEscapes(m) {
			continue
		}
		tmp := g.freshVar("_ml")
		// Save and restore the substitution map across the recursive
		// lowering: emitMatchLifted emits arm-body statements through
		// the regular emitStmt path, whose per-kind defer-resets would
		// otherwise wipe our in-progress outer entries. The inner
		// scope gets its own fresh map; ours is restored afterwards.
		saved := g.matchSubs
		g.matchSubs = nil
		g.emitMatchLifted(tmp, m)
		g.matchSubs = saved
		g.matchSubs[m] = tmp
	}
}

// resetMatchSubs clears the substitution map at statement boundary.
func (g *gen) resetMatchSubs() { g.matchSubs = nil }

// walkDirectMatches collects MatchExpr nodes reachable from `e`
// without crossing closure or control-flow boundaries. The traversal
// is preorder in source order, so when a tainted match contains
// another tainted match in its scrutinee position the inner one is
// lifted first.
//
// IfExpr / Block / ClosureExpr branches are not descended: a match
// nested inside one of those is consumed by the inner emitter, which
// runs its own preLiftMatches.
func (g *gen) walkDirectMatches(e ast.Expr, out *[]*ast.MatchExpr) {
	if e == nil {
		return
	}
	switch e := e.(type) {
	case *ast.MatchExpr:
		g.walkDirectMatches(e.Scrutinee, out)
		*out = append(*out, e)
	case *ast.ParenExpr:
		g.walkDirectMatches(e.X, out)
	case *ast.UnaryExpr:
		g.walkDirectMatches(e.X, out)
	case *ast.BinaryExpr:
		g.walkDirectMatches(e.Left, out)
		g.walkDirectMatches(e.Right, out)
	case *ast.CallExpr:
		g.walkDirectMatches(e.Fn, out)
		for _, a := range e.Args {
			g.walkDirectMatches(a.Value, out)
		}
	case *ast.FieldExpr:
		g.walkDirectMatches(e.X, out)
	case *ast.IndexExpr:
		g.walkDirectMatches(e.X, out)
		g.walkDirectMatches(e.Index, out)
	case *ast.TurbofishExpr:
		g.walkDirectMatches(e.Base, out)
	case *ast.TupleExpr:
		for _, x := range e.Elems {
			g.walkDirectMatches(x, out)
		}
	case *ast.ListExpr:
		for _, x := range e.Elems {
			g.walkDirectMatches(x, out)
		}
	case *ast.MapExpr:
		for _, ent := range e.Entries {
			g.walkDirectMatches(ent.Key, out)
			g.walkDirectMatches(ent.Value, out)
		}
	case *ast.StructLit:
		for _, f := range e.Fields {
			if f.Value != nil {
				g.walkDirectMatches(f.Value, out)
			}
		}
	case *ast.RangeExpr:
		g.walkDirectMatches(e.Start, out)
		g.walkDirectMatches(e.Stop, out)
	case *ast.QuestionExpr:
		g.walkDirectMatches(e.X, out)
	case *ast.IfExpr, *ast.ClosureExpr, *ast.Block:
		// stop — these emit their own children with their own pre-lift.
	}
}

// matchEscapes reports whether any arm body of m contains an explicit
// `return` statement that would need to escape past the IIFE
// boundary. Returns inside a nested closure are local to the closure
// and do not count.
func matchEscapes(m *ast.MatchExpr) bool {
	for _, arm := range m.Arms {
		if exprContainsEscape(arm.Body) {
			return true
		}
		if arm.Guard != nil && exprContainsEscape(arm.Guard) {
			return true
		}
	}
	return false
}

// exprContainsEscape walks an Osty expression looking for a `return`
// statement that targets the enclosing function. Stops at ClosureExpr
// because returns inside a closure body return from the closure, not
// the enclosing function.
func exprContainsEscape(e ast.Expr) bool {
	if e == nil {
		return false
	}
	switch e := e.(type) {
	case *ast.Block:
		for _, s := range e.Stmts {
			if stmtContainsEscape(s) {
				return true
			}
		}
	case *ast.IfExpr:
		if exprContainsEscape(e.Cond) {
			return true
		}
		if e.Then != nil {
			for _, s := range e.Then.Stmts {
				if stmtContainsEscape(s) {
					return true
				}
			}
		}
		if e.Else != nil && exprContainsEscape(e.Else) {
			return true
		}
	case *ast.MatchExpr:
		if exprContainsEscape(e.Scrutinee) {
			return true
		}
		for _, arm := range e.Arms {
			if exprContainsEscape(arm.Body) {
				return true
			}
			if arm.Guard != nil && exprContainsEscape(arm.Guard) {
				return true
			}
		}
	case *ast.ParenExpr:
		return exprContainsEscape(e.X)
	case *ast.UnaryExpr:
		return exprContainsEscape(e.X)
	case *ast.BinaryExpr:
		return exprContainsEscape(e.Left) || exprContainsEscape(e.Right)
	case *ast.CallExpr:
		if exprContainsEscape(e.Fn) {
			return true
		}
		for _, a := range e.Args {
			if exprContainsEscape(a.Value) {
				return true
			}
		}
	case *ast.FieldExpr:
		return exprContainsEscape(e.X)
	case *ast.IndexExpr:
		return exprContainsEscape(e.X) || exprContainsEscape(e.Index)
	case *ast.TurbofishExpr:
		return exprContainsEscape(e.Base)
	case *ast.TupleExpr:
		for _, x := range e.Elems {
			if exprContainsEscape(x) {
				return true
			}
		}
	case *ast.ListExpr:
		for _, x := range e.Elems {
			if exprContainsEscape(x) {
				return true
			}
		}
	case *ast.MapExpr:
		for _, ent := range e.Entries {
			if exprContainsEscape(ent.Key) || exprContainsEscape(ent.Value) {
				return true
			}
		}
	case *ast.StructLit:
		for _, f := range e.Fields {
			if f.Value != nil && exprContainsEscape(f.Value) {
				return true
			}
		}
	case *ast.RangeExpr:
		return exprContainsEscape(e.Start) || exprContainsEscape(e.Stop)
	case *ast.QuestionExpr:
		return exprContainsEscape(e.X)
	case *ast.ClosureExpr:
		return false
	}
	return false
}

func stmtContainsEscape(s ast.Stmt) bool {
	if s == nil {
		return false
	}
	switch s := s.(type) {
	case *ast.ReturnStmt:
		return true
	case *ast.LetStmt:
		return exprContainsEscape(s.Value)
	case *ast.AssignStmt:
		for _, t := range s.Targets {
			if exprContainsEscape(t) {
				return true
			}
		}
		return exprContainsEscape(s.Value)
	case *ast.ExprStmt:
		return exprContainsEscape(s.X)
	case *ast.Block:
		for _, ss := range s.Stmts {
			if stmtContainsEscape(ss) {
				return true
			}
		}
	case *ast.ForStmt:
		// A `return` inside the loop body still escapes; break/continue
		// inside it are local to the loop and don't count.
		if s.Body != nil {
			for _, ss := range s.Body.Stmts {
				if stmtContainsEscape(ss) {
					return true
				}
			}
		}
		return exprContainsEscape(s.Iter)
	case *ast.DeferStmt:
		return exprContainsEscape(s.X)
	case *ast.ChanSendStmt:
		return exprContainsEscape(s.Channel) || exprContainsEscape(s.Value)
	}
	return false
}

// emitMatchLifted writes the statement-position lowering of m. The
// emitted shape wraps the arm chain in a one-shot `for { ... }` loop
// whose only purpose is to give arms a `break` target distinct from
// the enclosing function:
//
//	var <tmp> T
//	_ = <tmp>
//	for {
//	    _scrut := <scrutinee>
//	    _ = _scrut
//	    if <pat1 test> { bindings; <body1 trailer> }
//	    if <pat2 test> { bindings; <body2 trailer> }
//	    panic("unreachable match")  // omitted when last arm is total
//	}
//
// Value-producing arms write `<tmp> = <expr>; break` so the loop
// terminates after first match (matching IIFE first-match
// semantics). Escape arms (`return` / `break` / `continue` in the
// arm body's tail) emit the escape statement directly — `return`
// exits the enclosing function, which makes the missing `break`
// trivially unreachable.
//
// The `for` form is the only Go construct that gives a labeled break
// target without abusing `goto`; a bare `{ ... }` block is not a
// break target. The loop iterates exactly once on every code path.
func (g *gen) emitMatchLifted(tmp string, m *ast.MatchExpr) {
	retType := "any"
	if t := g.typeOf(m); t != nil && !types.IsError(t) && !types.IsUnit(t) {
		retType = g.goType(t)
	}
	g.body.writef("var %s %s\n_ = %s\n", tmp, retType, tmp)
	g.body.writeln("for {")
	g.body.indent()
	scrutName := g.freshVar("_m")
	g.body.writef("%s := ", scrutName)
	g.emitExpr(m.Scrutinee)
	g.body.writef("\n_ = %s\n", scrutName)

	scrutType := g.typeOf(m.Scrutinee)
	totallyCovered := false
	for i, a := range m.Arms {
		g.emitMatchArmLifted(tmp, scrutName, scrutType, a)
		if i == len(m.Arms)-1 && g.isCatchAll(a.Pattern) && a.Guard == nil {
			totallyCovered = true
		}
	}
	if !totallyCovered {
		g.body.writeln(`panic("unreachable match")`)
	}
	g.body.dedent()
	g.body.writeln("}")
}

// emitMatchArmLifted is the lifted-mode counterpart of emitMatchArm.
// Bindings + guard test are unchanged from the IIFE form; only the
// trailer differs — see emitArmTrailerLifted.
func (g *gen) emitMatchArmLifted(tmp, scrut string, scrutType types.Type, arm *ast.MatchArm) {
	catchAll := g.isCatchAll(arm.Pattern)

	if catchAll && arm.Guard == nil {
		g.body.writeln("{")
		g.body.indent()
		g.emitPatternBindings(scrut, scrutType, arm.Pattern)
		g.emitArmTrailerLifted(tmp, arm.Body)
		g.body.dedent()
		g.body.writeln("}")
		return
	}

	if !catchAll {
		g.body.write("if ")
		g.emitPatternTest(scrut, scrutType, arm.Pattern)
		g.body.writeln(" {")
		g.body.indent()
	} else {
		g.body.writeln("{")
		g.body.indent()
	}

	g.emitPatternBindings(scrut, scrutType, arm.Pattern)

	if arm.Guard != nil {
		g.body.write("if ")
		g.emitExpr(arm.Guard)
		g.body.writeln(" {")
		g.body.indent()
		g.emitArmTrailerLifted(tmp, arm.Body)
		g.body.dedent()
		g.body.writeln("}")
	} else {
		g.emitArmTrailerLifted(tmp, arm.Body)
	}

	g.body.dedent()
	g.body.writeln("}")
}

// emitArmTrailerLifted writes one arm's body in lifted mode.
//
// When the body's tail is an escape statement (return / break /
// continue), the body is emitted as plain statements — no assignment
// is needed because the escape transfers control before any
// assignment site, and no `break` from the enclosing for is needed
// because `return` already exits the function (and break/continue
// already exit the surrounding loop).
//
// Otherwise the body's tail expression is wrapped as `tmp = <expr>`
// followed by `break` to exit the enclosing one-shot for, giving us
// first-match semantics. Block bodies are inlined: the leading
// statements emit through emitStmt (which runs its own pre-lift),
// then the last expression assigns to tmp and breaks.
func (g *gen) emitArmTrailerLifted(tmp string, body ast.Expr) {
	if b, ok := body.(*ast.Block); ok && len(b.Stmts) > 0 {
		last := b.Stmts[len(b.Stmts)-1]
		for _, s := range b.Stmts[:len(b.Stmts)-1] {
			g.emitStmt(s)
		}
		switch s := last.(type) {
		case *ast.ExprStmt:
			g.preLiftMatches(s.X)
			g.body.writef("%s = ", tmp)
			g.emitExpr(s.X)
			g.body.nl()
			g.body.writeln("break")
		default:
			// Last stmt is non-value (return / break / continue / ...);
			// emit it directly. Any `break` we add would be unreachable.
			g.emitStmt(last)
		}
		return
	}
	g.preLiftMatches(body)
	g.body.writef("%s = ", tmp)
	g.emitExpr(body)
	g.body.nl()
	g.body.writeln("break")
}
