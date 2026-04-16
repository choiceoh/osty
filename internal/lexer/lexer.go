// Package lexer exposes the compiler's tokenization API.
package lexer

import (
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/token"
)

// Error is a lexical error with position. Retained as a thin alias over a
// diag.Diagnostic for callers that only need a position+message.
type Error = diag.Diagnostic

// Lexer is a small compatibility wrapper around the bootstrapped pure-Osty
// lexer. It keeps comments and diagnostics available to older callers while
// the token stream itself comes from internal/selfhost.
type Lexer struct {
	src      []byte
	comments []token.Comment
	errs     []*diag.Diagnostic
	toks     []token.Token
}

// New returns a lexer over src. The source must be UTF-8 encoded.
func New(src []byte) *Lexer {
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		src = src[3:]
	}
	src = normalizeNewlines(src)
	return &Lexer{src: src}
}

func normalizeNewlines(src []byte) []byte {
	if !hasCR(src) {
		return src
	}
	out := make([]byte, 0, len(src))
	for i := 0; i < len(src); i++ {
		if src[i] == '\r' {
			if i+1 < len(src) && src[i+1] == '\n' {
				continue
			}
			out = append(out, '\n')
			continue
		}
		out = append(out, src[i])
	}
	return out
}

func hasCR(src []byte) bool {
	for i := 0; i < len(src); i++ {
		if src[i] == '\r' {
			return true
		}
	}
	return false
}

// IsIdentStart reports whether b can begin an identifier in byte-oriented
// editor helpers. Non-ASCII bytes are accepted here; the lexer proper
// validates UTF-8 and Unicode categories.
func IsIdentStart(b byte) bool {
	return b == '_' || b >= 0x80 || ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
}

// IsIdentCont reports whether b can continue an identifier.
func IsIdentCont(b byte) bool {
	return IsIdentStart(b) || ('0' <= b && b <= '9')
}

// Errors returns any lexical errors collected so far.
func (l *Lexer) Errors() []*diag.Diagnostic { return l.errs }

// Comments returns every comment (line, block, doc) the lexer saw, in source
// order.
func (l *Lexer) Comments() []token.Comment { return l.comments }

// Lex returns the complete token stream, terminated by EOF.
func (l *Lexer) Lex() []token.Token {
	l.toks, l.errs, l.comments = selfhost.Lex(l.src)
	return l.toks
}
