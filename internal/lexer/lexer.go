// Package lexer scans an Osty source file into a stream of tokens.
package lexer

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// Error is a lexical error with position. Retained as a thin alias over a
// diag.Diagnostic for callers that only need a position+message.
type Error = diag.Diagnostic

// Lexer produces tokens from a UTF-8 source buffer.
type Lexer struct {
	src    []byte
	offset int
	line   int
	col    int

	// insertTerm is true when the previous emitted non-newline token is one
	// that permits a newline to act as an implicit statement terminator.
	insertTerm bool
	// parenDepth counts unmatched "(" and "[". Newlines inside are ignored.
	parenDepth int

	// pendingDoc accumulates `///` doc-comment lines seen during whitespace
	// scanning. It is attached to the LeadingDoc field of the next real
	// (non-NEWLINE, non-EOF) token and then cleared. A blank line or any
	// other content resets the buffer.
	pendingDoc []string
	// docLastLine is the line number where the last `///` was seen, used
	// to detect blank-line separation.
	docLastLine int

	// comments is every comment — line, block, and doc — discovered in
	// source order. The parser ignores this slice entirely; the formatter
	// retrieves it via Comments() to avoid a second scan over src.
	comments []token.Comment

	errs []*diag.Diagnostic
}

// New returns a lexer over src. The source must be UTF-8 encoded.
func New(src []byte) *Lexer {
	// Normalize CR variants to \n: `\r\n` collapses to `\n`, and a
	// lone `\r` (classic-Mac line ending, or a stray in a comment)
	// also becomes `\n`. §1.1 names `\r\n` explicitly; the lone case
	// is handled the same way so no stray `\r` survives into comment
	// or string text where it would break idempotent formatting.
	src = normalizeNewlines(src)
	return &Lexer{src: src, line: 1, col: 1}
}

func normalizeNewlines(src []byte) []byte {
	if !hasCR(src) {
		return src
	}
	out := make([]byte, 0, len(src))
	for i := 0; i < len(src); i++ {
		if src[i] == '\r' {
			if i+1 < len(src) && src[i+1] == '\n' {
				continue // drop the \r of \r\n
			}
			out = append(out, '\n') // lone \r → \n
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

// Errors returns any lexical errors collected so far.
func (l *Lexer) Errors() []*diag.Diagnostic { return l.errs }

// Comments returns every comment (line, block, doc) the lexer saw, in
// source order. Doc comments appear both here and attached to the
// following token's LeadingDoc — callers that only want the
// token-attached form can filter by Kind, and vice versa.
func (l *Lexer) Comments() []token.Comment { return l.comments }

// Lex returns the complete token stream, terminated by EOF.
func (l *Lexer) Lex() []token.Token {
	l.skipShebang()
	var toks []token.Token
	for {
		t := l.next()
		toks = append(toks, t)
		if t.Kind == token.EOF {
			return toks
		}
	}
}

func (l *Lexer) skipShebang() {
	if len(l.src) >= 2 && l.src[0] == '#' && l.src[1] == '!' {
		for l.offset < len(l.src) && l.src[l.offset] != '\n' {
			l.offset++
			l.col++
		}
	}
}

func (l *Lexer) pos() token.Pos {
	return token.Pos{Offset: l.offset, Line: l.line, Column: l.col}
}

func (l *Lexer) peek() byte {
	if l.offset >= len(l.src) {
		return 0
	}
	return l.src[l.offset]
}

func (l *Lexer) peekAt(n int) byte {
	if l.offset+n >= len(l.src) {
		return 0
	}
	return l.src[l.offset+n]
}

// advance consumes one Unicode code point at l.offset and returns its
// leading byte. Multi-byte sequences advance offset by the full rune
// width but count as one column. Callers that need the rune value use
// advanceRune() instead — this form exists for byte-level inspection
// paths (punctuation, keywords, number literals) where decoding a rune
// would be wasted work.
//
// Keep in sync with advanceRune(): the ASCII fast path short-circuits
// utf8.DecodeRune on the common case, and both helpers must apply the
// same line/col bookkeeping for peek-and-restore to stay consistent.
func (l *Lexer) advance() byte {
	if l.offset >= len(l.src) {
		return 0
	}
	b := l.src[l.offset]
	if b < 0x80 {
		l.offset++
		if b == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
		return b
	}
	_, sz := utf8.DecodeRune(l.src[l.offset:])
	if sz == 0 {
		sz = 1
	}
	l.offset += sz
	l.col++
	return b
}

// advanceRune consumes one Unicode code point at l.offset and returns
// the rune. Use for text scanning (string bodies, char literals);
// advance() is cheaper when only the leading byte is needed.
//
// Keep in sync with advance(): see the note there.
func (l *Lexer) advanceRune() rune {
	if l.offset >= len(l.src) {
		return 0
	}
	b := l.src[l.offset]
	if b < 0x80 {
		l.offset++
		if b == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
		return rune(b)
	}
	r, sz := utf8.DecodeRune(l.src[l.offset:])
	if sz == 0 {
		sz = 1
	}
	l.offset += sz
	l.col++
	return r
}

func (l *Lexer) errorf(p token.Pos, format string, args ...any) {
	l.errs = append(l.errs, diag.New(diag.Error, fmt.Sprintf(format, args...)).
		PrimaryPos(p, "").
		Build())
}

// errorCode reports a lex error tagged with a stable code and an optional
// hint. The span is [start, end).
func (l *Lexer) errorCode(start, end token.Pos, code, msg, hint string) {
	b := diag.New(diag.Error, msg).
		Code(code).
		Primary(diag.Span{Start: start, End: end}, "")
	if hint != "" {
		b.Hint(hint)
	}
	l.errs = append(l.errs, b.Build())
}

// next returns the next token, applying newline-as-terminator rules.
func (l *Lexer) next() token.Token {
	for {
		l.skipSpacesAndComments()
		if l.offset >= len(l.src) {
			pos := l.pos()
			// Emit trailing NEWLINE if needed before EOF.
			if l.insertTerm {
				l.insertTerm = false
				return token.Token{Kind: token.NEWLINE, Pos: pos, End: pos}
			}
			return token.Token{Kind: token.EOF, Pos: pos, End: pos}
		}

		if l.peek() == '\n' {
			pos := l.pos()
			l.advance()
			if l.parenDepth == 0 && l.insertTerm {
				// v0.2 R2 case 2: suppress NEWLINE if the next non-
				// whitespace token would be one that demands a left
				// operand (binary op) or otherwise continues the
				// preceding expression (`.`, `?.`, `,`, `)`, `]`, `}`,
				// `..`, `..=`, `??`).
				if l.nextTokenSuppressesTerm() {
					continue
				}
				l.insertTerm = false
				return token.Token{Kind: token.NEWLINE, Pos: pos, End: l.pos()}
			}
			continue
		}

		tok := l.scanToken()
		if len(l.pendingDoc) > 0 {
			// Doc comments only attach when the next real token sits on
			// the line immediately after the last `///` line — any blank
			// line detaches the comment.
			if tok.Pos.Line == l.docLastLine+1 {
				tok.LeadingDoc = strings.Join(l.pendingDoc, "\n")
			}
			l.pendingDoc = nil
		}
		return tok
	}
}

// skipSpacesAndComments skips spaces, tabs, and comments. It does not
// skip newlines (those are handled by next). Doc comments (`///`) are
// accumulated into pendingDoc for attachment to the next real token;
// every comment (line, block, doc) is also appended to l.comments for
// retrieval via Lexer.Comments().
func (l *Lexer) skipSpacesAndComments() {
	for l.offset < len(l.src) {
		b := l.peek()
		switch b {
		case ' ', '\t':
			l.advance()
		case '/':
			nx := l.peekAt(1)
			if nx == '/' {
				// Distinguish doc comment `///` from regular `//`.
				if l.peekAt(2) == '/' && l.peekAt(3) != '/' {
					l.scanDocComment()
					continue
				}
				l.scanLineComment()
			} else if nx == '*' {
				l.scanBlockComment()
			} else {
				return
			}
		default:
			return
		}
	}
}

// scanDocComment consumes a `///` line, accumulates it into pendingDoc
// for attachment to the next real token, and also records a CommentDoc
// entry in l.comments so downstream consumers (formatter) see it.
func (l *Lexer) scanDocComment() {
	startPos := l.pos()
	docStartLine := l.line
	// Reset pendingDoc if the previous doc line was non-adjacent — per
	// §1.5, doc comments must immediately precede the declaration with
	// no blank line between.
	if len(l.pendingDoc) > 0 && l.docLastLine+1 != docStartLine {
		l.pendingDoc = nil
	}
	l.advance() // /
	l.advance() // /
	l.advance() // /
	// Strip a single optional leading space, matching Rust/Swift.
	if l.peek() == ' ' {
		l.advance()
	}
	textStart := l.offset
	for l.offset < len(l.src) && l.peek() != '\n' {
		l.advance()
	}
	text := string(l.src[textStart:l.offset])
	l.pendingDoc = append(l.pendingDoc, text)
	l.docLastLine = docStartLine
	l.comments = append(l.comments, token.Comment{
		Kind:    token.CommentDoc,
		Pos:     startPos,
		Text:    text,
		EndLine: docStartLine,
	})
}

// scanLineComment consumes a `//` line, records it, and resets any
// pending doc-comment buffer (a regular line comment is non-doc
// content and detaches an accumulating doc run).
func (l *Lexer) scanLineComment() {
	startPos := l.pos()
	l.pendingDoc = nil
	l.advance() // /
	l.advance() // /
	if l.peek() == ' ' {
		l.advance()
	}
	textStart := l.offset
	for l.offset < len(l.src) && l.peek() != '\n' {
		l.advance()
	}
	l.comments = append(l.comments, token.Comment{
		Kind:    token.CommentLine,
		Pos:     startPos,
		Text:    string(l.src[textStart:l.offset]),
		EndLine: startPos.Line,
	})
}

// scanBlockComment consumes a `/* ... */` block, which may span lines.
// Non-nesting per §1.5. Unterminated blocks emit a lex error and abort
// the skip.
func (l *Lexer) scanBlockComment() {
	startPos := l.pos()
	l.advance() // /
	l.advance() // *
	textStart := l.offset
	for {
		if l.offset >= len(l.src) {
			l.errorf(startPos, "unterminated block comment")
			return
		}
		if l.peek() == '*' && l.peekAt(1) == '/' {
			textEnd := l.offset
			endLine := l.line
			l.advance() // *
			l.advance() // /
			l.comments = append(l.comments, token.Comment{
				Kind:    token.CommentBlock,
				Pos:     startPos,
				Text:    string(l.src[textStart:textEnd]),
				EndLine: endLine,
			})
			return
		}
		l.advance()
	}
}

// scanToken scans a single non-whitespace, non-newline token.
func (l *Lexer) scanToken() token.Token {
	start := l.pos()
	b := l.peek()
	switch {
	case IsIdentStart(b):
		return l.scanIdentOrKeyword(start)
	case b >= '0' && b <= '9':
		return l.scanNumber(start)
	case b == '"':
		return l.scanString(start, false)
	case b == '\'':
		return l.scanChar(start)
	case b == 'r' && l.peekAt(1) == '"':
		// handled in scanIdentOrKeyword? No — that expects identifier chars
		// only. 'r' alone would start an identifier. But rXXX with quote:
		// detect before falling into ident. In practice 'r' followed by '"'
		// is a raw string.
		return l.scanRawString(start)
	case b == 'b' && l.peekAt(1) == '\'':
		return l.scanByte(start)
	}

	// Check r"..." before generic ident (IsIdentStart catches 'r'). Move the
	// check above.
	return l.scanPunct(start)
}

// IsIdentStart reports whether b is a valid first byte of an Osty
// identifier (ASCII letter or underscore). Exported so other
// front-end components (LSP text scanning, formatter heuristics) can
// reuse the same rule without maintaining a parallel copy.
func IsIdentStart(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

// IsIdentCont reports whether b is a valid continuation byte of an
// Osty identifier (IsIdentStart ∪ ASCII digit).
func IsIdentCont(b byte) bool {
	return IsIdentStart(b) || (b >= '0' && b <= '9')
}

func (l *Lexer) scanIdentOrKeyword(start token.Pos) token.Token {
	// Handle r"..." and b'...' before falling into ident scan.
	if l.peek() == 'r' && l.peekAt(1) == '"' {
		return l.scanRawString(start)
	}
	if l.peek() == 'b' && l.peekAt(1) == '\'' {
		return l.scanByte(start)
	}
	from := l.offset
	for l.offset < len(l.src) && IsIdentCont(l.peek()) {
		l.advance()
	}
	lex := string(l.src[from:l.offset])
	end := l.pos()
	kind := token.LookupKeyword(lex)
	if kind == token.IDENT && lex == "_" {
		kind = token.UNDERSCORE
		l.setInsertTerm(kind)
		return token.Token{Kind: kind, Pos: start, End: end, Value: lex}
	}
	l.setInsertTerm(kind)
	if kind != token.IDENT {
		return token.Token{Kind: kind, Pos: start, End: end, Value: lex}
	}
	return token.Token{Kind: token.IDENT, Pos: start, End: end, Value: lex}
}

// scanNumber parses an integer or float literal, permitting underscores and
// base prefixes 0x, 0b, 0o.
func (l *Lexer) scanNumber(start token.Pos) token.Token {
	from := l.offset
	isFloat := false
	// v0.2 R11: only lowercase base prefixes are accepted. The lexer
	// reports an error if uppercase variants appear and continues lexing
	// using the lowercase rule.
	if l.peek() == '0' && (l.peekAt(1) == 'x' || l.peekAt(1) == 'X') {
		if l.peekAt(1) == 'X' {
			l.errorCode(l.pos(), token.Pos{Line: l.line, Column: l.col + 2, Offset: l.offset + 2},
				diag.CodeUppercaseBasePrefix,
				"uppercase base prefix `0X` is not allowed",
				"use lowercase: `0x`. v0.2 R11 requires lowercase base prefixes.")
		}
		l.advance()
		l.advance()
		for l.offset < len(l.src) && (isHex(l.peek()) || l.peek() == '_') {
			l.advance()
		}
		tok := token.Token{Kind: token.INT, Pos: start, End: l.pos(), Value: string(l.src[from:l.offset])}
		l.setInsertTerm(token.INT)
		return tok
	}
	if l.peek() == '0' && (l.peekAt(1) == 'b' || l.peekAt(1) == 'B') {
		if l.peekAt(1) == 'B' {
			l.errorCode(l.pos(), token.Pos{Line: l.line, Column: l.col + 2, Offset: l.offset + 2},
				diag.CodeUppercaseBasePrefix,
				"uppercase base prefix `0B` is not allowed",
				"use lowercase: `0b`. v0.2 R11 requires lowercase base prefixes.")
		}
		l.advance()
		l.advance()
		for l.offset < len(l.src) && (l.peek() == '0' || l.peek() == '1' || l.peek() == '_') {
			l.advance()
		}
		tok := token.Token{Kind: token.INT, Pos: start, End: l.pos(), Value: string(l.src[from:l.offset])}
		l.setInsertTerm(token.INT)
		return tok
	}
	if l.peek() == '0' && (l.peekAt(1) == 'o' || l.peekAt(1) == 'O') {
		if l.peekAt(1) == 'O' {
			l.errorCode(l.pos(), token.Pos{Line: l.line, Column: l.col + 2, Offset: l.offset + 2},
				diag.CodeUppercaseBasePrefix,
				"uppercase base prefix `0O` is not allowed",
				"use lowercase: `0o`. v0.2 R11 requires lowercase base prefixes.")
		}
		l.advance()
		l.advance()
		for l.offset < len(l.src) && ((l.peek() >= '0' && l.peek() <= '7') || l.peek() == '_') {
			l.advance()
		}
		tok := token.Token{Kind: token.INT, Pos: start, End: l.pos(), Value: string(l.src[from:l.offset])}
		l.setInsertTerm(token.INT)
		return tok
	}

	for l.offset < len(l.src) && (isDigit(l.peek()) || l.peek() == '_') {
		l.advance()
	}
	// Fractional part. Must be digit after '.', because `0..10` uses `..`.
	if l.peek() == '.' && isDigit(l.peekAt(1)) {
		isFloat = true
		l.advance() // .
		for l.offset < len(l.src) && (isDigit(l.peek()) || l.peek() == '_') {
			l.advance()
		}
	}
	// Exponent.
	if l.peek() == 'e' || l.peek() == 'E' {
		isFloat = true
		l.advance()
		if l.peek() == '+' || l.peek() == '-' {
			l.advance()
		}
		for l.offset < len(l.src) && (isDigit(l.peek()) || l.peek() == '_') {
			l.advance()
		}
	}
	kind := token.INT
	if isFloat {
		kind = token.FLOAT
	}
	tok := token.Token{Kind: kind, Pos: start, End: l.pos(), Value: string(l.src[from:l.offset])}
	l.setInsertTerm(kind)
	return tok
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
func isHex(b byte) bool {
	return isDigit(b) || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// scanString reads a "..." string, possibly triple-quoted, with interpolation.
func (l *Lexer) scanString(start token.Pos, _raw bool) token.Token {
	// Detect triple-quoted.
	if l.peek() == '"' && l.peekAt(1) == '"' && l.peekAt(2) == '"' {
		return l.scanTripleString(start, false)
	}
	l.advance() // opening "
	var parts []token.StringPart
	var buf strings.Builder
	for {
		if l.offset >= len(l.src) {
			l.errorf(start, "unterminated string literal")
			break
		}
		b := l.peek()
		if b == '"' {
			l.advance()
			break
		}
		if b == '\n' {
			l.errorf(l.pos(), "newline in string literal (use triple-quoted for multi-line)")
			break
		}
		if b == '\\' {
			l.advance()
			r, ok := l.decodeEscape()
			if !ok {
				continue
			}
			buf.WriteRune(r)
			continue
		}
		if b == '{' {
			if buf.Len() > 0 {
				parts = append(parts, token.StringPart{Kind: token.PartText, Text: buf.String()})
				buf.Reset()
			}
			interpToks := l.scanInterpolation()
			parts = append(parts, token.StringPart{Kind: token.PartExpr, Expr: interpToks})
			continue
		}
		buf.WriteRune(l.advanceRune())
	}
	if buf.Len() > 0 {
		parts = append(parts, token.StringPart{Kind: token.PartText, Text: buf.String()})
	}
	end := l.pos()
	l.setInsertTerm(token.STRING)
	return token.Token{Kind: token.STRING, Pos: start, End: end, Parts: parts}
}

// tripleLine collects the parts accumulated for one content line of a
// triple-quoted string, plus the whitespace prefix that will be checked
// against the closing-line indent.
type tripleLine struct {
	indent string // leading whitespace of this line (only valid while blank)
	parts  []token.StringPart
	blank  bool // true while only whitespace has been seen
}

// scanTripleString scans """...""" (and r"""...""") in a single streaming
// pass. Interpolations are tokenized in-place via scanInterpolation so
// their reported positions point at the real source. Indentation handling
// follows §1.6.3: the whitespace prefix of the closing """ line is the
// common indent, stripped from every content line.
//
// The 'raw' flag disables escape processing and interpolation. Raw strings
// still undergo indentation stripping.
func (l *Lexer) scanTripleString(start token.Pos, raw bool) token.Token {
	// Consume opening """.
	l.advance()
	l.advance()
	l.advance()
	if l.peek() != '\n' {
		l.errorf(l.pos(), `triple-quoted string must have newline after opening """`)
	} else {
		l.advance()
	}

	var lines []tripleLine
	cur := tripleLine{blank: true}
	var text strings.Builder

	flushText := func() {
		if text.Len() > 0 {
			cur.parts = append(cur.parts, token.StringPart{
				Kind: token.PartText,
				Text: text.String(),
			})
			text.Reset()
		}
	}
	endLine := func() {
		flushText()
		lines = append(lines, cur)
		cur = tripleLine{blank: true}
	}

	for {
		if l.offset >= len(l.src) {
			l.errorf(start, "unterminated triple-quoted string")
			break
		}
		b := l.peek()

		// Closing `"""` may only appear on an otherwise-blank line. When
		// we hit it, the current line's accumulated whitespace is the
		// common indent prefix.
		if b == '"' && l.peekAt(1) == '"' && l.peekAt(2) == '"' && cur.blank {
			indent := cur.indent
			l.advance()
			l.advance()
			l.advance()
			return l.finalizeTripleString(start, lines, indent, raw)
		}

		if b == '\n' {
			l.advance()
			endLine()
			continue
		}

		if cur.blank && (b == ' ' || b == '\t') {
			cur.indent += string(rune(b))
			text.WriteByte(b)
			l.advance()
			continue
		}
		cur.blank = false

		if !raw && b == '\\' {
			l.advance()
			if r, ok := l.decodeEscape(); ok {
				text.WriteRune(r)
			}
			continue
		}

		if !raw && b == '{' {
			flushText()
			toks := l.scanInterpolation()
			cur.parts = append(cur.parts, token.StringPart{
				Kind: token.PartExpr,
				Expr: toks,
			})
			continue
		}

		text.WriteRune(l.advanceRune())
	}
	// Unterminated-EOF path: the error was already reported via errorf;
	// return an empty STRING token so the parser keeps going.
	l.setInsertTerm(token.STRING)
	return token.Token{Kind: token.STRING, Pos: start, End: l.pos()}
}

// finalizeTripleString assembles the collected content lines into the
// token's Parts after applying §1.6.3 indentation rules:
//   - Each non-blank content line must begin with the closing-line's
//     indent prefix, which is stripped.
//   - Blank lines produce an empty line.
//   - The trailing newline before the closing `"""` is removed by not
//     appending a newline after the last line.
//
// `raw` is threaded through so the emitted token carries the correct
// Kind (RAWSTRING for r"""...""" vs STRING for """...""").
func (l *Lexer) finalizeTripleString(start token.Pos, lines []tripleLine, indent string, raw bool) token.Token {
	var out []token.StringPart
	appendText := func(s string) {
		if s == "" {
			return
		}
		if n := len(out); n > 0 && out[n-1].Kind == token.PartText {
			out[n-1].Text += s
			return
		}
		out = append(out, token.StringPart{Kind: token.PartText, Text: s})
	}

	for i, line := range lines {
		if i > 0 {
			appendText("\n")
		}
		// Strip indent from the first PartText on the line.
		if len(line.parts) > 0 && line.parts[0].Kind == token.PartText {
			t := line.parts[0].Text
			switch {
			case strings.HasPrefix(t, indent):
				t = t[len(indent):]
			case line.blank:
				// Blank line that is shorter than the indent: allowed,
				// produces an empty line.
				t = ""
			default:
				l.errorf(start, "line in triple-quoted string does not match the closing-indent prefix")
			}
			appendText(t)
			for _, p := range line.parts[1:] {
				if p.Kind == token.PartText {
					appendText(p.Text)
				} else {
					out = append(out, p)
				}
			}
			continue
		}
		// No leading text part (line began with interpolation).
		for _, p := range line.parts {
			if p.Kind == token.PartText {
				appendText(p.Text)
			} else {
				out = append(out, p)
			}
		}
	}

	kind := token.STRING
	if raw {
		kind = token.RAWSTRING
	}
	l.setInsertTerm(kind)
	return token.Token{Kind: kind, Pos: start, End: l.pos(), Parts: out, Triple: true}
}

// scanRawString reads r"..." (no escapes, no interpolation). Also handles
// r"""...""" triple-raw strings.
func (l *Lexer) scanRawString(start token.Pos) token.Token {
	l.advance() // r
	if l.peek() == '"' && l.peekAt(1) == '"' && l.peekAt(2) == '"' {
		return l.scanTripleString(start, true)
	}
	l.advance() // "
	from := l.offset
	for {
		if l.offset >= len(l.src) {
			l.errorf(start, "unterminated raw string")
			break
		}
		if l.peek() == '"' {
			break
		}
		if l.peek() == '\n' {
			l.errorf(l.pos(), "newline in raw string (use r\"\"\"...\"\"\")")
			break
		}
		l.advanceRune()
	}
	text := string(l.src[from:l.offset])
	if l.offset < len(l.src) && l.peek() == '"' {
		l.advance()
	}
	end := l.pos()
	l.setInsertTerm(token.RAWSTRING)
	return token.Token{
		Kind:  token.RAWSTRING,
		Pos:   start,
		End:   end,
		Parts: []token.StringPart{{Kind: token.PartText, Text: text}},
	}
}

// scanInterpolation reads tokens until the matching } at the same nesting
// level. The opening '{' has already been consumed.
func (l *Lexer) scanInterpolation() []token.Token {
	l.advance() // consume {
	var toks []token.Token
	depth := 1
	for {
		l.skipSpacesAndComments()
		if l.offset >= len(l.src) {
			l.errorf(l.pos(), "unterminated interpolation in string")
			return toks
		}
		if l.peek() == '\n' {
			l.advance()
			continue
		}
		if l.peek() == '{' {
			depth++
			t := l.scanPunct(l.pos())
			toks = append(toks, t)
			continue
		}
		if l.peek() == '}' {
			depth--
			if depth == 0 {
				l.advance()
				return toks
			}
			t := l.scanPunct(l.pos())
			toks = append(toks, t)
			continue
		}
		t := l.scanToken()
		toks = append(toks, t)
	}
}

// scanChar reads a 'X' Char literal. The value is stored as UTF-8 text in
// Value; the parser decodes it to a rune.
func (l *Lexer) scanChar(start token.Pos) token.Token {
	l.advance() // opening '
	var r rune
	if l.peek() == '\\' {
		l.advance()
		rr, ok := l.decodeEscape()
		if !ok {
			r = 0xFFFD
		} else {
			r = rr
		}
	} else {
		r = l.advanceRune()
	}
	if l.peek() != '\'' {
		l.errorf(l.pos(), "expected closing ' in char literal")
	} else {
		l.advance()
	}
	end := l.pos()
	l.setInsertTerm(token.CHAR)
	return token.Token{Kind: token.CHAR, Pos: start, End: end, Value: string(r)}
}

// scanByte reads a b'X' Byte literal.
func (l *Lexer) scanByte(start token.Pos) token.Token {
	l.advance() // b
	l.advance() // '
	var val byte
	if l.peek() == '\\' {
		l.advance()
		r, ok := l.decodeEscape()
		if !ok || r > 0x7F {
			l.errorf(start, "byte literal must be ASCII")
			val = 0
		} else {
			val = byte(r)
		}
	} else {
		b := l.peek()
		if b >= 0x80 {
			l.errorf(start, "byte literal must be ASCII")
		}
		val = b
		l.advance()
	}
	if l.peek() != '\'' {
		l.errorf(l.pos(), "expected closing ' in byte literal")
	} else {
		l.advance()
	}
	end := l.pos()
	l.setInsertTerm(token.BYTE)
	return token.Token{Kind: token.BYTE, Pos: start, End: end, Value: string(val)}
}

// decodeEscape is called after a backslash. Returns the decoded rune and
// whether the escape was understood.
func (l *Lexer) decodeEscape() (rune, bool) {
	if l.offset >= len(l.src) {
		l.errorf(l.pos(), "incomplete escape sequence")
		return 0, false
	}
	b := l.peek()
	switch b {
	case 'n':
		l.advance()
		return '\n', true
	case 't':
		l.advance()
		return '\t', true
	case 'r':
		l.advance()
		return '\r', true
	case '0':
		l.advance()
		return 0, true
	case '\\':
		l.advance()
		return '\\', true
	case '\'':
		l.advance()
		return '\'', true
	case '"':
		l.advance()
		return '"', true
	case '{':
		l.advance()
		return '{', true
	case '}':
		l.advance()
		return '}', true
	case 'u':
		l.advance()
		if l.peek() != '{' {
			l.errorf(l.pos(), `expected '{' after \u`)
			return 0xFFFD, false
		}
		l.advance()
		var val rune
		for l.peek() != '}' && l.offset < len(l.src) {
			if !isHex(l.peek()) {
				l.errorf(l.pos(), "invalid hex digit in unicode escape")
				return 0xFFFD, false
			}
			val = val*16 + rune(hexVal(l.peek()))
			l.advance()
		}
		if l.peek() == '}' {
			l.advance()
		}
		if val >= 0xD800 && val <= 0xDFFF {
			l.errorCode(l.pos(), l.pos(),
				diag.CodeUnknownEscape,
				fmt.Sprintf("surrogate code point U+%04X is not a valid Unicode scalar", val),
				"v0.3 §2.1: surrogate code points (U+D800..U+DFFF) are not representable in Osty — they exist only to encode supplementary characters in UTF-16")
			return 0xFFFD, false
		}
		if !utf8.ValidRune(val) {
			l.errorCode(l.pos(), l.pos(),
				diag.CodeUnknownEscape,
				fmt.Sprintf("invalid Unicode scalar value U+%X", val),
				"valid scalar values are U+0..U+D7FF and U+E000..U+10FFFF")
			return 0xFFFD, false
		}
		return val, true
	}
	l.errorf(l.pos(), "unknown escape sequence \\%c", b)
	l.advance()
	return 0xFFFD, false
}

func decodeEscapeString(s string) (rune, int) {
	if s == "" {
		return 0, 0
	}
	switch s[0] {
	case 'n':
		return '\n', 1
	case 't':
		return '\t', 1
	case 'r':
		return '\r', 1
	case '0':
		return 0, 1
	case '\\':
		return '\\', 1
	case '\'':
		return '\'', 1
	case '"':
		return '"', 1
	case '{':
		return '{', 1
	case '}':
		return '}', 1
	case 'u':
		if len(s) < 3 || s[1] != '{' {
			return 0xFFFD, 1
		}
		var val rune
		i := 2
		for i < len(s) && s[i] != '}' {
			if !isHex(s[i]) {
				return 0xFFFD, i
			}
			val = val*16 + rune(hexVal(s[i]))
			i++
		}
		if i < len(s) && s[i] == '}' {
			return val, i + 1
		}
		return 0xFFFD, i
	}
	return rune(s[0]), 1
}

func hexVal(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	}
	return 0
}

// scanPunct scans an operator or punctuation token using longest match.
func (l *Lexer) scanPunct(start token.Pos) token.Token {
	b := l.peek()
	switch b {
	case '(':
		l.advance()
		l.parenDepth++
		l.setInsertTerm(token.LPAREN)
		return token.Token{Kind: token.LPAREN, Pos: start, End: l.pos()}
	case ')':
		l.advance()
		if l.parenDepth > 0 {
			l.parenDepth--
		}
		l.setInsertTerm(token.RPAREN)
		return token.Token{Kind: token.RPAREN, Pos: start, End: l.pos()}
	case '[':
		l.advance()
		l.parenDepth++
		l.setInsertTerm(token.LBRACKET)
		return token.Token{Kind: token.LBRACKET, Pos: start, End: l.pos()}
	case ']':
		l.advance()
		if l.parenDepth > 0 {
			l.parenDepth--
		}
		l.setInsertTerm(token.RBRACKET)
		return token.Token{Kind: token.RBRACKET, Pos: start, End: l.pos()}
	case '{':
		l.advance()
		l.setInsertTerm(token.LBRACE)
		return token.Token{Kind: token.LBRACE, Pos: start, End: l.pos()}
	case '}':
		l.advance()
		l.setInsertTerm(token.RBRACE)
		return token.Token{Kind: token.RBRACE, Pos: start, End: l.pos()}
	case ',':
		l.advance()
		l.setInsertTerm(token.COMMA)
		return token.Token{Kind: token.COMMA, Pos: start, End: l.pos()}
	case ';':
		l.advance()
		l.setInsertTerm(token.SEMICOLON)
		return token.Token{Kind: token.SEMICOLON, Pos: start, End: l.pos()}
	case '@':
		l.advance()
		l.setInsertTerm(token.AT)
		return token.Token{Kind: token.AT, Pos: start, End: l.pos()}
	case '~':
		l.advance()
		l.setInsertTerm(token.BITNOT)
		return token.Token{Kind: token.BITNOT, Pos: start, End: l.pos()}
	case '#':
		// Shebang at byte 0 is consumed by skipShebang. A `#` elsewhere
		// in the source is the annotation prefix (v0.2 R26).
		l.advance()
		l.setInsertTerm(token.HASH)
		return token.Token{Kind: token.HASH, Pos: start, End: l.pos()}

	case '.':
		if l.peekAt(1) == '.' {
			l.advance()
			l.advance()
			if l.peek() == '=' {
				l.advance()
				l.setInsertTerm(token.DOTDOTEQ)
				return token.Token{Kind: token.DOTDOTEQ, Pos: start, End: l.pos()}
			}
			l.setInsertTerm(token.DOTDOT)
			return token.Token{Kind: token.DOTDOT, Pos: start, End: l.pos()}
		}
		l.advance()
		l.setInsertTerm(token.DOT)
		return token.Token{Kind: token.DOT, Pos: start, End: l.pos()}

	case ':':
		if l.peekAt(1) == ':' {
			l.advance()
			l.advance()
			l.setInsertTerm(token.COLONCOLON)
			return token.Token{Kind: token.COLONCOLON, Pos: start, End: l.pos()}
		}
		l.advance()
		l.setInsertTerm(token.COLON)
		return token.Token{Kind: token.COLON, Pos: start, End: l.pos()}

	case '?':
		if l.peekAt(1) == '.' {
			l.advance()
			l.advance()
			l.setInsertTerm(token.QDOT)
			return token.Token{Kind: token.QDOT, Pos: start, End: l.pos()}
		}
		if l.peekAt(1) == '?' {
			l.advance()
			l.advance()
			l.setInsertTerm(token.QQ)
			return token.Token{Kind: token.QQ, Pos: start, End: l.pos()}
		}
		l.advance()
		l.setInsertTerm(token.QUESTION)
		return token.Token{Kind: token.QUESTION, Pos: start, End: l.pos()}

	case '+':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			l.setInsertTerm(token.PLUSEQ)
			return token.Token{Kind: token.PLUSEQ, Pos: start, End: l.pos()}
		}
		l.setInsertTerm(token.PLUS)
		return token.Token{Kind: token.PLUS, Pos: start, End: l.pos()}

	case '-':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			l.setInsertTerm(token.MINUSEQ)
			return token.Token{Kind: token.MINUSEQ, Pos: start, End: l.pos()}
		}
		if l.peek() == '>' {
			l.advance()
			l.setInsertTerm(token.ARROW)
			return token.Token{Kind: token.ARROW, Pos: start, End: l.pos()}
		}
		l.setInsertTerm(token.MINUS)
		return token.Token{Kind: token.MINUS, Pos: start, End: l.pos()}

	case '*':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			l.setInsertTerm(token.STAREQ)
			return token.Token{Kind: token.STAREQ, Pos: start, End: l.pos()}
		}
		l.setInsertTerm(token.STAR)
		return token.Token{Kind: token.STAR, Pos: start, End: l.pos()}

	case '/':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			l.setInsertTerm(token.SLASHEQ)
			return token.Token{Kind: token.SLASHEQ, Pos: start, End: l.pos()}
		}
		l.setInsertTerm(token.SLASH)
		return token.Token{Kind: token.SLASH, Pos: start, End: l.pos()}

	case '%':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			l.setInsertTerm(token.PERCENTEQ)
			return token.Token{Kind: token.PERCENTEQ, Pos: start, End: l.pos()}
		}
		l.setInsertTerm(token.PERCENT)
		return token.Token{Kind: token.PERCENT, Pos: start, End: l.pos()}

	case '=':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			l.setInsertTerm(token.EQ)
			return token.Token{Kind: token.EQ, Pos: start, End: l.pos()}
		}
		if l.peek() == '>' {
			// O7 / §1.7: `=>` is not a token. Any occurrence is a lex
			// error. Consume both bytes so recovery continues at the
			// next real token rather than emitting a spurious `>`.
			l.advance()
			end := l.pos()
			l.errorCode(start, end,
				diag.CodeFatArrowRemoved,
				"`=>` is not a token in Osty",
				"use `->` — match arms and every other arrow position use `->` (v0.3 §1.7, O7).")
			l.setInsertTerm(token.ILLEGAL)
			return token.Token{Kind: token.ILLEGAL, Pos: start, End: end, Value: "=>"}
		}
		l.setInsertTerm(token.ASSIGN)
		return token.Token{Kind: token.ASSIGN, Pos: start, End: l.pos()}

	case '!':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			l.setInsertTerm(token.NEQ)
			return token.Token{Kind: token.NEQ, Pos: start, End: l.pos()}
		}
		l.setInsertTerm(token.NOT)
		return token.Token{Kind: token.NOT, Pos: start, End: l.pos()}

	case '<':
		l.advance()
		switch l.peek() {
		case '=':
			l.advance()
			l.setInsertTerm(token.LEQ)
			return token.Token{Kind: token.LEQ, Pos: start, End: l.pos()}
		case '<':
			l.advance()
			if l.peek() == '=' {
				l.advance()
				l.setInsertTerm(token.SHLEQ)
				return token.Token{Kind: token.SHLEQ, Pos: start, End: l.pos()}
			}
			l.setInsertTerm(token.SHL)
			return token.Token{Kind: token.SHL, Pos: start, End: l.pos()}
		case '-':
			l.advance()
			l.setInsertTerm(token.CHANARROW)
			return token.Token{Kind: token.CHANARROW, Pos: start, End: l.pos()}
		}
		l.setInsertTerm(token.LT)
		return token.Token{Kind: token.LT, Pos: start, End: l.pos()}

	case '>':
		l.advance()
		switch l.peek() {
		case '=':
			l.advance()
			l.setInsertTerm(token.GEQ)
			return token.Token{Kind: token.GEQ, Pos: start, End: l.pos()}
		case '>':
			l.advance()
			if l.peek() == '=' {
				l.advance()
				l.setInsertTerm(token.SHREQ)
				return token.Token{Kind: token.SHREQ, Pos: start, End: l.pos()}
			}
			l.setInsertTerm(token.SHR)
			return token.Token{Kind: token.SHR, Pos: start, End: l.pos()}
		}
		l.setInsertTerm(token.GT)
		return token.Token{Kind: token.GT, Pos: start, End: l.pos()}

	case '&':
		l.advance()
		if l.peek() == '&' {
			l.advance()
			l.setInsertTerm(token.AND)
			return token.Token{Kind: token.AND, Pos: start, End: l.pos()}
		}
		if l.peek() == '=' {
			l.advance()
			l.setInsertTerm(token.BITANDEQ)
			return token.Token{Kind: token.BITANDEQ, Pos: start, End: l.pos()}
		}
		l.setInsertTerm(token.BITAND)
		return token.Token{Kind: token.BITAND, Pos: start, End: l.pos()}

	case '|':
		l.advance()
		if l.peek() == '|' {
			l.advance()
			l.setInsertTerm(token.OR)
			return token.Token{Kind: token.OR, Pos: start, End: l.pos()}
		}
		if l.peek() == '=' {
			l.advance()
			l.setInsertTerm(token.BITOREQ)
			return token.Token{Kind: token.BITOREQ, Pos: start, End: l.pos()}
		}
		l.setInsertTerm(token.BITOR)
		return token.Token{Kind: token.BITOR, Pos: start, End: l.pos()}

	case '^':
		l.advance()
		if l.peek() == '=' {
			l.advance()
			l.setInsertTerm(token.BITXOREQ)
			return token.Token{Kind: token.BITXOREQ, Pos: start, End: l.pos()}
		}
		l.setInsertTerm(token.BITXOR)
		return token.Token{Kind: token.BITXOR, Pos: start, End: l.pos()}
	}

	l.errorf(start, "unexpected character %q", b)
	l.advance()
	l.setInsertTerm(token.ILLEGAL)
	return token.Token{Kind: token.ILLEGAL, Pos: start, End: l.pos(), Value: string(b)}
}

// nextTokenSuppressesTerm peeks past whitespace and comments to determine
// whether the next significant character begins a token that should
// suppress an implicit statement terminator (v0.2 R2 case 2). Lexer state
// is fully restored on return.
func (l *Lexer) nextTokenSuppressesTerm() bool {
	defer l.restore(l.snapshot())
	l.skipSpacesAndComments()
	return l.atSuppressingChar()
}

// lexerState captures every mutable field a peek-then-restore call can
// touch. Slice fields are saved as headers — append-only growth means
// the restored (shorter) slice hides any items a peeked-over call may
// have written to the backing array. Adding new mutable state to Lexer
// means updating both snapshot() and restore() or the next peek-style
// helper will silently leak that state.
type lexerState struct {
	offset      int
	line        int
	col         int
	pendingDoc  []string
	docLastLine int
	comments    []token.Comment
	errs        []*diag.Diagnostic
}

func (l *Lexer) snapshot() lexerState {
	return lexerState{
		offset:      l.offset,
		line:        l.line,
		col:         l.col,
		pendingDoc:  l.pendingDoc,
		docLastLine: l.docLastLine,
		comments:    l.comments,
		errs:        l.errs,
	}
}

func (l *Lexer) restore(s lexerState) {
	l.offset = s.offset
	l.line = s.line
	l.col = s.col
	l.pendingDoc = s.pendingDoc
	l.docLastLine = s.docLastLine
	l.comments = s.comments
	l.errs = s.errs
}

// atSuppressingChar inspects the next 1-3 source bytes to decide if a
// suppressing token starts here. Called from nextTokenSuppressesTerm with
// whitespace already skipped.
func (l *Lexer) atSuppressingChar() bool {
	if l.offset >= len(l.src) {
		return false
	}
	b := l.peek()
	switch b {
	case ')', ']', '}', ',':
		return true
	case '.':
		// `.foo`, `..b`, `..=b` all suppress.
		return true
	case '?':
		// `?.foo` and `??` suppress; bare `?` is postfix and would
		// already attach to prior expression — also suppress.
		return true
	case '+', '*', '/', '%', '^':
		// Pure binary operators (no prefix form).
		return true
	case '<':
		// `<`, `<=`, `<<`, `<<=` are binary; `<-` is statement only and
		// also suppresses (channel send continues prior expression).
		return true
	case '>':
		// `>`, `>=`, `>>`, `>>=`.
		return true
	case '=':
		// Assignment is statement-only and not really subject to ASI in
		// the middle of expressions; only `==` is binary. Suppress only
		// for `==`.
		return l.peekAt(1) == '='
	case '!':
		return l.peekAt(1) == '='
	case '&':
		// `&`, `&&`, `&=` — all expression-continuing.
		return true
	case '|':
		// `|`, `||`, `|=` — bit-or / logical-or. Pattern `|` only
		// appears inside a match arm, never starts a new line in a
		// statement context. Suppress.
		return true
	case '-':
		// `-` is ambiguous (binary subtract or unary negate). The spec
		// places it in case 2; suppress to bias toward continuation.
		return true
	}
	return false
}

// setInsertTerm decides whether a subsequent newline should act as a
// statement terminator, per OSTY_GRAMMAR_v0.3 R2 case 1.
//
// Suppression list (newline after these is swallowed): binary operators
// requiring a left operand, `,`, `->`, `<-`, `::`, `@`, pattern `|`, `(`,
// `[`, `{`. Per O3, `.` and `?.` are NOT in this list — a trailing `.`
// followed by a newline is a syntax error.
func (l *Lexer) setInsertTerm(k token.Kind) {
	switch k {
	// Tokens that *end* a statement or expression — newline after them
	// becomes a statement terminator.
	case token.IDENT,
		token.INT, token.FLOAT, token.STRING, token.RAWSTRING,
		token.CHAR, token.BYTE,
		token.RPAREN, token.RBRACKET, token.RBRACE,
		token.BREAK, token.CONTINUE, token.RETURN,
		token.UNDERSCORE,
		token.QUESTION,
		// O3: `.` and `?.` do NOT suppress trailing newlines.
		token.DOT, token.QDOT:
		l.insertTerm = true
	default:
		l.insertTerm = false
	}
}
