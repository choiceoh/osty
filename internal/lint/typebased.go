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
