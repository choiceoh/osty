// Package lexer scans an Osty source file into a stream of tokens.
// This package is a thin facade over internal/selfhost.
package lexer

import (
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/token"
)

// Error is a lexical error. Retained as a thin alias over diag.Diagnostic.
type Error = diag.Diagnostic

// Lexer wraps the self-hosted front end and exposes the same surface as the
// former hand-written Go lexer.
type Lexer struct {
	toks     []token.Token
	errs     []*diag.Diagnostic
	comments []token.Comment
}

// New returns a Lexer over src.
func New(src []byte) *Lexer {
	toks, errs, comments := selfhost.Lex(src)
	return &Lexer{toks: toks, errs: errs, comments: comments}
}

// Lex returns the complete token stream, terminated by EOF.
func (l *Lexer) Lex() []token.Token {
	return l.toks
}

// Errors returns lexical errors.
func (l *Lexer) Errors() []*diag.Diagnostic {
	return l.errs
}

// Comments returns every comment (line, block, doc) in source order.
func (l *Lexer) Comments() []token.Comment {
	return l.comments
}

// IsIdentStart reports whether b is a valid first byte of an Osty identifier
// (ASCII letter or underscore).
func IsIdentStart(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

// IsIdentCont reports whether b is a valid continuation byte of an Osty
// identifier (IsIdentStart ∪ ASCII digit).
func IsIdentCont(b byte) bool {
	return IsIdentStart(b) || (b >= '0' && b <= '9')
}
