package gen

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/types"
)

// preLiftQuestions walks `e` and emits one lift block per `?` operator
// that appears in a direct-evaluation position (not guarded by a
// closure, branch, or loop body). Each lifted `?` is recorded in
// g.questionSubs so the subsequent emitExpr for that node writes the
// unwrapped temp instead of a panic-IIFE.
//
// The walk preserves source order, which matches Go's left-to-right
// evaluation of the rewritten expression. A `?` nested inside a
// control-flow expression (closure body, if arm, match arm, loop body)
// is left alone — it binds to its own lexical return, and lifting it
// here would both short-circuit prematurely and attempt to return the
// wrong type.
//
// Callers must ensure questionSubs is initialized (or nil, in which
// case this is a no-op for expressions without `?`).
func (g *gen) preLiftQuestions(e ast.Expr) {
	if e == nil {
		return
	}
	var qs []*ast.QuestionExpr
	g.walkDirectQuestions(e, &qs)
	if len(qs) == 0 {
		return
	}
	if g.questionSubs == nil {
		g.questionSubs = map[*ast.QuestionExpr]string{}
	}
	for _, q := range qs {
		if _, already := g.questionSubs[q]; already {
			continue
		}
		tmp := g.freshVar("_q")
		g.emitQuestionLiftBody(tmp, q)
		if g.isResultOperand(q.X) {
			g.questionSubs[q] = tmp + ".Value"
		} else {
			g.questionSubs[q] = "*" + tmp
		}
	}
}

// emitQuestionLiftBody writes the `tmp := <operand>; if <failure> { return <failure> }`
// prelude for one lifted `?` occurrence. Shared by the let-stmt fast
// path, the return-stmt lift, and preLiftQuestions.
func (g *gen) emitQuestionLiftBody(tmp string, q *ast.QuestionExpr) {
	isResult := g.isResultOperand(q.X)
	g.body.writef("%s := ", tmp)
	g.emitExpr(q.X)
	g.body.nl()
	if isResult {
		retGo := "any"
		if g.currentRetType != nil {
			retGo = g.goTypeExpr(g.currentRetType)
		}
		g.body.writef("if !%s.IsOk { return %s{Error: %s.Error} }\n", tmp, retGo, tmp)
		return
	}
	g.body.writef("if %s == nil { return nil }\n", tmp)
}

// isResultOperand reports whether the operand of a `?` yields a
// Result<T, E>. Prefers the checker-inferred type; falls back to an
// AST shape check for stdlib calls the checker hasn't typed (e.g.
// `fs.readToString(p)?` inside a registry-less transpile pass).
func (g *gen) isResultOperand(e ast.Expr) bool {
	if t := g.typeOf(e); t != nil {
		if n, ok := t.(*types.Named); ok && n.Sym != nil && n.Sym.Name == "Result" {
			return true
		}
	}
	return g.astShapeIsResult(e)
}

// astShapeIsResult inspects the AST of an expression whose checker type
// is missing to decide whether it returns a Result. Currently recognises
// stdlib fs calls that are declared with a Result return type; this is
// the set of calls the pre-lift might otherwise miss when the checker
// didn't propagate the stub's signature to the call expression.
func (g *gen) astShapeIsResult(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	f, ok := call.Fn.(*ast.FieldExpr)
	if !ok {
		return false
	}
	id, ok := f.X.(*ast.Ident)
	if !ok {
		return false
	}
	mod, ok := g.stdAliases[id.Name]
	if !ok {
		return false
	}
	if mod == "fs" {
		switch f.Name {
		case "readToString", "writeString", "remove":
			return true
		}
	}
	return false
}

// walkDirectQuestions collects QuestionExpr nodes reachable from `e`
// without crossing a control-flow or closure boundary. The traversal
// is preorder in source order so lift blocks emit in evaluation order.
func (g *gen) walkDirectQuestions(e ast.Expr, out *[]*ast.QuestionExpr) {
	if e == nil {
		return
	}
	switch e := e.(type) {
	case *ast.QuestionExpr:
		// First descend into the operand so nested `a??` lifts correctly.
		g.walkDirectQuestions(e.X, out)
		*out = append(*out, e)
	case *ast.ParenExpr:
		g.walkDirectQuestions(e.X, out)
	case *ast.UnaryExpr:
		g.walkDirectQuestions(e.X, out)
	case *ast.BinaryExpr:
		g.walkDirectQuestions(e.Left, out)
		g.walkDirectQuestions(e.Right, out)
	case *ast.CallExpr:
		g.walkDirectQuestions(e.Fn, out)
		for _, a := range e.Args {
			g.walkDirectQuestions(a.Value, out)
		}
	case *ast.FieldExpr:
		// `.` (and `?.`) access — both evaluate the receiver directly.
		g.walkDirectQuestions(e.X, out)
	case *ast.IndexExpr:
		g.walkDirectQuestions(e.X, out)
		g.walkDirectQuestions(e.Index, out)
	case *ast.TurbofishExpr:
		g.walkDirectQuestions(e.Base, out)
	case *ast.TupleExpr:
		for _, x := range e.Elems {
			g.walkDirectQuestions(x, out)
		}
	case *ast.ListExpr:
		for _, x := range e.Elems {
			g.walkDirectQuestions(x, out)
		}
	case *ast.MapExpr:
		for _, ent := range e.Entries {
			g.walkDirectQuestions(ent.Key, out)
			g.walkDirectQuestions(ent.Value, out)
		}
	case *ast.StructLit:
		for _, f := range e.Fields {
			if f.Value != nil {
				g.walkDirectQuestions(f.Value, out)
			}
		}
	case *ast.RangeExpr:
		g.walkDirectQuestions(e.Start, out)
		g.walkDirectQuestions(e.Stop, out)
	// Control-flow / closure boundaries are deliberately not descended:
	// any `?` inside them targets its own lexical return context.
	case *ast.IfExpr, *ast.MatchExpr, *ast.ClosureExpr, *ast.Block:
		// stop
	}
}

// resetQuestionSubs clears the lift-substitution map. Called after a
// statement finishes emitting so subs don't bleed into the next
// statement (mostly a sanity measure — node keys are unique, but the
// map can grow without bound otherwise).
func (g *gen) resetQuestionSubs() {
	g.questionSubs = nil
}
