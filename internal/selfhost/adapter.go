package selfhost

import (
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
	lexed := ostyLexSource(string(src))
	text := lexed.source
	rt := newRuneTable(text)
	stream := lexed.stream
	return adaptLexStream(rt, stream, ostyLexFactsFromStream(text, stream))
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
	semantic *AstFile
	lexDiags []*diag.Diagnostic
	diags    []*diag.Diagnostic
	adapted  bool
}

// Run executes the self-hosted lexer and parser once and keeps all adapted
// public surfaces available through FrontendRun methods.
func Run(src []byte) *FrontendRun {
	return runFrontend(src, false)
}

func runFrontend(src []byte, adaptTokens bool) *FrontendRun {
	lexed := ostyLexSource(string(src))
	text := lexed.source
	rt := newRuneTable(text)
	stream := lexed.stream
	frontToks := frontTokensFromRuneTable(rt, stream)
	p := newOstyParser(frontToks)
	opParseFile(p)

	run := &FrontendRun{text: text, rt: rt, stream: stream, parser: p}
	if adaptTokens {
		run.ensureLexAdapted()
	}
	return run
}

func frontTokensFromRuneTable(rt runeTable, stream *FrontLexStream) []*FrontToken {
	if stream == nil || len(stream.tokens) == 0 {
		return nil
	}
	values := make([]FrontToken, len(stream.tokens))
	parseTokens := make([]*FrontToken, len(stream.tokens))
	for i, tok := range stream.tokens {
		values[i] = FrontToken{
			kind:        tok.kind,
			text:        rt.slice(tok.start.offset, tok.start.offset+tok.length),
			startOffset: tok.start.offset,
			endOffset:   tok.end.offset,
		}
		parseTokens[i] = &values[i]
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

// File returns the public semantic AST for this front-end pass.
//
// First call materializes the *ast.File from the already-lowered semantic
// arena via astLowerPublicFile. Subsequent calls return the cached result
// without touching astbridge again, so each FrontendRun contributes at most one
// lowering to AstbridgeLowerCount regardless of how many callers poke it.
//
// Deprecated: production front-end paths should keep FrontendRun / arena /
// structured results as the source of truth. Use LowerPublicFileFromRun only at
// explicit public-AST compatibility boundaries.
func (r *FrontendRun) File() *ast.File {
	if r.file != nil {
		return r.file
	}
	atomic.AddInt64(&astbridgeLowerCount, 1)
	arena := r.parser.arena
	if semantic := r.semanticAstFile(); semantic != nil && semantic.arena != nil {
		arena = semantic.arena
	}
	r.file = lowerPublicFileFromArena(arena, r.Tokens())
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

func (r *FrontendRun) semanticAstFile() *AstFile {
	if r == nil {
		return nil
	}
	if r.semantic != nil {
		return r.semantic
	}
	r.semantic = selfhostSemanticAstFile(r.astFile())
	return r.semantic
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
		tok := token.Token{
			Kind:       mapTokenKind(ft.kind),
			Pos:        rt.pos(ft.start),
			End:        rt.pos(ft.end),
			Value:      adapterStringAt(facts.tokenTexts, len(toks)),
			Triple:     ft.triple,
			LeadingDoc: adapterStringAt(facts.leadingDocs, len(toks)),
		}
		fillLiteralParts(&tok, rt, stream, facts, ft.id)
		toks = append(toks, tok)
	}
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
	case *FrontTokenKind_FrontAsQuestion:
		return token.ASQUESTION
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

func fillLiteralParts(tok *token.Token, rt runeTable, stream *FrontLexStream, facts *OstyLexFacts, ownerID int) {
	switch tok.Kind {
	case token.STRING, token.RAWSTRING:
		for _, p := range facts.stringParts {
			if p.ownerTokenID != ownerID {
				continue
			}
			if p.kindCode == int(token.PartExpr) {
				tok.Parts = append(tok.Parts, token.StringPart{
					Kind: token.PartExpr,
					Expr: interpolationTokens(stream, p.exprTokenStart, p.exprTokenCount, rt, facts.interpolationTokenTexts),
				})
				continue
			}
			tok.Parts = append(tok.Parts, token.StringPart{Kind: token.PartText, Text: p.text})
		}
	}
}

func interpolationTokens(stream *FrontLexStream, start, count int, rt runeTable, tokenTexts []string) []token.Token {
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
			Value: adapterStringAt(tokenTexts, i),
		}
		out = append(out, tok)
	}
	return out
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
