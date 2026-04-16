package lsp

import (
	"github.com/osty/osty/internal/lexer"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/token"
)

// semanticTokenTypes is the legend the server advertises in
// ServerCapabilities. Indices into this slice appear in each encoded
// token; the client maps them to theme colors.
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

	replyJSON(s.conn, req.ID, &SemanticTokens{Data: encodeSemTokens(sems)})
}

// classifyToken maps one token.Token to its (type, modifiers) pair through
// the self-hosted LSP policy. For identifiers we consult the prebuilt
// offset→Symbol index so function call sites color differently from plain
// variable reads; the lookup is O(1) per token.
func classifyToken(t token.Token, identIndex map[int]*resolve.Symbol) (semToken, bool) {
	symbolKind := ""
	if t.Kind == token.IDENT {
		if sym, ok := identIndex[t.Pos.Offset]; ok && sym != nil {
			symbolKind = sym.Kind.String()
		}
	}
	tokenType, ok := selfhost.LSPSemanticTypeForTokenKind(t.Kind.String(), symbolKind)
	if !ok {
		return semToken{}, false
	}
	return semToken{ttype: tokenType}, true
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
		ttype:  selfhost.LSPSemanticTypeForComment(),
	}
}

// encodeSemTokens turns absolute tokens into the sorted, delta-encoded form
// LSP expects.
func encodeSemTokens(sems []semToken) []uint32 {
	tokens := make([]selfhost.LSPSemanticToken, 0, len(sems))
	for _, t := range sems {
		tokens = append(tokens, selfhost.LSPSemanticToken{
			Line:      t.line,
			Column:    t.col,
			Length:    t.length,
			TokenType: t.ttype,
			Modifiers: t.mods,
		})
	}
	return selfhost.EncodeLSPSemanticTokens(tokens)
}
