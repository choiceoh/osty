package lint

import (
	"fmt"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
)

// sprint concatenates its arguments with fmt.Sprint for concise label
// building.
func sprint(a ...any) string { return fmt.Sprint(a...) }

// Default thresholds for the complexity family. These are intentionally
// conservative (higher than clippy's defaults) so typical code doesn't
// trip them; users who want stricter limits can tighten via config
// once threshold knobs are exposed.
const (
	defaultMaxParams  = 7
	defaultMaxBodyLen = 80
	defaultMaxNesting = 5
)

// lintComplexity runs the L0050 / L0052 / L0053 checks over every
// function / method body in the file.
func (l *linter) lintComplexity() {
	for _, d := range l.file.Decls {
		l.complexityDecl(d)
	}
}

func (l *linter) complexityDecl(d ast.Decl) {
	switch n := d.(type) {
	case *ast.FnDecl:
		l.complexityFn(n)
	case *ast.StructDecl:
		for _, m := range n.Methods {
			l.complexityFn(m)
		}
	case *ast.EnumDecl:
		for _, m := range n.Methods {
			l.complexityFn(m)
		}
	case *ast.InterfaceDecl:
		for _, m := range n.Methods {
			l.complexityFn(m)
		}
	}
}

func (l *linter) complexityFn(fn *ast.FnDecl) {
	if fn == nil {
		return
	}
	// L0050: too many parameters. Count excludes the implicit `self`
	// receiver. `pub` functions are still flagged because param count
	// is an API ergonomics concern.
	if cnt := len(fn.Params); cnt > defaultMaxParams {
		l.emit(diag.New(diag.Warning,
			"function takes too many parameters").
			Code(diag.CodeTooManyParams).
			Primary(diag.Span{Start: fn.PosV, End: fn.EndV},
				formatParamCount(cnt)).
			Note("long parameter lists are hard to call correctly; group them into a struct or split the function").
			Hint("extract related parameters into a struct, or refactor the function").
			Build())
	}
	if fn.Body == nil {
		return
	}
	// L0052: function body too long (total statement count, recursive).
	if stmts := countStmts(fn.Body); stmts > defaultMaxBodyLen {
		l.emit(diag.New(diag.Warning,
			"function body is too long").
			Code(diag.CodeFunctionTooLong).
			Primary(diag.Span{Start: fn.Body.PosV, End: fn.Body.EndV},
				formatBodyLen(stmts)).
			Hint("extract cohesive chunks into helper functions").
			Build())
	}
	// L0053: deep nesting.
	if depth := blockMaxDepth(fn.Body, 0); depth > defaultMaxNesting {
		l.emit(diag.New(diag.Warning,
			"control-flow nesting is too deep").
			Code(diag.CodeDeepNesting).
			Primary(diag.Span{Start: fn.Body.PosV, End: fn.Body.EndV},
				formatNesting(depth)).
			Hint("flatten with early returns, or extract inner branches into helpers").
			Build())
	}
}

// countStmts counts every statement reachable from b (recursively
// including nested blocks / if branches / loop bodies / match arms).
func countStmts(b *ast.Block) int {
	if b == nil {
		return 0
	}
	n := len(b.Stmts)
	for _, s := range b.Stmts {
		n += stmtChildStmts(s)
	}
	return n
}

func stmtChildStmts(s ast.Stmt) int {
	switch n := s.(type) {
	case *ast.ForStmt:
		return countStmts(n.Body)
	case *ast.ExprStmt:
		return exprChildStmts(n.X)
	case *ast.LetStmt:
		return exprChildStmts(n.Value)
	case *ast.AssignStmt:
		return exprChildStmts(n.Value)
	case *ast.DeferStmt:
		return exprChildStmts(n.X)
	case *ast.ReturnStmt:
		return exprChildStmts(n.Value)
	case *ast.Block:
		return countStmts(n)
	}
	return 0
}

func exprChildStmts(e ast.Expr) int {
	if e == nil {
		return 0
	}
	switch n := e.(type) {
	case *ast.Block:
		return countStmts(n)
	case *ast.IfExpr:
		total := countStmts(n.Then)
		if b, ok := n.Else.(*ast.Block); ok {
			total += countStmts(b)
		} else {
			total += exprChildStmts(n.Else)
		}
		return total
	case *ast.MatchExpr:
		total := 0
		for _, arm := range n.Arms {
			total += exprChildStmts(arm.Body)
		}
		return total
	case *ast.ClosureExpr:
		return exprChildStmts(n.Body)
	}
	return 0
}

// blockMaxDepth returns the maximum nesting depth reachable from b,
// where every `*ast.Block` that contains flow-control (if / for /
// match) adds one level.
func blockMaxDepth(b *ast.Block, cur int) int {
	if b == nil {
		return cur
	}
	max := cur
	for _, s := range b.Stmts {
		if d := stmtDepth(s, cur+1); d > max {
			max = d
		}
	}
	return max
}

func stmtDepth(s ast.Stmt, cur int) int {
	switch n := s.(type) {
	case *ast.ForStmt:
		return blockMaxDepth(n.Body, cur)
	case *ast.ExprStmt:
		return exprDepth(n.X, cur)
	case *ast.LetStmt:
		return exprDepth(n.Value, cur)
	case *ast.AssignStmt:
		return exprDepth(n.Value, cur)
	case *ast.DeferStmt:
		return exprDepth(n.X, cur)
	case *ast.ReturnStmt:
		return exprDepth(n.Value, cur)
	case *ast.Block:
		return blockMaxDepth(n, cur)
	}
	return cur
}

func exprDepth(e ast.Expr, cur int) int {
	if e == nil {
		return cur
	}
	switch n := e.(type) {
	case *ast.Block:
		return blockMaxDepth(n, cur)
	case *ast.IfExpr:
		thenD := blockMaxDepth(n.Then, cur)
		elseD := cur
		if b, ok := n.Else.(*ast.Block); ok {
			elseD = blockMaxDepth(b, cur)
		} else {
			elseD = exprDepth(n.Else, cur)
		}
		if thenD > elseD {
			return thenD
		}
		return elseD
	case *ast.MatchExpr:
		max := cur
		for _, arm := range n.Arms {
			if d := exprDepth(arm.Body, cur+1); d > max {
				max = d
			}
		}
		return max
	case *ast.ClosureExpr:
		return exprDepth(n.Body, cur)
	}
	return cur
}

// formatParamCount returns the label "this function takes N parameters
// (max %d recommended)".
func formatParamCount(cnt int) string {
	return sprint("takes ", cnt, " parameters (recommended limit: ", defaultMaxParams, ")")
}

func formatBodyLen(n int) string {
	return sprint(n, " statements (recommended limit: ", defaultMaxBodyLen, ")")
}

func formatNesting(d int) string {
	return sprint(d, " levels deep (recommended limit: ", defaultMaxNesting, ")")
}
