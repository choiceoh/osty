package lint

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/types"
)

// lintIgnoredResult flags statement-level expressions whose type is
// `Result<T, E>` or `Option<T>` (or `T?`) — discarding such values
// silently loses errors / missing-value signals. Analogous to Rust's
// `must_use` and Go's `errcheck`.
//
// Only fires when the type checker was supplied (otherwise there's no
// type info to consult). The rule targets:
//
//   - top-level script statements of the form `expr`
//   - `ExprStmt` nodes inside function bodies
//
// Assignment targets of the form `let _ = risky()` are NOT flagged —
// the `_` pattern is the idiomatic "I am explicitly ignoring this".
func (l *linter) lintIgnoredResult() {
	if l.check == nil {
		return
	}
	for _, d := range l.file.Decls {
		l.ignoredResultDecl(d)
	}
	// Script top-level statements have no declared return type. The tail
	// position DOES count as discarded unless it's explicit `return ...`.
	for _, s := range l.file.Stmts {
		l.ignoredResultStmtInBlock(s, false /* not a value-producing tail */)
	}
}

func (l *linter) ignoredResultDecl(d ast.Decl) {
	switch n := d.(type) {
	case *ast.FnDecl:
		l.ignoredResultFnBody(n.ReturnType, n.Body)
	case *ast.StructDecl:
		for _, m := range n.Methods {
			l.ignoredResultFnBody(m.ReturnType, m.Body)
		}
	case *ast.EnumDecl:
		for _, m := range n.Methods {
			l.ignoredResultFnBody(m.ReturnType, m.Body)
		}
	case *ast.InterfaceDecl:
		for _, m := range n.Methods {
			l.ignoredResultFnBody(m.ReturnType, m.Body)
		}
	}
}

// ignoredResultFnBody walks a function body treating the tail expression
// as a value ONLY when the function has a declared non-unit return type.
// For unit-returning fns (implicit `()`), the tail expression IS
// discarded, so a trailing `find(3)` in a unit-returning `fn main()` is
// correctly flagged.
func (l *linter) ignoredResultFnBody(retType ast.Type, b *ast.Block) {
	if b == nil {
		return
	}
	tailIsValue := retType != nil // nil == unit; non-nil == declared return
	for i, s := range b.Stmts {
		isTail := i == len(b.Stmts)-1
		l.ignoredResultStmtInBlock(s, isTail && tailIsValue)
	}
}

func (l *linter) ignoredResultBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for i, s := range b.Stmts {
		// For blocks we visit NOT as a fn body (if branch, match arm,
		// closure body, plain expression block), treat the tail as a
		// value position — we don't know if the enclosing context needs
		// the value, but the pessimistic choice (tail-is-value) avoids
		// noise at the cost of a few missed cases.
		isTail := i == len(b.Stmts)-1
		l.ignoredResultStmtInBlock(s, isTail)
	}
}

func (l *linter) ignoredResultStmtInBlock(s ast.Stmt, tailIsValue bool) {
	switch n := s.(type) {
	case *ast.ExprStmt:
		if !tailIsValue && l.isIgnoredMustUse(n.X) {
			l.warnNode(n.X, diag.CodeIgnoredResult,
				"discarded `%s` value — handle the error or assign to `_`", l.describeMustUse(n.X))
		}
		l.ignoredResultExpr(n.X)
	case *ast.LetStmt:
		l.ignoredResultExpr(n.Value)
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			l.ignoredResultExpr(t)
		}
		l.ignoredResultExpr(n.Value)
	case *ast.ReturnStmt:
		l.ignoredResultExpr(n.Value)
	case *ast.DeferStmt:
		if l.isIgnoredMustUse(n.X) {
			l.warnNode(n.X, diag.CodeIgnoredResult,
				"discarded `%s` value in `defer` — wrap with `ignoreError` / `logError` (§10.1) or handle explicitly", l.describeMustUse(n.X))
		}
		l.ignoredResultExpr(n.X)
	case *ast.ForStmt:
		l.ignoredResultExpr(n.Iter)
		l.ignoredResultBlock(n.Body)
	case *ast.ChanSendStmt:
		l.ignoredResultExpr(n.Channel)
		l.ignoredResultExpr(n.Value)
	case *ast.Block:
		l.ignoredResultBlock(n)
	}
}

func (l *linter) ignoredResultExpr(e ast.Expr) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.Block:
		l.ignoredResultBlock(n)
	case *ast.IfExpr:
		l.ignoredResultExpr(n.Cond)
		l.ignoredResultBlock(n.Then)
		l.ignoredResultExpr(n.Else)
	case *ast.MatchExpr:
		l.ignoredResultExpr(n.Scrutinee)
		for _, arm := range n.Arms {
			l.ignoredResultExpr(arm.Guard)
			l.ignoredResultExpr(arm.Body)
		}
	case *ast.ClosureExpr:
		l.ignoredResultExpr(n.Body)
	}
}

// isIgnoredMustUse reports whether e has a must-use-style type (Result,
// Option, or `?`-typed) whose value would be silently discarded when the
// expression appears as a plain statement.
func (l *linter) isIgnoredMustUse(e ast.Expr) bool {
	if l.check == nil {
		return false
	}
	t := l.check.LookupType(e)
	if t == nil || types.IsError(t) {
		return false
	}
	if types.IsOptional(t) {
		return true
	}
	if _, ok := types.AsNamedBuiltin(t, "Result"); ok {
		return true
	}
	if _, ok := types.AsNamedBuiltin(t, "Option"); ok {
		return true
	}
	return false
}

func (l *linter) describeMustUse(e ast.Expr) string {
	t := l.check.LookupType(e)
	if t == nil {
		return "Result"
	}
	return t.String()
}

// extendDeadCodeWithTypeInfo augments lintDeadCode by flagging
// statements that follow a diverging call — either a Never-typed call
// (precise, via the type checker) or a call to a conventionally
// divergent function (`panic`, `exit`, `abort`, `unreachable`, `todo`)
// when type info is unavailable.
//
// Runs as a supplementary pass after the AST-only lintDeadCode so that
// either mechanism can catch cases the other misses; one warning per
// block is still the rule.
func (l *linter) extendDeadCodeWithTypeInfo() {
	for _, d := range l.file.Decls {
		l.neverDeclDeadCode(d)
	}
	for _, s := range l.file.Stmts {
		l.neverStmtDeadCode(s)
	}
}

func (l *linter) neverDeclDeadCode(d ast.Decl) {
	switch n := d.(type) {
	case *ast.FnDecl:
		l.neverBlockDeadCode(n.Body)
	case *ast.StructDecl:
		for _, m := range n.Methods {
			l.neverBlockDeadCode(m.Body)
		}
	case *ast.EnumDecl:
		for _, m := range n.Methods {
			l.neverBlockDeadCode(m.Body)
		}
	case *ast.InterfaceDecl:
		for _, m := range n.Methods {
			l.neverBlockDeadCode(m.Body)
		}
	}
}

func (l *linter) neverBlockDeadCode(b *ast.Block) {
	if b == nil {
		return
	}
	// If a prior pass (AST-level lintDeadCode) already fired on this
	// block, skip — one warning per block is the agreed behavior.
	for i, s := range b.Stmts {
		if isDivergingStmt(s) {
			return // AST pass handles it
		}
		if l.stmtIsNeverTyped(s) && i+1 < len(b.Stmts) {
			rest := b.Stmts[i+1:]
			start := rest[0].Pos()
			end := rest[len(rest)-1].End()
			l.emit(diag.New(diag.Warning,
				"unreachable code after diverging call").
				Code(diag.CodeDeadCode).
				Primary(diag.Span{Start: start, End: end},
					"this code is unreachable").
				Secondary(diag.Span{Start: s.Pos(), End: s.End()},
					"this call returns `Never` — control never reaches the next statement").
				Hint("remove the unreachable statements").
				Build())
			return
		}
	}
	for _, s := range b.Stmts {
		l.neverStmtDeadCode(s)
	}
}

func (l *linter) neverStmtDeadCode(s ast.Stmt) {
	switch n := s.(type) {
	case *ast.ExprStmt:
		l.neverExprDeadCode(n.X)
	case *ast.LetStmt:
		l.neverExprDeadCode(n.Value)
	case *ast.AssignStmt:
		l.neverExprDeadCode(n.Value)
		for _, t := range n.Targets {
			l.neverExprDeadCode(t)
		}
	case *ast.ReturnStmt:
		l.neverExprDeadCode(n.Value)
	case *ast.DeferStmt:
		l.neverExprDeadCode(n.X)
	case *ast.ForStmt:
		l.neverExprDeadCode(n.Iter)
		l.neverBlockDeadCode(n.Body)
	case *ast.Block:
		l.neverBlockDeadCode(n)
	}
}

func (l *linter) neverExprDeadCode(e ast.Expr) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.Block:
		l.neverBlockDeadCode(n)
	case *ast.IfExpr:
		l.neverExprDeadCode(n.Cond)
		l.neverBlockDeadCode(n.Then)
		l.neverExprDeadCode(n.Else)
	case *ast.MatchExpr:
		l.neverExprDeadCode(n.Scrutinee)
		for _, arm := range n.Arms {
			l.neverExprDeadCode(arm.Body)
		}
	case *ast.ClosureExpr:
		l.neverExprDeadCode(n.Body)
	}
}

// stmtIsNeverTyped reports whether the statement's effective value
// type is Never. Type info makes this precise; when no type checker is
// available we fall back to a conventional-name heuristic matching
// `panic(...)`, `exit(...)`, `abort(...)`, `unreachable(...)`, `todo(...)`
// at the top level of the expression statement.
func (l *linter) stmtIsNeverTyped(s ast.Stmt) bool {
	es, ok := s.(*ast.ExprStmt)
	if !ok {
		return false
	}
	if l.check != nil {
		if t := l.check.LookupType(es.X); t != nil && types.IsNever(t) {
			return true
		}
	}
	return isConventionalNeverCall(es.X)
}

// isConventionalNeverCall matches `panic(...)`, `exit(...)`, `abort(...)`,
// `unreachable(...)`, `todo(...)` where the callee is a bare identifier
// by that name. Qualified calls (`os.Exit`, `pkg.panic`) are not
// recognised here because the convention is project-local and false
// positives on user-defined `exit` functions are a real risk.
func isConventionalNeverCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := call.Fn.(*ast.Ident)
	if !ok {
		return false
	}
	switch id.Name {
	case "panic", "exit", "abort", "unreachable", "todo":
		return true
	}
	return false
}
