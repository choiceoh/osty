package lsp

import (
	"github.com/osty/osty/internal/lexer"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
)

// findNameOffset returns the byte offset of the name identifier
// within a declaration's source span. The parser doesn't record the
// name token's Pos separately (FnDecl.PosV points at `fn` / `pub`),
// so we re-lex the span and look for the first IDENT token whose
// lexeme matches `name`. Returns -1 when no such token exists —
// e.g. synthetic symbols or decls whose source is empty.
//
// Keeping the search local to the decl span means renaming doesn't
// drift into surrounding declarations even for packages where the
// same name appears in multiple files.
func findNameOffset(src []byte, declPos, declEnd int, name string) int {
	if name == "" || declPos < 0 || declEnd > len(src) || declEnd <= declPos {
		return -1
	}
	l := lexer.New(src[declPos:declEnd])
	for _, t := range l.Lex() {
		if t.Kind == token.EOF {
			break
		}
		if t.Kind == token.IDENT && t.Value == name {
			return declPos + t.Pos.Offset
		}
	}
	return -1
}

// nameRangeForSymbol resolves a resolver Symbol to the byte range of
// its declared name inside `src`. Returns ok=false for builtins, for
// symbols without a source decl, or when the lexer fails to locate
// the name (shouldn't happen for well-formed decls).
func nameRangeForSymbol(src []byte, sym *resolve.Symbol) (start, end int, ok bool) {
	if sym == nil || sym.Decl == nil {
		return 0, 0, false
	}
	declStart := sym.Decl.Pos().Offset
	declEnd := sym.Decl.End().Offset
	off := findNameOffset(src, declStart, declEnd, sym.Name)
	if off < 0 {
		return 0, 0, false
	}
	return off, off + len(sym.Name), true
}
