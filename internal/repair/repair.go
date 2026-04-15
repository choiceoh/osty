// Package repair applies conservative source-level rewrites for common
// Osty mistakes that prevent parsing.
package repair

import (
	"bytes"
	"sort"

	"github.com/osty/osty/internal/lexer"
	"github.com/osty/osty/internal/token"
)

// Change describes one user-facing repair. A change may expand to more
// than one textual edit; for example, moving a trailing method-chain dot
// deletes it at the old line and inserts it at the continuation line.
type Change struct {
	Kind    string
	Message string
	Pos     token.Pos
}

// Result is the output of applying source repairs.
type Result struct {
	Source  []byte
	Changes []Change
	Skipped int
}

type edit struct {
	start int
	end   int
	text  string
}

// Source returns src with every safe, machine-applicable repair applied.
//
// The rewrites intentionally cover only syntax that has an unambiguous
// Osty equivalent:
//
//   - match-style fat arrows: `=>` -> `->`
//   - uppercase base prefixes: `0X` / `0B` / `0O` -> lowercase
//   - Rust-style member separators: `foo::bar` -> `foo.bar`
//   - Go/Python/JS-style declaration keywords in declaration shape:
//     `func` / `function` / `def` -> `fn`, `const` -> `let`,
//     `var` -> `let mut`, `public` -> `pub`, `private` removed
//   - Python/JS control-flow spelling: `elif` / `elseif` -> `else if`,
//     `while` -> `for`, `switch` -> `match`, `case x:` -> `x ->`,
//     `default:` -> `_ ->`
//   - Python/JS value and operator spelling: `nil` / `null` -> `None`,
//     `True` / `False` -> `true` / `false`, `and` / `or` / `not` /
//     `is` / `is not` -> Osty operators
//   - Go short declarations: `x := value` -> `let x = value`
//   - JS logging: `console.log(x)` -> `println(x)`
//   - statement semicolons: removed or split into newlines
//   - `}` newline `else`: collapsed to `} else`
//   - trailing method-chain `.` / `?.`: moved to the continuation line
func Source(src []byte) Result {
	normalizedLineEndings := bytes.ContainsRune(src, '\r')
	src = normalizeNewlines(src)
	toks := lexer.New(src).Lex()

	var edits []edit
	var changes []Change
	if normalizedLineEndings {
		changes = append(changes, Change{
			Kind:    "line_endings",
			Message: "normalize CRLF/CR line endings to LF",
			Pos:     token.Pos{Line: 1, Column: 1},
		})
	}
	add := func(t token.Token, kind, msg string, editsForChange ...edit) {
		edits = append(edits, editsForChange...)
		changes = append(changes, Change{Kind: kind, Message: msg, Pos: t.Pos})
	}

	for i, t := range toks {
		switch t.Kind {
		case token.ILLEGAL:
			if t.Value == "=>" {
				add(t, "fat_arrow", "replace `=>` with `->`",
					edit{start: t.Pos.Offset, end: t.End.Offset, text: "->"})
			}
		case token.INT:
			if prefix := uppercaseBasePrefix(t.Value); prefix != "" && t.Pos.Offset+2 <= len(src) {
				add(t, "uppercase_base_prefix", "lowercase numeric base prefix",
					edit{start: t.Pos.Offset + 1, end: t.Pos.Offset + 2, text: prefix})
			}
		case token.COLONCOLON:
			if next := nextSignificant(toks, i+1); next.Kind == token.IDENT {
				add(t, "member_separator", "replace Rust-style `::` member access with `.`",
					edit{start: t.Pos.Offset, end: t.End.Offset, text: "."})
			}
		case token.IDENT:
			if colon, assign, ok := shortVarDecl(toks, i); ok {
				add(t, "short_var_decl", "replace Go-style `:=` declaration with `let ... =`",
					edit{start: t.Pos.Offset, end: t.Pos.Offset, text: "let "},
					edit{start: colon.Pos.Offset, end: assign.End.Offset, text: "="})
			}
			if repl, ok := declarationKeywordReplacement(t.Value); ok && looksLikeFnDecl(toks, i) {
				add(t, "function_keyword", "replace function declaration keyword with `fn`",
					edit{start: t.Pos.Offset, end: t.End.Offset, text: repl})
			}
			if t.Value == "const" && looksLikeLetDecl(toks, i) {
				add(t, "const_keyword", "replace `const` declaration with `let`",
					edit{start: t.Pos.Offset, end: t.End.Offset, text: "let"})
			}
			if t.Value == "var" && looksLikeLetDecl(toks, i) {
				add(t, "var_keyword", "replace `var` declaration with `let mut`",
					edit{start: t.Pos.Offset, end: t.End.Offset, text: "let mut"})
			}
			if t.Value == "public" && looksLikeVisibilityDecl(toks, i) {
				add(t, "visibility_keyword", "replace `public` with `pub`",
					edit{start: t.Pos.Offset, end: t.End.Offset, text: "pub"})
			}
			if t.Value == "private" {
				if next, ok := nextSignificantIndex(toks, i+1); ok && looksLikeVisibilityTarget(toks[next]) {
					add(t, "visibility_keyword", "remove redundant `private` keyword",
						edit{start: t.Pos.Offset, end: toks[next].Pos.Offset})
				}
			}
			if (t.Value == "elif" || t.Value == "elseif") && looksLikeElseIf(toks, i) {
				add(t, "else_if_keyword", "replace alternate else-if spelling with `else if`",
					edit{start: t.Pos.Offset, end: t.End.Offset, text: "else if"})
			}
			if t.Value == "while" && looksLikeBlockHead(toks, i) {
				add(t, "while_keyword", "replace `while` with Osty's condition loop `for`",
					edit{start: t.Pos.Offset, end: t.End.Offset, text: "for"})
			}
			if t.Value == "switch" && looksLikeBlockHead(toks, i) {
				add(t, "switch_keyword", "replace `switch` with `match`",
					edit{start: t.Pos.Offset, end: t.End.Offset, text: "match"})
			}
			if t.Value == "case" {
				if next, colon, ok := caseArmColon(toks, i); ok {
					add(t, "case_arm", "replace `case` arm syntax with Osty match arm syntax",
						edit{start: t.Pos.Offset, end: toks[next].Pos.Offset},
						edit{start: colon.Pos.Offset, end: colon.End.Offset, text: " ->"})
				}
			}
			if t.Value == "default" {
				if colon, ok := defaultArmColon(toks, i); ok {
					add(t, "default_arm", "replace `default` arm with `_ ->`",
						edit{start: t.Pos.Offset, end: t.End.Offset, text: "_"},
						edit{start: colon.Pos.Offset, end: colon.End.Offset, text: " ->"})
				}
			}
			if repl, ok := valueIdentifierReplacement(t.Value); ok && looksLikeValueUse(toks, i) {
				add(t, "value_spelling", "replace foreign literal spelling with Osty spelling",
					edit{start: t.Pos.Offset, end: t.End.Offset, text: repl})
			}
			if repl, ok := wordOperatorReplacement(toks, i); ok {
				add(t, "word_operator", "replace word operator with Osty operator",
					edit{start: t.Pos.Offset, end: wordOperatorEnd(toks, i), text: repl})
			}
			if logTok, ok := consoleLogCall(toks, i); ok {
				add(t, "console_log", "replace `console.log` with `println`",
					edit{start: t.Pos.Offset, end: logTok.End.Offset, text: "println"})
			}
		case token.SEMICOLON:
			add(t, "semicolon", "repair statement semicolon",
				semicolonEdit(src, toks, i))
		case token.RBRACE:
			if elseTok, ok := newlineSeparatedElse(src, toks, i); ok {
				add(elseTok, "else_newline", "move `else` onto the closing-brace line",
					edit{start: t.End.Offset, end: elseTok.Pos.Offset, text: " "})
			}
		case token.DOT, token.QDOT:
			if next := nextSignificant(toks, i+1); canMoveTrailingChainDot(src, t, next) {
				text := "."
				if t.Kind == token.QDOT {
					text = "?."
				}
				add(t, "trailing_chain_dot", "move method-chain dot to the continuation line",
					edit{start: t.Pos.Offset, end: t.End.Offset},
					edit{start: next.Pos.Offset, end: next.Pos.Offset, text: text})
			}
		}
	}

	out, skipped := apply(src, edits)
	return Result{Source: out, Changes: changes, Skipped: skipped}
}

func uppercaseBasePrefix(value string) string {
	if len(value) < 2 || value[0] != '0' {
		return ""
	}
	switch value[1] {
	case 'X':
		return "x"
	case 'B':
		return "b"
	case 'O':
		return "o"
	default:
		return ""
	}
}

func declarationKeywordReplacement(value string) (string, bool) {
	switch value {
	case "func", "function", "def":
		return "fn", true
	default:
		return "", false
	}
}

func looksLikeFnDecl(toks []token.Token, i int) bool {
	next := nextSignificant(toks, i+1)
	if next.Kind != token.IDENT {
		return false
	}
	afterName := nextSignificantFromToken(toks, next, i+2)
	if afterName.Kind != token.LPAREN {
		return false
	}
	prev := prevSignificant(toks, i-1)
	return prev.Kind != token.DOT && prev.Kind != token.QDOT
}

func looksLikeLetDecl(toks []token.Token, i int) bool {
	next := nextSignificant(toks, i+1)
	if next.Kind != token.IDENT {
		return false
	}
	for j := i + 2; j < len(toks); j++ {
		switch toks[j].Kind {
		case token.NEWLINE, token.EOF, token.RBRACE:
			return false
		case token.ASSIGN:
			return true
		}
	}
	return false
}

func looksLikeVisibilityDecl(toks []token.Token, i int) bool {
	next := nextSignificant(toks, i+1)
	return looksLikeVisibilityTarget(next)
}

func looksLikeVisibilityTarget(t token.Token) bool {
	switch t.Kind {
	case token.FN, token.STRUCT, token.ENUM, token.INTERFACE, token.TYPE, token.LET:
		return true
	case token.IDENT:
		_, isFn := declarationKeywordReplacement(t.Value)
		return isFn || t.Value == "const" || t.Value == "var"
	default:
		return false
	}
}

func looksLikeElseIf(toks []token.Token, i int) bool {
	next := nextSignificant(toks, i+1)
	return startsExpr(next.Kind)
}

func looksLikeBlockHead(toks []token.Token, i int) bool {
	next := nextSignificant(toks, i+1)
	if !startsExpr(next.Kind) {
		return false
	}
	depth := 0
	for j := i + 1; j < len(toks); j++ {
		switch toks[j].Kind {
		case token.LPAREN, token.LBRACKET:
			depth++
		case token.RPAREN, token.RBRACKET:
			if depth > 0 {
				depth--
			}
		case token.LBRACE:
			return depth == 0
		case token.NEWLINE, token.EOF:
			return false
		}
	}
	return false
}

func shortVarDecl(toks []token.Token, i int) (token.Token, token.Token, bool) {
	if !atStatementStart(toks, i) {
		return token.Token{}, token.Token{}, false
	}
	colonIdx, ok := nextSignificantIndex(toks, i+1)
	if !ok || toks[colonIdx].Kind != token.COLON {
		return token.Token{}, token.Token{}, false
	}
	assignIdx, ok := nextSignificantIndex(toks, colonIdx+1)
	if !ok || toks[assignIdx].Kind != token.ASSIGN {
		return token.Token{}, token.Token{}, false
	}
	return toks[colonIdx], toks[assignIdx], true
}

func atStatementStart(toks []token.Token, i int) bool {
	prev := prevSignificant(toks, i-1)
	switch prev.Kind {
	case token.EOF, token.NEWLINE, token.LBRACE, token.RBRACE:
		return true
	default:
		return false
	}
}

func valueIdentifierReplacement(value string) (string, bool) {
	switch value {
	case "nil", "null":
		return "None", true
	case "True":
		return "true", true
	case "False":
		return "false", true
	default:
		return "", false
	}
}

func looksLikeValueUse(toks []token.Token, i int) bool {
	next := nextSignificant(toks, i+1)
	prev := prevSignificant(toks, i-1)
	if next.Kind == token.COLON && !(prev.Kind == token.IDENT && prev.Value == "case") {
		return false
	}
	if next.Kind == token.ASSIGN {
		return false
	}
	if prev.Kind == token.LET || prev.Kind == token.FN || prev.Kind == token.MUT ||
		prev.Kind == token.PUB || prev.Kind == token.DOT || prev.Kind == token.QDOT {
		return false
	}
	return true
}

func wordOperatorReplacement(toks []token.Token, i int) (string, bool) {
	t := toks[i]
	prev := prevSignificant(toks, i-1)
	next := nextSignificant(toks, i+1)
	switch t.Value {
	case "and":
		if tokenEndsExpr(prev) && tokenStartsExpr(next) {
			return "&&", true
		}
	case "or":
		if tokenEndsExpr(prev) && tokenStartsExpr(next) {
			return "||", true
		}
	case "not":
		if prev.Kind == token.IDENT && prev.Value == "is" {
			return "", false
		}
		if tokenStartsExpr(next) && !tokenEndsExpr(prev) {
			return "!", true
		}
	case "is":
		if !tokenEndsExpr(prev) {
			return "", false
		}
		if next.Kind == token.IDENT && next.Value == "not" {
			afterNot := nextSignificantFromToken(toks, next, i+2)
			if tokenStartsExpr(afterNot) {
				return "!=", true
			}
		}
		if tokenStartsExpr(next) {
			return "==", true
		}
	}
	return "", false
}

func wordOperatorEnd(toks []token.Token, i int) int {
	if toks[i].Value != "is" {
		return toks[i].End.Offset
	}
	next := nextSignificant(toks, i+1)
	if next.Kind == token.IDENT && next.Value == "not" {
		return next.End.Offset
	}
	return toks[i].End.Offset
}

func consoleLogCall(toks []token.Token, i int) (token.Token, bool) {
	if toks[i].Value != "console" {
		return token.Token{}, false
	}
	dotIdx, ok := nextSignificantIndex(toks, i+1)
	if !ok || toks[dotIdx].Kind != token.DOT {
		return token.Token{}, false
	}
	logIdx, ok := nextSignificantIndex(toks, dotIdx+1)
	if !ok || toks[logIdx].Kind != token.IDENT || toks[logIdx].Value != "log" {
		return token.Token{}, false
	}
	callIdx, ok := nextSignificantIndex(toks, logIdx+1)
	if !ok || toks[callIdx].Kind != token.LPAREN {
		return token.Token{}, false
	}
	return toks[logIdx], true
}

func caseArmColon(toks []token.Token, i int) (nextIdx int, colon token.Token, ok bool) {
	nextIdx, ok = nextSignificantIndex(toks, i+1)
	if !ok || toks[nextIdx].Kind == token.COLON || toks[nextIdx].Kind == token.NEWLINE {
		return 0, token.Token{}, false
	}
	colon, ok = findArmColon(toks, nextIdx)
	return nextIdx, colon, ok
}

func defaultArmColon(toks []token.Token, i int) (token.Token, bool) {
	next := nextSignificant(toks, i+1)
	if next.Kind == token.COLON {
		return next, true
	}
	return token.Token{}, false
}

func findArmColon(toks []token.Token, start int) (token.Token, bool) {
	depth := 0
	for j := start; j < len(toks); j++ {
		switch toks[j].Kind {
		case token.LPAREN, token.LBRACKET, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACKET, token.RBRACE:
			if depth == 0 {
				return token.Token{}, false
			}
			depth--
		case token.COLON:
			if depth == 0 {
				return toks[j], true
			}
		case token.ARROW:
			return token.Token{}, false
		case token.NEWLINE, token.EOF:
			return token.Token{}, false
		}
	}
	return token.Token{}, false
}

func semicolonEdit(src []byte, toks []token.Token, i int) edit {
	next := nextSignificant(toks, i+1)
	if next.Kind != token.EOF && next.Kind != token.RBRACE && next.Pos.Line == toks[i].Pos.Line {
		return edit{
			start: toks[i].Pos.Offset,
			end:   next.Pos.Offset,
			text:  "\n" + lineIndent(src, toks[i].Pos.Offset),
		}
	}
	return edit{start: toks[i].Pos.Offset, end: toks[i].End.Offset}
}

func lineIndent(src []byte, offset int) string {
	start := offset
	for start > 0 && src[start-1] != '\n' {
		start--
	}
	end := start
	for end < len(src) {
		switch src[end] {
		case ' ', '\t':
			end++
		default:
			return string(src[start:end])
		}
	}
	return string(src[start:end])
}

func nextSignificant(toks []token.Token, start int) token.Token {
	if i, ok := nextSignificantIndex(toks, start); ok {
		return toks[i]
	}
	return token.Token{Kind: token.EOF}
}

func nextSignificantIndex(toks []token.Token, start int) (int, bool) {
	for i := start; i < len(toks); i++ {
		if toks[i].Kind != token.NEWLINE {
			return i, true
		}
	}
	return 0, false
}

func nextSignificantFromToken(toks []token.Token, tok token.Token, fallback int) token.Token {
	for i := fallback; i < len(toks); i++ {
		if toks[i].Pos.Offset < tok.End.Offset {
			continue
		}
		if toks[i].Kind != token.NEWLINE {
			return toks[i]
		}
	}
	return token.Token{Kind: token.EOF}
}

func startsExpr(k token.Kind) bool {
	switch k {
	case token.IDENT, token.INT, token.FLOAT, token.STRING, token.RAWSTRING,
		token.CHAR, token.BYTE, token.LPAREN, token.LBRACKET, token.LBRACE,
		token.IF, token.MATCH, token.BITOR, token.OR, token.MINUS,
		token.NOT, token.BITNOT, token.DOTDOT, token.DOTDOTEQ:
		return true
	default:
		return false
	}
}

func tokenStartsExpr(t token.Token) bool {
	if t.Kind == token.IDENT {
		switch t.Value {
		case "and", "or", "is", "case", "default", "elif", "elseif":
			return false
		}
	}
	return startsExpr(t.Kind)
}

func endsExpr(k token.Kind) bool {
	switch k {
	case token.IDENT, token.INT, token.FLOAT, token.STRING, token.RAWSTRING,
		token.CHAR, token.BYTE, token.RPAREN, token.RBRACKET, token.RBRACE,
		token.QUESTION:
		return true
	default:
		return false
	}
}

func tokenEndsExpr(t token.Token) bool {
	if t.Kind == token.IDENT {
		switch t.Value {
		case "and", "or", "not", "is", "case", "default", "elif", "elseif":
			return false
		}
	}
	return endsExpr(t.Kind)
}

func prevSignificant(toks []token.Token, start int) token.Token {
	for i := start; i >= 0; i-- {
		if toks[i].Kind != token.NEWLINE {
			return toks[i]
		}
	}
	return token.Token{Kind: token.EOF}
}

func newlineSeparatedElse(src []byte, toks []token.Token, i int) (token.Token, bool) {
	j := i + 1
	sawNewline := false
	for j < len(toks) && toks[j].Kind == token.NEWLINE {
		sawNewline = true
		j++
	}
	if !sawNewline || j >= len(toks) || toks[j].Kind != token.ELSE {
		return token.Token{}, false
	}
	if !allWhitespace(src[toks[i].End.Offset:toks[j].Pos.Offset]) {
		return token.Token{}, false
	}
	return toks[j], true
}

func canMoveTrailingChainDot(src []byte, dot token.Token, next token.Token) bool {
	if next.Kind != token.IDENT {
		return false
	}
	if dot.End.Offset > next.Pos.Offset {
		return false
	}
	gap := src[dot.End.Offset:next.Pos.Offset]
	return bytes.ContainsRune(gap, '\n') && allWhitespace(gap)
}

func allWhitespace(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			continue
		default:
			return false
		}
	}
	return true
}

func apply(src []byte, edits []edit) ([]byte, int) {
	if len(edits) == 0 {
		return src, 0
	}
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].start != edits[j].start {
			return edits[i].start > edits[j].start
		}
		return edits[i].end > edits[j].end
	})
	out := append([]byte(nil), src...)
	lastStart := len(src) + 1
	skipped := 0
	for _, e := range edits {
		if e.start < 0 || e.end < e.start || e.end > len(src) || e.end > lastStart {
			skipped++
			continue
		}
		out = append(append([]byte(nil), out[:e.start]...), append([]byte(e.text), out[e.end:]...)...)
		lastStart = e.start
	}
	return out, skipped
}

func normalizeNewlines(src []byte) []byte {
	if !bytes.ContainsRune(src, '\r') {
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
