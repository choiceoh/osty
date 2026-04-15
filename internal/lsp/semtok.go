package lsp

import (
	"sort"

	"github.com/osty/osty/internal/lexer"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
)

// semanticTokenTypes is the legend the server advertises in
// ServerCapabilities. Indices into this slice appear in each encoded
// token; the client maps them to theme colors. Keep in sync with
// semTypeX constants below.
var semanticTokenTypes = []string{
	"namespace",
	"type",
	"parameter",
	"variable",
	"property",
	"function",
	"keyword",
	"string",
	"number",
	"operator",
	"comment",
	"enumMember",
}

// semanticTokenModifiers complements tokenTypes; packed as a bitmask
// on each emitted token.
var semanticTokenModifiers = []string{
	"declaration",
	"readonly",
}

const (
	semTypeNamespace  = 0
	semTypeType       = 1
	semTypeParameter  = 2
	semTypeVariable   = 3
	semTypeProperty   = 4
	semTypeFunction   = 5
	semTypeKeyword    = 6
	semTypeString     = 7
	semTypeNumber     = 8
	semTypeOperator   = 9
	semTypeComment    = 10
	semTypeEnumMember = 11
)

// semToken is the intermediate form before relative encoding. `mods`
// is always 0 today; kept in the struct so the wire-format (5 ints
// per token) doesn't need a branch when we start emitting modifiers.
type semToken struct {
	line   uint32
	col    uint32 // UTF-16 code unit
	length uint32 // UTF-16 code units
	ttype  uint32
	mods   uint32
}

// handleSemanticTokens answers
// `textDocument/semanticTokens/full`. We lex the buffer (the parser's
// cached token stream isn't directly reachable from docAnalysis, but
// the lexer is fast enough to re-run on demand) and map every
// non-trivia token to a typed LSP entry. Comments come from the
// same Lexer via Comments(), so we get the full picture without a
// second pass over src.
func (s *Server) handleSemanticTokens(req *rpcRequest) {
	var params SemanticTokensParams
	if err := unmarshalParams(req, &params); err != nil {
		_ = s.conn.writeError(req.ID, errInvalidParams, err.Error())
		return
	}
	doc := s.docs.get(params.TextDocument.URI)
	if doc == nil {
		replyJSON(s.conn, req.ID, &SemanticTokens{Data: []uint32{}})
		return
	}

	l := lexer.New(doc.src)
	toks := l.Lex()
	li := doc.analysis.lines
	idx := doc.analysis.identIndex
	var sems []semToken
	for _, tok := range toks {
		if tok.Kind == token.EOF || tok.Kind == token.NEWLINE || tok.Kind == token.ILLEGAL {
			continue
		}
		st, ok := classifyToken(tok, idx)
		if !ok {
			continue
		}
		sems = append(sems, fillPosition(st, li, tok.Pos, tok.End))
	}
	for _, c := range l.Comments() {
		sems = append(sems, commentSemToken(li, c))
	}

	sort.Slice(sems, func(i, j int) bool {
		if sems[i].line != sems[j].line {
			return sems[i].line < sems[j].line
		}
		return sems[i].col < sems[j].col
	})
	replyJSON(s.conn, req.ID, &SemanticTokens{Data: encodeSemTokens(sems)})
}

// classifyToken maps one token.Token to its (type, modifiers) pair.
// For identifiers we consult the prebuilt offset→Symbol index so
// function call sites color differently from plain variable reads;
// the lookup is O(1) per token.
func classifyToken(t token.Token, identIndex map[int]*resolve.Symbol) (semToken, bool) {
	switch t.Kind {
	case token.IDENT:
		if sym, ok := identIndex[t.Pos.Offset]; ok && sym != nil {
			return semToken{ttype: semTypeFromKind(sym.Kind)}, true
		}
		return semToken{ttype: semTypeVariable}, true
	case token.INT, token.FLOAT, token.CHAR, token.BYTE:
		return semToken{ttype: semTypeNumber}, true
	case token.STRING, token.RAWSTRING:
		return semToken{ttype: semTypeString}, true
	}
	if isKeywordKind(t.Kind) {
		return semToken{ttype: semTypeKeyword}, true
	}
	if isOperatorKind(t.Kind) {
		return semToken{ttype: semTypeOperator}, true
	}
	return semToken{}, false
}

// semTypeFromKind maps resolver SymbolKind to a tokenType index.
func semTypeFromKind(k resolve.SymbolKind) uint32 {
	switch k {
	case resolve.SymFn:
		return semTypeFunction
	case resolve.SymStruct, resolve.SymEnum, resolve.SymInterface, resolve.SymTypeAlias:
		return semTypeType
	case resolve.SymBuiltin:
		return semTypeType
	case resolve.SymLet:
		return semTypeVariable
	case resolve.SymParam:
		return semTypeParameter
	case resolve.SymVariant:
		return semTypeEnumMember
	case resolve.SymGeneric:
		return semTypeType
	case resolve.SymPackage:
		return semTypeNamespace
	}
	return semTypeVariable
}

// isKeywordKind reports whether the token kind is one of Osty's
// reserved keywords.
func isKeywordKind(k token.Kind) bool {
	switch k {
	case token.FN, token.STRUCT, token.ENUM, token.INTERFACE,
		token.TYPE, token.LET, token.MUT, token.PUB,
		token.IF, token.ELSE, token.MATCH, token.FOR,
		token.BREAK, token.CONTINUE, token.RETURN, token.USE,
		token.DEFER:
		return true
	}
	return false
}

// isOperatorKind covers the punctuation and operator families. We
// deliberately keep parens/braces/brackets/commas/colons out of
// "operator" because most themes don't color them and the extra
// tokens bloat the data array.
func isOperatorKind(k token.Kind) bool {
	switch k {
	case token.PLUS, token.MINUS, token.STAR, token.SLASH, token.PERCENT,
		token.EQ, token.NEQ, token.LT, token.GT, token.LEQ, token.GEQ,
		token.AND, token.OR, token.NOT,
		token.BITAND, token.BITOR, token.BITXOR, token.BITNOT, token.SHL, token.SHR,
		token.ASSIGN, token.PLUSEQ, token.MINUSEQ, token.STAREQ, token.SLASHEQ,
		token.PERCENTEQ, token.BITANDEQ, token.BITOREQ, token.BITXOREQ,
		token.SHLEQ, token.SHREQ,
		token.QUESTION, token.QDOT, token.QQ,
		token.DOTDOT, token.DOTDOTEQ,
		token.ARROW, token.CHANARROW:
		return true
	}
	return false
}

// fillPosition completes a semToken with line/col/length based on
// the token's source range. Length uses UTF-16 code units to match
// the LSP client expectation.
func fillPosition(st semToken, li *lineIndex, start, end token.Pos) semToken {
	lspStart := li.ostyToLSP(start)
	lspEnd := li.ostyToLSP(end)
	st.line = lspStart.Line
	st.col = lspStart.Character
	if lspEnd.Line == lspStart.Line && lspEnd.Character >= lspStart.Character {
		st.length = lspEnd.Character - lspStart.Character
	} else {
		// Multi-line tokens (triple-quoted strings, block comments)
		// don't have a meaningful single-line length. Give a safe
		// rest-of-line value so the client doesn't crash; themes
		// still color the first line.
		st.length = 1
	}
	return st
}

// commentSemToken converts a token.Comment to a semToken.
func commentSemToken(li *lineIndex, c token.Comment) semToken {
	lspStart := li.ostyToLSP(c.Pos)
	length := uint32(len(c.Text) + 2) // `//` or `/*` prefix bytes approximation
	return semToken{
		line:   lspStart.Line,
		col:    lspStart.Character,
		length: length,
		ttype:  semTypeComment,
	}
}

// encodeSemTokens turns absolute tokens into the delta-encoded form
// LSP expects. Tokens must be sorted by (line, col) before calling.
func encodeSemTokens(sems []semToken) []uint32 {
	data := make([]uint32, 0, 5*len(sems))
	var prevLine, prevCol uint32
	for i, t := range sems {
		deltaLine := t.line - prevLine
		var deltaCol uint32
		if i == 0 || deltaLine != 0 {
			deltaCol = t.col
		} else {
			deltaCol = t.col - prevCol
		}
		data = append(data, deltaLine, deltaCol, t.length, t.ttype, t.mods)
		prevLine = t.line
		prevCol = t.col
	}
	return data
}
