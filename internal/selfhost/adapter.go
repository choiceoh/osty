package selfhost

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// Lex runs the bootstrapped pure-Osty lexer and adapts its stream to the
// compiler's public token surface.
func Lex(src []byte) ([]token.Token, []*diag.Diagnostic, []token.Comment) {
	text := string(src)
	rt := newRuneTable(text)
	stream := frontendLexStream(text)

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
			LeadingDoc: leadingDoc(stream, len(toks), rt),
		}
		fillLiteralParts(&tok, rt, stream, len(toks), startRune, endRune)
		toks = append(toks, tok)
	}
	toks = collapseFatArrows(toks)

	diags := make([]*diag.Diagnostic, 0, len(stream.diagnostics))
	for _, d := range stream.diagnostics {
		diags = append(diags, lexDiagnostic(d, rt))
	}
	fixCharDiagnostics(text, diags)
	diags = append(diags, fatArrowDiagnostics(text, rt)...)

	comments := make([]token.Comment, 0, len(stream.comments))
	for _, c := range stream.comments {
		comments = append(comments, token.Comment{
			Kind:    mapCommentKind(c.kind),
			Pos:     rt.pos(c.start),
			Text:    commentText(c, rt),
			EndLine: c.end.line,
		})
	}
	return toks, diags, comments
}

// ParseDiagnostics runs the bootstrapped pure-Osty parser and returns only
// diagnostics. Full AST lowering is handled by Parse in parse_adapter.go.
func ParseDiagnostics(src []byte) []*diag.Diagnostic {
	text := string(src)
	rt := newRuneTable(text)
	stream := frontendLexStream(text)
	tokens := frontTokensFromSource(text, stream)
	p := newOstyParser(tokens)
	opParseFile(p)

	out := make([]*diag.Diagnostic, 0, len(p.arena.errors))
	for _, e := range p.arena.errors {
		pos := token.Pos{Line: 1, Column: 1}
		if e.tokenIndex >= 0 && e.tokenIndex < len(stream.tokens) {
			pos = rt.pos(stream.tokens[e.tokenIndex].start)
		}
		b := diag.New(diag.Error, e.message).PrimaryPos(pos, "")
		if e.code != "" {
			b.Code(e.code)
		}
		if e.hint != "" {
			b.Hint(e.hint)
		}
		if e.note != "" {
			b.Note(e.note)
		}
		out = append(out, b.Build())
	}
	return out
}

type runeTable struct {
	src       string
	runes     []rune
	byteStart []int
}

func newRuneTable(src string) runeTable {
	rt := runeTable{src: src}
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

func fillLiteralParts(tok *token.Token, rt runeTable, stream *FrontLexStream, owner, startRune, endRune int) {
	switch tok.Kind {
	case token.STRING, token.RAWSTRING:
		for partIdx, p := range stream.stringParts {
			if p.ownerToken != owner {
				continue
			}
			if _, ok := p.kind.(*FrontStringPartKind_FrontStringInterpolation); ok {
				tok.Parts = append(tok.Parts, token.StringPart{
					Kind: token.PartExpr,
					Expr: interpolationTokens(stream, partIdx, rt),
				})
				continue
			}
			raw := rt.slice(p.start.offset, p.end.offset)
			if tok.Triple {
				raw = normalizeTripleSegment(raw)
			} else if tok.Kind == token.STRING {
				raw = decodeEscapes(raw)
			}
			tok.Parts = append(tok.Parts, token.StringPart{Kind: token.PartText, Text: raw})
		}
		if len(tok.Parts) == 0 {
			tok.Parts = []token.StringPart{{Kind: token.PartText, Text: stringContent(tok.Value)}}
		}
	case token.CHAR:
		tok.Value = decodeChar(tok.Value)
	case token.BYTE:
		tok.Value = decodeByte(tok.Value)
	default:
		_ = startRune
		_ = endRune
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

func normalizeTripleSegment(s string) string {
	trimIndent := strings.HasPrefix(s, "\n")
	if strings.HasPrefix(s, "\n") {
		s = s[1:]
	}
	if strings.HasSuffix(s, "\n") {
		s = s[:len(s)-1]
	}
	lines := strings.Split(s, "\n")
	indent := ""
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		for i, r := range line {
			if r != ' ' && r != '\t' {
				indent = line[:i]
				break
			}
		}
		break
	}
	if trimIndent && indent != "" {
		for i, line := range lines {
			lines[i] = strings.TrimPrefix(line, indent)
		}
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func interpolationTokens(stream *FrontLexStream, ownerPart int, rt runeTable) []token.Token {
	var out []token.Token
	for _, it := range stream.interpolationTokens {
		if it.ownerPart != ownerPart || it.token == nil {
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

func stringContent(s string) string {
	switch {
	case len(s) >= 6 && strings.HasPrefix(s, `r"""`) && strings.HasSuffix(s, `"""`):
		return s[4 : len(s)-3]
	case len(s) >= 6 && strings.HasPrefix(s, `"""`) && strings.HasSuffix(s, `"""`):
		return s[3 : len(s)-3]
	case len(s) >= 3 && strings.HasPrefix(s, `r"`) && strings.HasSuffix(s, `"`):
		return s[2 : len(s)-1]
	case len(s) >= 2 && strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`):
		return decodeEscapes(s[1 : len(s)-1])
	}
	return s
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

func leadingDoc(stream *FrontLexStream, owner int, rt runeTable) string {
	if owner < 0 || owner >= len(stream.tokens) || stream.tokens[owner].leadingDocLines <= 0 {
		return ""
	}
	need := stream.tokens[owner].leadingDocLines
	var lines []string
	for i := len(stream.comments) - 1; i >= 0 && len(lines) < need; i-- {
		c := stream.comments[i]
		if _, ok := c.kind.(*FrontCommentKind_FrontCommentDoc); !ok {
			continue
		}
		if c.end.line >= stream.tokens[owner].start.line {
			continue
		}
		lines = append([]string{strings.TrimSpace(commentText(c, rt))}, lines...)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func mapCommentKind(k FrontCommentKind) token.CommentKind {
	switch k.(type) {
	case *FrontCommentKind_FrontCommentDoc:
		return token.CommentDoc
	case *FrontCommentKind_FrontCommentBlock:
		return token.CommentBlock
	default:
		return token.CommentLine
	}
}

func commentText(c *FrontComment, rt runeTable) string {
	raw := rt.src[rt.byteOffset(c.start.offset):rt.byteOffset(c.end.offset)]
	switch c.kind.(type) {
	case *FrontCommentKind_FrontCommentDoc:
		return strings.TrimPrefix(raw, "///")
	case *FrontCommentKind_FrontCommentLine:
		return strings.TrimPrefix(raw, "//")
	case *FrontCommentKind_FrontCommentBlock:
		raw = strings.TrimPrefix(raw, "/*")
		return strings.TrimSuffix(raw, "*/")
	default:
		return raw
	}
}

func lexDiagnostic(d *FrontLexDiagnostic, rt runeTable) *diag.Diagnostic {
	code, msg := lexDiagnosticInfo(d.code)
	b := diag.New(diag.Error, msg).
		Code(code).
		Primary(diag.Span{Start: rt.pos(d.start), End: rt.pos(d.end)}, "")
	if code == diag.CodeUppercaseBasePrefix {
		b.Hint("use lowercase base prefixes: `0x`, `0b`, or `0o`")
	}
	return b.Build()
}

func lexDiagnosticInfo(code FrontLexDiagnosticCode) (string, string) {
	switch code.(type) {
	case *FrontLexDiagnosticCode_FrontDiagUnterminatedBlockComment:
		return diag.CodeUnterminatedComment, "unterminated block comment"
	case *FrontLexDiagnosticCode_FrontDiagUnterminatedString:
		return diag.CodeUnterminatedString, "unterminated string literal"
	case *FrontLexDiagnosticCode_FrontDiagUnterminatedInterpolation:
		return diag.CodeUnterminatedString, "unterminated interpolation in string"
	case *FrontLexDiagnosticCode_FrontDiagNewlineInString:
		return diag.CodeUnterminatedString, "newline in string literal"
	case *FrontLexDiagnosticCode_FrontDiagTripleMissingLeadingNewline, *FrontLexDiagnosticCode_FrontDiagBadTripleIndent:
		return diag.CodeBadTripleString, "invalid triple-quoted string"
	case *FrontLexDiagnosticCode_FrontDiagUppercaseBasePrefix:
		return diag.CodeUppercaseBasePrefix, "uppercase base prefix is not allowed"
	case *FrontLexDiagnosticCode_FrontDiagUnknownEscape:
		return diag.CodeUnknownEscape, "unknown escape sequence"
	case *FrontLexDiagnosticCode_FrontDiagEmptyChar:
		return diag.CodeUnterminatedString, "empty char literal"
	case *FrontLexDiagnosticCode_FrontDiagEmptyByte:
		return diag.CodeUnterminatedString, "empty byte literal"
	default:
		return diag.CodeIllegalCharacter, "illegal character"
	}
}

func collapseFatArrows(in []token.Token) []token.Token {
	out := make([]token.Token, 0, len(in))
	for i := 0; i < len(in); i++ {
		if i+1 < len(in) && in[i].Kind == token.ASSIGN && in[i+1].Kind == token.GT && in[i].End.Offset == in[i+1].Pos.Offset {
			tok := in[i]
			tok.Kind = token.ILLEGAL
			tok.Value = "=>"
			tok.End = in[i+1].End
			out = append(out, tok)
			i++
			continue
		}
		out = append(out, in[i])
	}
	return out
}

func fixCharDiagnostics(src string, diags []*diag.Diagnostic) {
	for _, d := range diags {
		if d.Message != "empty char literal" && d.Message != "empty byte literal" {
			continue
		}
		off := d.PrimaryPos().Offset
		if d.Message == "empty byte literal" && off+2 < len(src) && src[off] == 'b' && src[off+1] == '\'' && (src[off+2] == '\n' || src[off+2] == '\r') {
			d.Message = "unterminated byte literal"
			continue
		}
		if off+1 < len(src) && src[off] == '\'' && (src[off+1] == '\n' || src[off+1] == '\r') {
			d.Message = "unterminated char literal"
		}
	}
}

func fatArrowDiagnostics(src string, rt runeTable) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	runeOffset := 0
	for i := 0; i < len(src)-1; {
		r, sz := utf8.DecodeRuneInString(src[i:])
		if r == '=' && src[i+1] == '>' {
			pos := posFromByte(src, i, runeOffset)
			end := pos
			end.Offset += 2
			end.Column += 2
			out = append(out, diag.New(diag.Error, "`=>` is not valid Osty syntax; use `->`").
				Code(diag.CodeFatArrowRemoved).
				Primary(diag.Span{Start: pos, End: end}, "").
				Build())
		}
		i += sz
		runeOffset++
	}
	_ = rt
	return out
}

func posFromByte(src string, byteOff, runeOff int) token.Pos {
	line, col := 1, 1
	seen := 0
	for off, r := range src {
		if off >= byteOff || seen >= runeOff {
			break
		}
		if r == '\n' {
			line++
			col = 1
		} else {
			col++
		}
		seen++
	}
	return token.Pos{Offset: byteOff, Line: line, Column: col}
}
