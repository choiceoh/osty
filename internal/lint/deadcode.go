package lint

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
)

// lintDeadCode flags any statement that follows a diverging statement
// at the same block level. "Diverging" means control never flows past
// the statement. Recognised forms:
//
//   - direct `return`, `break`, `continue`
//   - an if/else where BOTH branches diverge (e.g. `if c { return 1 }
//     else { return 2 }`)
//   - a match where EVERY arm diverges AND a `_` (wildcard) arm is
//     present (proxy for exhaustiveness until the type checker owns it)
//   - an infinite `for { … }` with no top-level `break` escape
//   - a block whose last statement is itself diverging
//
// Still NOT recognised (type-checker territory):
//   - function calls to `panic(...)` / `exit(...)` / other Never-typed
//     functions
//   - match with no wildcard but complete coverage of a finite enum
//
// One warning is emitted per block — its primary span covers every dead
// statement, with a secondary span pointing at the terminator that made
// them unreachable.
func (l *linter) lintDeadCode() {
	for _, d := range l.file.Decls {
		l.deadCodeDecl(d)
	}
	for _, s := range l.file.Stmts {
		l.deadCodeStmt(s)
	}
}

func (l *linter) deadCodeDecl(d ast.Decl) {
	switch n := d.(type) {
	case *ast.FnDecl:
		if n.Body != nil {
			l.deadCodeBlock(n.Body)
		}
	case *ast.StructDecl:
		for _, m := range n.Methods {
			if m.Body != nil {
				l.deadCodeBlock(m.Body)
			}
		}
	case *ast.EnumDecl:
		for _, m := range n.Methods {
			if m.Body != nil {
				l.deadCodeBlock(m.Body)
			}
		}
	case *ast.InterfaceDecl:
		// Interface methods may have default bodies.
		for _, m := range n.Methods {
			if m.Body != nil {
				l.deadCodeBlock(m.Body)
			}
		}
	case *ast.LetDecl:
		l.deadCodeExpr(n.Value)
	}
}

// deadCodeBlock checks one block for a terminator-then-statement pattern,
// then recurses into every nested block.
func (l *linter) deadCodeBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for i, s := range b.Stmts {
		if isDivergingStmt(s) && i+1 < len(b.Stmts) {
			rest := b.Stmts[i+1:]
			start := rest[0].Pos()
			end := rest[len(rest)-1].End()
			l.emit(diag.New(diag.Warning, "unreachable code after diverging statement").
				Code(diag.CodeDeadCode).
				Primary(diag.Span{Start: start, End: end}, "this code is unreachable").
				Secondary(diag.Span{Start: s.Pos(), End: s.End()},
					"control never returns from this statement").
				Hint("remove the unreachable statements, or restructure control flow").
				Build())
			break // one warning per block
		}
	}
	// Recurse into all statements regardless of where the terminator
	// landed — even unreachable code may itself contain blocks worth
	// linting (if the user keeps it around for later use, we still want
	// to flag a dead-code pattern within it).
	for _, s := range b.Stmts {
		l.deadCodeStmt(s)
	}
}

func (l *linter) deadCodeStmt(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.LetStmt:
		l.deadCodeExpr(n.Value)
	case *ast.ExprStmt:
		l.deadCodeExpr(n.X)
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			l.deadCodeExpr(t)
		}
		l.deadCodeExpr(n.Value)
	case *ast.ReturnStmt:
		l.deadCodeExpr(n.Value)
	case *ast.DeferStmt:
		l.deadCodeExpr(n.X)
	case *ast.ForStmt:
		l.deadCodeExpr(n.Iter)
		l.deadCodeBlock(n.Body)
	case *ast.ChanSendStmt:
		l.deadCodeExpr(n.Channel)
		l.deadCodeExpr(n.Value)
	}
}

func (l *linter) deadCodeExpr(e ast.Expr) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.Block:
		l.deadCodeBlock(n)
	case *ast.IfExpr:
		l.deadCodeExpr(n.Cond)
		l.deadCodeBlock(n.Then)
		l.deadCodeExpr(n.Else)
	case *ast.MatchExpr:
		l.deadCodeExpr(n.Scrutinee)
		for _, arm := range n.Arms {
			l.deadCodeExpr(arm.Guard)
			l.deadCodeExpr(arm.Body)
		}
	case *ast.ClosureExpr:
		l.deadCodeExpr(n.Body)
	case *ast.UnaryExpr:
		l.deadCodeExpr(n.X)
	case *ast.BinaryExpr:
		l.deadCodeExpr(n.Left)
		l.deadCodeExpr(n.Right)
	case *ast.CallExpr:
		l.deadCodeExpr(n.Fn)
		for _, a := range n.Args {
			l.deadCodeExpr(a.Value)
		}
	case *ast.FieldExpr:
		l.deadCodeExpr(n.X)
	case *ast.IndexExpr:
		l.deadCodeExpr(n.X)
		l.deadCodeExpr(n.Index)
	case *ast.ParenExpr:
		l.deadCodeExpr(n.X)
	case *ast.TupleExpr:
		for _, x := range n.Elems {
			l.deadCodeExpr(x)
		}
	case *ast.ListExpr:
		for _, x := range n.Elems {
			l.deadCodeExpr(x)
		}
	case *ast.MapExpr:
		for _, e := range n.Entries {
			l.deadCodeExpr(e.Key)
			l.deadCodeExpr(e.Value)
		}
	case *ast.StructLit:
		l.deadCodeExpr(n.Type)
		for _, f := range n.Fields {
			l.deadCodeExpr(f.Value)
		}
		l.deadCodeExpr(n.Spread)
	case *ast.RangeExpr:
		l.deadCodeExpr(n.Start)
		l.deadCodeExpr(n.Stop)
	case *ast.QuestionExpr:
		l.deadCodeExpr(n.X)
	case *ast.TurbofishExpr:
		l.deadCodeExpr(n.Base)
	}
}

// isDivergingStmt reports whether control can never pass this statement.
func isDivergingStmt(s ast.Stmt) bool {
	switch n := s.(type) {
	case *ast.ReturnStmt, *ast.BreakStmt, *ast.ContinueStmt:
		return true
	case *ast.ExprStmt:
		return isDivergingExpr(n.X)
	case *ast.ForStmt:
		// `for { ... }` with no condition / pattern is infinite. It
		// diverges unless the body contains a reachable `break` at the
		// top level or inside a non-for branch (a break inside a nested
		// for escapes the inner loop, not this one).
		if n.Iter == nil && n.Pattern == nil && !n.IsForLet {
			return !blockHasBreakEscape(n.Body)
		}
	case *ast.Block:
		return isDivergingBlock(n)
	}
	return false
}

// isDivergingExpr is the expression counterpart. An expression diverges
// when evaluating it can never complete normally.
func isDivergingExpr(e ast.Expr) bool {
	if e == nil {
		return false
	}
	switch n := e.(type) {
	case *ast.Block:
		return isDivergingBlock(n)
	case *ast.IfExpr:
		// Without an else, control falls through when `cond` is false.
		if n.Else == nil {
			return false
		}
		return isDivergingBlock(n.Then) && isDivergingExpr(n.Else)
	case *ast.MatchExpr:
		if len(n.Arms) == 0 {
			return false
		}
		hasWild := false
		for _, arm := range n.Arms {
			if !isDivergingExpr(arm.Body) {
				return false
			}
			if _, ok := arm.Pattern.(*ast.WildcardPat); ok {
				hasWild = true
			}
		}
		// Require a wildcard as a stand-in for exhaustiveness proof.
		// The proper check belongs in the type checker.
		return hasWild
	case *ast.ParenExpr:
		return isDivergingExpr(n.X)
	}
	return false
}

// isDivergingBlock is true when the block's last statement diverges.
// Empty blocks do not diverge (they fall through immediately).
func isDivergingBlock(b *ast.Block) bool {
	if b == nil || len(b.Stmts) == 0 {
		return false
	}
	return isDivergingStmt(b.Stmts[len(b.Stmts)-1])
}

// blockHasBreakEscape reports whether the block contains a `break`
// reachable from the top of the block without first crossing an
// enclosing `for` loop (i.e. a break that would escape the block's
// enclosing for).
func blockHasBreakEscape(b *ast.Block) bool {
	if b == nil {
		return false
	}
	for _, s := range b.Stmts {
		if stmtHasBreakEscape(s) {
			return true
		}
	}
	return false
}

func stmtHasBreakEscape(s ast.Stmt) bool {
	switch n := s.(type) {
	case *ast.BreakStmt:
		return true
	case *ast.ExprStmt:
		return exprHasBreakEscape(n.X)
	case *ast.LetStmt:
		return exprHasBreakEscape(n.Value)
	case *ast.AssignStmt:
		return exprHasBreakEscape(n.Value)
	case *ast.ReturnStmt:
		return exprHasBreakEscape(n.Value)
	case *ast.DeferStmt:
		return exprHasBreakEscape(n.X)
	case *ast.Block:
		return blockHasBreakEscape(n)
		// *ForStmt: a break inside a nested for loop targets the inner for,
		// not the outer one we're analysing. Skip recursion into the body.
	}
	return false
}

func exprHasBreakEscape(e ast.Expr) bool {
	if e == nil {
		return false
	}
	switch n := e.(type) {
	case *ast.Block:
		return blockHasBreakEscape(n)
	case *ast.IfExpr:
		if blockHasBreakEscape(n.Then) {
			return true
		}
		return exprHasBreakEscape(n.Else)
	case *ast.MatchExpr:
		for _, arm := range n.Arms {
			if exprHasBreakEscape(arm.Body) {
				return true
			}
		}
	case *ast.ParenExpr:
		return exprHasBreakEscape(n.X)
	}
	return false
}
