package selfhost

import (
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// astbridgeLowerCount records every time a FrontendRun materializes
// the *ast.File via the astbridge-based astLowerPublicFile adapter.
// It is the single source of truth for "did this code path touch the
// runtime.golegacy.astbridge bootstrap bridge?" — tests use it to pin
// astbridge-free code paths (e.g., the native resolve wedge) and to
// detect regressions when a would-be-native path silently falls back
// to the Go AST. Counter is package-global because FrontendRun.File()
// is the only astbridge entry point for the resolve/check/llvmgen
// callers. See ResolveStructuredFromRun / cmd/osty case "resolve" for
// the intended zero-bump usage.
var astbridgeLowerCount int64

// AstbridgeLowerCount returns the total number of astbridge-based
// *ast.File lowerings performed since process start (or since the
// last ResetAstbridgeLowerCount call).
func AstbridgeLowerCount() int64 {
	return atomic.LoadInt64(&astbridgeLowerCount)
}

// ResetAstbridgeLowerCount zeros the counter. Intended for tests that
// want to measure astbridge activity over a specific code window.
func ResetAstbridgeLowerCount() {
	atomic.StoreInt64(&astbridgeLowerCount, 0)
}

// Lex runs the bootstrapped pure-Osty lexer and adapts its stream to the
// compiler's public token surface.
func Lex(src []byte) ([]token.Token, []*diag.Diagnostic, []token.Comment) {
	text := normalizeSourceNewlines(string(src))
	rt := newRuneTable(text)
	stream := frontendLexStream(text)
	return adaptLexStream(rt, stream, ostyLexFactsFromStream(text, stream))
}

// normalizeSourceNewlines rewrites \r\n and lone \r to \n, mirroring
// the self-hosted ostyNormalizeSource wrapper so offsets reported by
// the Go-facing Lex/Run entry points match what the Osty-authored
// front-end normalizer would have produced.
func normalizeSourceNewlines(text string) string {
	if !strings.ContainsRune(text, '\r') {
		return text
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

// FrontendRun is one complete self-hosted front-end pass over a source file.
// It owns the shared lex stream, parser arena, public token adaptation,
// lowered AST, and diagnostic adaptation so callers do not accidentally
// re-run the front end.
type FrontendRun struct {
	text     string
	rt       runeTable
	stream   *FrontLexStream
	lexFacts *OstyLexFacts
	parser   *OstyParser
	toks     []token.Token
	comments []token.Comment
	file     *ast.File
	lexDiags []*diag.Diagnostic
	diags    []*diag.Diagnostic
	adapted  bool
}

// Run executes the self-hosted lexer and parser once and keeps all adapted
// public surfaces available through FrontendRun methods.
func Run(src []byte) *FrontendRun {
	return runFrontend(src, true)
}

func runFrontend(src []byte, adaptTokens bool) *FrontendRun {
	text := normalizeSourceNewlines(string(src))
	rt := newRuneTable(text)
	stream := frontendLexStream(text)
	frontToks := frontTokensFromRuneTable(rt, stream)
	p := newOstyParser(frontToks)
	opParseFile(p)

	run := &FrontendRun{text: text, rt: rt, stream: stream, parser: p}
	if adaptTokens {
		run.ensureLexAdapted()
	} else {
		run.lexDiags = lexDiagnosticsFromFacts(rt, stream, run.facts())
	}
	return run
}

func frontTokensFromRuneTable(rt runeTable, stream *FrontLexStream) []*FrontToken {
	parseTokens := make([]*FrontToken, 0, len(stream.tokens))
	for _, tok := range stream.tokens {
		parseTokens = append(parseTokens, &FrontToken{
			kind:        tok.kind,
			text:        rt.slice(tok.start.offset, tok.start.offset+tok.length),
			startOffset: tok.start.offset,
			endOffset:   tok.end.offset,
		})
	}
	return parseTokens
}

func countStringUnits(text string) int {
	n := 0
	for range text {
		n++
	}
	return n
}

func splitStringUnits(text string) []string {
	if text == "" {
		return nil
	}
	units := make([]string, 0, countStringUnits(text))
	start := 0
	for idx := range text {
		if idx == 0 {
			continue
		}
		units = append(units, text[start:idx])
		start = idx
	}
	units = append(units, text[start:])
	return units
}

func matchTextUnits(units []string, start int, text string) bool {
	offset := 0
	for i := 0; i < len(text); {
		_, size := utf8.DecodeRuneInString(text[i:])
		if !ostyEqual(frontUnitAt(units, start+offset), text[i:i+size]) {
			return false
		}
		offset++
		i += size
	}
	return true
}

func collectLeadingDocs(units []string, stream *FrontLexStream) []string {
	leadingDocs := make([]string, 0, len(stream.tokens))
	commentIdx := 0
	commentCount := len(stream.comments)

	for _, tok := range stream.tokens {
		for commentIdx < commentCount && stream.comments[commentIdx].end.line < tok.start.line {
			commentIdx++
		}
		if tok.leadingDocLines <= 0 {
			leadingDocs = append(leadingDocs, "")
			continue
		}

		docStartLine := tok.start.line - tok.leadingDocLines
		docIdx := commentIdx
		for docIdx > 0 && stream.comments[docIdx-1].end.line >= docStartLine {
			docIdx--
		}

		var doc strings.Builder
		for idx := docIdx; idx < commentIdx; idx++ {
			c := stream.comments[idx]
			if !ostyEqual(c.kind, FrontCommentKind(&FrontCommentKind_FrontCommentDoc{})) {
				continue
			}
			if doc.Len() > 0 {
				doc.WriteByte('\n')
			}
			doc.WriteString(strings.TrimSpace(ostyCommentText(units, c)))
		}
		leadingDocs = append(leadingDocs, doc.String())
	}

	return leadingDocs
}

// Tokens returns the public token stream, including EOF.
func (r *FrontendRun) Tokens() []token.Token {
	r.ensureLexAdapted()
	return r.toks
}

// Comments returns every comment in source order.
func (r *FrontendRun) Comments() []token.Comment {
	r.ensureLexAdapted()
	return r.comments
}

// File returns the lowered semantic AST for this front-end pass.
//
// First call materializes the *ast.File via astLowerPublicFile — the
// single astbridge (`runtime.golegacy.astbridge`) entry point on the
// resolve / check / llvmgen side of the compiler. Subsequent calls
// return the cached result without touching astbridge again, so each
// FrontendRun contributes at most one lowering to
// AstbridgeLowerCount regardless of how many callers poke it.
func (r *FrontendRun) File() *ast.File {
	if r.file != nil {
		return r.file
	}
	atomic.AddInt64(&astbridgeLowerCount, 1)
	r.file = astLowerPublicFile(r.parser.arena, r.Tokens())
	return r.file
}

// astFile wraps the parser arena in the self-host AstFile handle without
// going through the astbridge *ast.File round-trip. Downstream native passes
// (resolve/check/llvmgen) consume AstArena directly, so this is the
// no-detour entry point.
func (r *FrontendRun) astFile() *AstFile {
	if r.parser == nil {
		return nil
	}
	return &AstFile{arena: r.parser.arena}
}

// LexDiagnostics returns lexer-only diagnostics.
func (r *FrontendRun) LexDiagnostics() []*diag.Diagnostic {
	if r.lexDiags == nil {
		r.lexDiags = lexDiagnosticsFromFacts(r.rt, r.stream, r.facts())
	}
	return r.lexDiags
}

func (r *FrontendRun) facts() *OstyLexFacts {
	if r.lexFacts == nil {
		r.lexFacts = ostyLexFactsFromStream(r.text, r.stream)
	}
	return r.lexFacts
}

func (r *FrontendRun) ensureLexAdapted() {
	if r.adapted {
		return
	}
	r.toks, r.lexDiags, r.comments = adaptLexStream(r.rt, r.stream, r.facts())
	r.adapted = true
}

func adaptLexStream(rt runeTable, stream *FrontLexStream, facts *OstyLexFacts) ([]token.Token, []*diag.Diagnostic, []token.Comment) {
	toks := make([]token.Token, 0, len(stream.tokens))
	for _, ft := range stream.tokens {
		startRune := ft.start.offset
		endRune := ft.start.offset + ft.length
		tok := token.Token{
			Kind:       mapTokenKind(ft.kind),
			Pos:        rt.pos(ft.start),
			End:        rt.pos(ft.end),
			Value:      rt.slice(startRune, endRune),
			Triple:     ft.triple,
			LeadingDoc: adapterStringAt(facts.leadingDocs, len(toks)),
		}
		fillLiteralParts(&tok, rt, stream, facts.stringParts, len(toks))
		toks = append(toks, tok)
	}
	toks = collapseFatArrows(toks)

	comments := make([]token.Comment, 0, len(facts.comments))
	for _, c := range facts.comments {
		comments = append(comments, token.Comment{
			Kind:    token.CommentKind(c.kindCode),
			Pos:     token.Pos{Offset: rt.byteOffset(c.startOffset), Line: c.startLine, Column: c.startCol},
			Text:    c.text,
			EndLine: c.endLine,
		})
	}
	return toks, lexDiagnosticsFromFacts(rt, stream, facts), comments
}

func lexDiagnosticsFromFacts(rt runeTable, stream *FrontLexStream, facts *OstyLexFacts) []*diag.Diagnostic {
	diags := make([]*diag.Diagnostic, 0, len(facts.errors))
	for _, d := range facts.errors {
		diags = append(diags, lexDiagnostic(d, rt))
	}
	// Bridge until selfhost regen lands: post-scan for §1.6.1 numeric
	// separator violations and surface them as E0008.
	diags = append(diags, scanBadNumericSeparators(rt, stream)...)
	return diags
}

// ParseDiagnostics runs the bootstrapped pure-Osty lexer and parser and
// returns their combined diagnostics without lowering the full public AST.
func ParseDiagnostics(src []byte) []*diag.Diagnostic {
	return runFrontend(src, false).Diagnostics()
}

type runeTable struct {
	src       string
	runes     []rune
	byteStart []int
}

func newRuneTable(src string) runeTable {
	count := countStringUnits(src)
	rt := runeTable{
		src:       src,
		runes:     make([]rune, 0, count),
		byteStart: make([]int, 0, count+1),
	}
	for off, r := range src {
		rt.runes = append(rt.runes, r)
		rt.byteStart = append(rt.byteStart, off)
	}
	rt.byteStart = append(rt.byteStart, len(src))
	return rt
}

func (rt runeTable) pos(p *FrontPos) token.Pos {
	if p == nil {
		return token.Pos{Line: 1, Column: 1}
	}
	return token.Pos{Offset: rt.byteOffset(p.offset), Line: p.line, Column: p.column}
}

func (rt runeTable) span(start, end *FrontPos) diag.Span {
	startPos := rt.pos(start)
	endPos := rt.pos(end)
	if endPos.Offset < startPos.Offset {
		endPos = startPos
	}
	return diag.Span{Start: startPos, End: endPos}
}

func (rt runeTable) byteOffset(runeOffset int) int {
	if runeOffset <= 0 {
		return 0
	}
	if runeOffset >= len(rt.byteStart) {
		return len(rt.src)
	}
	return rt.byteStart[runeOffset]
}

func (rt runeTable) slice(startRune, endRune int) string {
	if startRune < 0 {
		startRune = 0
	}
	if endRune < startRune {
		endRune = startRune
	}
	start := rt.byteOffset(startRune)
	end := rt.byteOffset(endRune)
	if start > len(rt.src) {
		start = len(rt.src)
	}
	if end > len(rt.src) {
		end = len(rt.src)
	}
	return rt.src[start:end]
}

func mapTokenKind(k FrontTokenKind) token.Kind {
	switch k.(type) {
	case *FrontTokenKind_FrontEOF:
		return token.EOF
	case *FrontTokenKind_FrontIllegal:
		return token.ILLEGAL
	case *FrontTokenKind_FrontNewline:
		return token.NEWLINE
	case *FrontTokenKind_FrontIdent:
		return token.IDENT
	case *FrontTokenKind_FrontLabel:
		return token.LABEL
	case *FrontTokenKind_FrontInt:
		return token.INT
	case *FrontTokenKind_FrontFloat:
		return token.FLOAT
	case *FrontTokenKind_FrontChar:
		return token.CHAR
	case *FrontTokenKind_FrontByte:
		return token.BYTE
	case *FrontTokenKind_FrontString:
		return token.STRING
	case *FrontTokenKind_FrontRawString:
		return token.RAWSTRING
	case *FrontTokenKind_FrontFn:
		return token.FN
	case *FrontTokenKind_FrontStruct:
		return token.STRUCT
	case *FrontTokenKind_FrontEnum:
		return token.ENUM
	case *FrontTokenKind_FrontInterface:
		return token.INTERFACE
	case *FrontTokenKind_FrontType:
		return token.TYPE
	case *FrontTokenKind_FrontLet:
		return token.LET
	case *FrontTokenKind_FrontMut:
		return token.MUT
	case *FrontTokenKind_FrontPub:
		return token.PUB
	case *FrontTokenKind_FrontUse:
		return token.USE
	case *FrontTokenKind_FrontIf:
		return token.IF
	case *FrontTokenKind_FrontElse:
		return token.ELSE
	case *FrontTokenKind_FrontMatch:
		return token.MATCH
	case *FrontTokenKind_FrontFor:
		return token.FOR
	case *FrontTokenKind_FrontReturn:
		return token.RETURN
	case *FrontTokenKind_FrontBreak:
		return token.BREAK
	case *FrontTokenKind_FrontContinue:
		return token.CONTINUE
	case *FrontTokenKind_FrontDefer:
		return token.DEFER
	case *FrontTokenKind_FrontLParen:
		return token.LPAREN
	case *FrontTokenKind_FrontRParen:
		return token.RPAREN
	case *FrontTokenKind_FrontLBrace:
		return token.LBRACE
	case *FrontTokenKind_FrontRBrace:
		return token.RBRACE
	case *FrontTokenKind_FrontLBracket:
		return token.LBRACKET
	case *FrontTokenKind_FrontRBracket:
		return token.RBRACKET
	case *FrontTokenKind_FrontComma:
		return token.COMMA
	case *FrontTokenKind_FrontColon:
		return token.COLON
	case *FrontTokenKind_FrontSemicolon:
		return token.SEMICOLON
	case *FrontTokenKind_FrontDot:
		return token.DOT
	case *FrontTokenKind_FrontPlus:
		return token.PLUS
	case *FrontTokenKind_FrontMinus:
		return token.MINUS
	case *FrontTokenKind_FrontStar:
		return token.STAR
	case *FrontTokenKind_FrontSlash:
		return token.SLASH
	case *FrontTokenKind_FrontPercent:
		return token.PERCENT
	case *FrontTokenKind_FrontEq:
		return token.EQ
	case *FrontTokenKind_FrontNeq:
		return token.NEQ
	case *FrontTokenKind_FrontLt:
		return token.LT
	case *FrontTokenKind_FrontGt:
		return token.GT
	case *FrontTokenKind_FrontLeq:
		return token.LEQ
	case *FrontTokenKind_FrontGeq:
		return token.GEQ
	case *FrontTokenKind_FrontAnd:
		return token.AND
	case *FrontTokenKind_FrontOr:
		return token.OR
	case *FrontTokenKind_FrontNot:
		return token.NOT
	case *FrontTokenKind_FrontBitAnd:
		return token.BITAND
	case *FrontTokenKind_FrontBitOr:
		return token.BITOR
	case *FrontTokenKind_FrontBitXor:
		return token.BITXOR
	case *FrontTokenKind_FrontBitNot:
		return token.BITNOT
	case *FrontTokenKind_FrontShl:
		return token.SHL
	case *FrontTokenKind_FrontShr:
		return token.SHR
	case *FrontTokenKind_FrontAssign:
		return token.ASSIGN
	case *FrontTokenKind_FrontPlusEq:
		return token.PLUSEQ
	case *FrontTokenKind_FrontMinusEq:
		return token.MINUSEQ
	case *FrontTokenKind_FrontStarEq:
		return token.STAREQ
	case *FrontTokenKind_FrontSlashEq:
		return token.SLASHEQ
	case *FrontTokenKind_FrontPercentEq:
		return token.PERCENTEQ
	case *FrontTokenKind_FrontBitAndEq:
		return token.BITANDEQ
	case *FrontTokenKind_FrontBitOrEq:
		return token.BITOREQ
	case *FrontTokenKind_FrontBitXorEq:
		return token.BITXOREQ
	case *FrontTokenKind_FrontShlEq:
		return token.SHLEQ
	case *FrontTokenKind_FrontShrEq:
		return token.SHREQ
	case *FrontTokenKind_FrontArrow:
		return token.ARROW
	case *FrontTokenKind_FrontChanArrow:
		return token.CHANARROW
	case *FrontTokenKind_FrontQuestion:
		return token.QUESTION
	case *FrontTokenKind_FrontQDot:
		return token.QDOT
	case *FrontTokenKind_FrontQQ:
		return token.QQ
	case *FrontTokenKind_FrontDotDot:
		return token.DOTDOT
	case *FrontTokenKind_FrontDotDotEq:
		return token.DOTDOTEQ
	case *FrontTokenKind_FrontColonColon:
		return token.COLONCOLON
	case *FrontTokenKind_FrontUnderscore:
		return token.UNDERSCORE
	case *FrontTokenKind_FrontAt:
		return token.AT
	case *FrontTokenKind_FrontHash:
		return token.HASH
	}
	return token.ILLEGAL
}

func fillLiteralParts(tok *token.Token, rt runeTable, stream *FrontLexStream, parts []*OstyLexStringPart, owner int) {
	switch tok.Kind {
	case token.STRING, token.RAWSTRING:
		for _, p := range parts {
			if p.ownerToken != owner {
				continue
			}
			if p.kindCode == int(token.PartExpr) {
				tok.Parts = append(tok.Parts, token.StringPart{
					Kind: token.PartExpr,
					Expr: interpolationTokens(stream, p.exprTokenStart, p.exprTokenCount, rt),
				})
				continue
			}
			raw := p.text
			if p.decodeEscapes {
				raw = decodeEscapes(raw)
			}
			tok.Parts = append(tok.Parts, token.StringPart{Kind: token.PartText, Text: raw})
		}
	case token.CHAR:
		tok.Value = decodeChar(tok.Value)
	case token.BYTE:
		tok.Value = decodeByte(tok.Value)
	}
}

func decodeEscapes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r != '\\' {
			b.WriteRune(r)
			i += size
			continue
		}
		if i+1 >= len(s) {
			b.WriteByte('\\')
			i++
			continue
		}
		next, nextSize := utf8.DecodeRuneInString(s[i+1:])
		switch next {
		case 'n':
			b.WriteByte('\n')
			i += 1 + nextSize
		case 'r':
			b.WriteByte('\r')
			i += 1 + nextSize
		case 't':
			b.WriteByte('\t')
			i += 1 + nextSize
		case '0':
			b.WriteByte(0)
			i += 1 + nextSize
		case '"', '\'', '\\', '{', '}':
			b.WriteRune(next)
			i += 1 + nextSize
		case 'x':
			if i+3 < len(s) {
				if v, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
					b.WriteByte(byte(v))
					i += 4
					continue
				}
			}
			b.WriteRune(next)
			i += 1 + nextSize
		case 'u':
			if i+2 < len(s) && s[i+2] == '{' {
				if end := strings.IndexByte(s[i+3:], '}'); end >= 0 {
					hex := s[i+3 : i+3+end]
					if v, err := strconv.ParseInt(hex, 16, 32); err == nil {
						b.WriteRune(rune(v))
						i += 4 + end
						continue
					}
				}
			}
			b.WriteRune(next)
			i += 1 + nextSize
		default:
			b.WriteRune(next)
			i += 1 + nextSize
		}
	}
	return b.String()
}

func interpolationTokens(stream *FrontLexStream, start, count int, rt runeTable) []token.Token {
	out := make([]token.Token, 0, count)
	end := start + count
	if start < 0 {
		start = 0
	}
	if end > len(stream.interpolationTokens) {
		end = len(stream.interpolationTokens)
	}
	for i := start; i < end; i++ {
		it := stream.interpolationTokens[i]
		if it.token == nil {
			continue
		}
		ft := it.token
		tok := token.Token{
			Kind:  mapTokenKind(ft.kind),
			Pos:   rt.pos(ft.start),
			End:   rt.pos(ft.end),
			Value: rt.slice(ft.start.offset, ft.start.offset+ft.length),
		}
		out = append(out, tok)
	}
	return out
}

func decodeChar(s string) string {
	if strings.HasPrefix(s, "b") {
		s = s[1:]
	}
	body := strings.Trim(s, "'")
	decoded := decodeEscapes(body)
	if decoded == "" {
		return "\uFFFD"
	}
	return decoded
}

func decodeByte(s string) string {
	v := decodeChar(s)
	r, _ := utf8.DecodeRuneInString(v)
	if r > 255 {
		return string(byte(0))
	}
	return string(byte(r))
}

func lexDiagnostic(d *OstyLexError, rt runeTable) *diag.Diagnostic {
	start := token.Pos{Offset: rt.byteOffset(d.startOffset), Line: d.startLine, Column: d.startCol}
	end := token.Pos{Offset: rt.byteOffset(d.endOffset), Line: d.endLine, Column: d.endCol}
	b := diag.New(diag.Error, d.message).
		Code(d.diagCode).
		Primary(diag.Span{Start: start, End: end}, "")
	if d.hint != "" {
		b.Hint(d.hint)
	}
	return b.Build()
}

// adapterStringAt is the adapter-local indexed-string fetch. The
// Osty-side `stringAt` (in `toolchain/elab.osty`) transpiles into a
// package-level `stringAt` in `generated.go`; this renamed helper
// avoids the name collision while keeping the adapter self-contained.
func adapterStringAt(items []string, idx int) string {
	if idx < 0 || idx >= len(items) {
		return ""
	}
	return items[idx]
}

func collapseFatArrows(in []token.Token) []token.Token {
	write := 0
	for read := 0; read < len(in); read++ {
		tok := in[read]
		if read+1 < len(in) && tok.Kind == token.ASSIGN && in[read+1].Kind == token.GT && tok.End.Offset == in[read+1].Pos.Offset {
			tok.Kind = token.ILLEGAL
			tok.Value = "=>"
			tok.End = in[read+1].End
			in[write] = tok
			write++
			read++
			continue
		}
		if write != read {
			in[write] = tok
		}
		write++
	}
	clear(in[write:])
	return in[:write]
}
