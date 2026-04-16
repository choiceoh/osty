package selfhost

import (
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost/astbridge"
	"github.com/osty/osty/internal/token"
)

func init() {
	astbridge.ParseInterpolatedExpr = parseInterpolatedExpr
}

func parseInterpolatedExpr(toks []token.Token) ast.Expr {
	if len(toks) == 0 {
		return nil
	}
	src := interpolatedTokensSource(toks)
	if src == "" {
		return nil
	}
	file, diags := Parse([]byte("let __interp = " + src))
	if !hasDiagnosticError(diags) && file != nil && len(file.Stmts) > 0 {
		if ls, ok := file.Stmts[0].(*ast.LetStmt); ok {
			return ls.Value
		}
	}
	if expr := astLowerInterpolatedTokensToExpr(toks); !astbridge.IsNilExpr(expr) {
		return expr
	}
	if len(toks) > 0 {
		return &ast.Ident{PosV: toks[0].Pos, EndV: toks[len(toks)-1].End, Name: "__interp"}
	}
	return nil
}

func hasDiagnosticError(diags []*diag.Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == diag.Error {
			return true
		}
	}
	return false
}

func interpolatedTokensSource(toks []token.Token) string {
	var b strings.Builder
	for _, tk := range toks {
		if tk.Kind == token.EOF || tk.Kind == token.NEWLINE {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		if tk.Value != "" {
			b.WriteString(tk.Value)
		} else {
			b.WriteString(tk.Kind.String())
		}
	}
	return b.String()
}
