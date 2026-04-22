// Package astbridge exposes token and string helpers used by the generated bridge.
package astbridge

import (
	"unicode/utf8"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/token"
)

func TokenAt(toks []Token, idx int) Token {
	if idx >= 0 && idx < len(toks) {
		return toks[idx]
	}
	if idx < 0 && len(toks) > 0 {
		return toks[0]
	}
	if len(toks) > 0 {
		return toks[len(toks)-1]
	}
	return token.Token{Pos: token.Pos{Line: 1, Column: 1}, End: token.Pos{Line: 1, Column: 1}}
}

func TokenKind(tok Token) Kind         { return tok.Kind }
func TokenPos(tok Token) Pos           { return tok.Pos }
func TokenEnd(tok Token) Pos           { return tok.End }
func ZeroPos() Pos                     { return token.Pos{} }
func TokenValue(tok Token) string      { return tok.Value }
func TokenLeadingDoc(tok Token) string { return tok.LeadingDoc }
func TokenIsPub(tok Token) bool        { return tok.Kind == token.PUB }
func TokenIsIdent(tok Token) bool      { return tok.Kind == token.IDENT }
func TokenIsString(tok Token) bool     { return tok.Kind == token.STRING || tok.Kind == token.RAWSTRING }
func TokenIsDot(tok Token) bool        { return tok.Kind == token.DOT }
func TokenIsSlash(tok Token) bool      { return tok.Kind == token.SLASH }
func TokenIsColon(tok Token) bool      { return tok.Kind == token.COLON }
func TokenIsLBrace(tok Token) bool     { return tok.Kind == token.LBRACE }
func TokenIsNewline(tok Token) bool    { return tok.Kind == token.NEWLINE }
func TokenIsEOF(tok Token) bool        { return tok.Kind == token.EOF }
func TokenKindString(tok Token) string { return tok.Kind.String() }

func stringPartsToAST(parts []token.StringPart) []ast.StringPart {
	if len(parts) == 0 {
		return []ast.StringPart{{IsLit: true}}
	}
	out := make([]ast.StringPart, 0, len(parts))
	for _, p := range parts {
		if p.Kind == token.PartText {
			out = append(out, ast.StringPart{IsLit: true, Lit: p.Text})
			continue
		}
		var expr ast.Expr
		if ParseInterpolatedExpr != nil {
			expr = ParseInterpolatedExpr(p.Expr)
		}
		if expr == nil && len(p.Expr) > 0 {
			expr = &ast.Ident{PosV: p.Expr[0].Pos, EndV: p.Expr[len(p.Expr)-1].End, Name: "__interp"}
		}
		out = append(out, ast.StringPart{Expr: expr})
	}
	return out
}

var ParseInterpolatedExpr func([]Token) Expr

func RuneString(value int) string {
	return string(rune(value))
}

func BreakStmtLabelNode(pos, end Pos, label string, value Expr) Stmt {
	return BreakStmtValueNode(pos, end, label, ZeroPos(), ZeroPos(), value)
}

func ContinueStmtLabelNode(pos, end Pos, label string) Stmt {
	return ContinueStmtNode(pos, end, label, ZeroPos(), ZeroPos())
}

func ForStmtLabelNode(pos, end Pos, label string, isForLet bool, pat Pattern, iter Expr, body Block) Stmt {
	return ForStmtNode(pos, end, label, ZeroPos(), ZeroPos(), isForLet, pat, iter, body)
}

func LoopExprLabelNode(pos, end Pos, label string, body Block) Expr {
	return LoopExprNode(pos, end, label, ZeroPos(), ZeroPos(), body)
}

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

func firstByte(s string) byte {
	if s == "" {
		return 0
	}
	return s[0]
}
