package cst

import "github.com/osty/osty/internal/token"

// ParseGreen parses a token stream directly into a lossless Red/Green tree.
// It is independent of the semantic AST arena: the parser emits GreenBuilder
// events from tokens, using checkpoints for Pratt-style expression wrapping.
//
// The parser is intentionally recovery-first. When it cannot classify a
// region precisely, it still consumes tokens into the nearest enclosing node
// so byte coverage and Red navigation remain reliable.
func ParseGreen(src []byte, toks []token.Token, trivias []Trivia) *Tree {
	p := newGreenParser(src, toks, trivias)
	return p.parse()
}

type greenParser struct {
	src        []byte
	toks       []token.Token
	leading    [][]int
	trailing   [][]int
	tailTrivia []int
	triviaIDs  []int
	b          *GreenBuilder
	pos        int

	// The lexer keeps `>>` as one SHR token. In type-argument context that
	// single token can close both the inner and outer generic lists, so the
	// parser records the extra close for the enclosing list.
	pendingTypeArgClosers int
}

func newGreenParser(src []byte, toks []token.Token, trivias []Trivia) *greenParser {
	b := NewBuilder(nil)
	arena := b.Arena()
	triviaIDs := make([]int, len(trivias))
	for i, tr := range trivias {
		triviaIDs[i] = arena.AddTrivia(tr)
	}
	leading, trailing, tailTrivia := pairTriviaToTokens(toks, trivias)
	return &greenParser{
		src:        src,
		toks:       toks,
		leading:    leading,
		trailing:   trailing,
		tailTrivia: tailTrivia,
		triviaIDs:  triviaIDs,
		b:          b,
	}
}

func (p *greenParser) parse() *Tree {
	p.b.StartNode(GkFile)
	for !p.at(token.EOF) {
		if p.at(token.NEWLINE) {
			p.emit()
			continue
		}
		start := p.pos
		p.parseTopLevel()
		p.recoverIfStalled(start)
	}
	if len(p.tailTrivia) > 0 {
		p.b.Token(GkErrorMissing, 0, "", 0, translateTriviaIDs(p.tailTrivia, p.triviaIDs), nil)
	}
	p.b.FinishNode()
	arena, root := p.b.Finish()
	return NewTreeFromSource(arena, root, p.src)
}

func (p *greenParser) parseTopLevel() {
	cp := p.b.Checkpoint()
	p.parseAnnotations()
	kind := p.topLevelKind()
	if kind == GkUsePath {
		p.b.StartNodeAt(GkUsePath, cp)
		p.parseUseDecl(true)
		p.b.FinishNode()
		return
	}
	if kind != GkNone {
		p.b.StartNodeAt(kind, cp)
		switch kind {
		case GkFnDecl:
			p.parseFnDecl()
		case GkStructDecl:
			p.parseAggregateDecl(GkStructDecl)
		case GkEnumDecl:
			p.parseAggregateDecl(GkEnumDecl)
		case GkInterfaceDecl:
			p.parseAggregateDecl(GkInterfaceDecl)
		case GkTypeAlias:
			p.parseTypeAlias()
		case GkUseDecl:
			p.parseUseDecl(false)
		case GkLetDecl:
			p.parseLet()
		default:
			p.parseStmtBody(kind)
		}
		p.b.FinishNode()
		return
	}
	p.parseStmt()
}

func (p *greenParser) topLevelKind() GreenKind {
	i := p.pos
	if p.kindAt(i) == token.PUB {
		i++
	}
	switch p.kindAt(i) {
	case token.FN:
		return GkFnDecl
	case token.STRUCT:
		return GkStructDecl
	case token.ENUM:
		return GkEnumDecl
	case token.INTERFACE:
		return GkInterfaceDecl
	case token.TYPE:
		return GkTypeAlias
	case token.USE:
		if p.useLooksGrouped(i) {
			return GkUsePath
		}
		return GkUseDecl
	case token.LET:
		return GkLetDecl
	case token.RETURN:
		return GkReturnStmt
	case token.BREAK:
		return GkBreakStmt
	case token.CONTINUE:
		return GkContinueStmt
	case token.DEFER:
		return GkDeferStmt
	case token.FOR:
		return GkForStmt
	}
	return GkNone
}

func (p *greenParser) parseAnnotations() {
	for p.at(token.HASH) && p.kindAt(p.pos+1) == token.LBRACKET {
		p.b.StartNode(GkAnnotation)
		p.emit() // #
		p.emit() // [
		for !p.at(token.RBRACKET) && !p.at(token.EOF) {
			if p.at(token.LPAREN) {
				p.emit()
				for !p.at(token.RPAREN) && !p.at(token.EOF) {
					if p.at(token.COMMA) || p.at(token.NEWLINE) {
						p.emit()
						continue
					}
					p.b.StartNode(GkAnnotationArg)
					p.parseExprUntil(token.COMMA, token.RPAREN)
					p.b.FinishNode()
				}
				p.eat(token.RPAREN)
				continue
			}
			if p.at(token.ASSIGN) {
				p.emit()
				p.b.StartNode(GkAnnotationArg)
				p.parseExprUntil(token.RBRACKET)
				p.b.FinishNode()
				continue
			}
			p.emit()
		}
		p.eat(token.RBRACKET)
		p.b.FinishNode()
		for p.at(token.NEWLINE) {
			p.emit()
		}
	}
}

func (p *greenParser) parseUseDecl(grouped bool) {
	p.eat(token.PUB)
	p.eat(token.USE)
	p.parseUsePathUntil(token.LBRACE, token.NEWLINE, token.EOF)
	for !p.at(token.EOF) && !p.at(token.NEWLINE) {
		if grouped && p.at(token.LBRACE) {
			p.emit()
			for !p.at(token.RBRACE) && !p.at(token.EOF) {
				if p.at(token.COMMA) || p.at(token.NEWLINE) {
					p.emit()
					continue
				}
				p.parseGroupedUseDecl()
			}
			p.eat(token.RBRACE)
			continue
		}
		if p.atUseAliasKeyword() {
			p.parseUseAlias()
			continue
		}
		if p.at(token.LBRACE) {
			p.parseBalancedNode(GkUseFFIBody, token.LBRACE, token.RBRACE)
			continue
		}
		start := p.pos
		p.parseUsePathUntil(token.LBRACE, token.NEWLINE, token.EOF)
		p.recoverIfStalled(start)
	}
}

func (p *greenParser) parseGroupedUseDecl() {
	p.b.StartNode(GkUseDecl)
	p.parseUsePathUntil(token.COMMA, token.RBRACE, token.NEWLINE, token.EOF)
	if p.atUseAliasKeyword() {
		p.parseUseAlias()
	}
	p.b.FinishNode()
}

func (p *greenParser) parseUsePathUntil(stops ...token.Kind) {
	if p.atAny(stops...) || p.atUseAliasKeyword() || p.at(token.EOF) {
		return
	}
	p.b.StartNode(GkUsePath)
	for !p.atAny(stops...) && !p.atUseAliasKeyword() && !p.at(token.EOF) {
		p.emit()
	}
	p.b.FinishNode()
}

func (p *greenParser) parseUseAlias() {
	p.b.StartNode(GkUseAlias)
	p.emit()
	if p.at(token.IDENT) {
		p.emit()
	}
	p.b.FinishNode()
}

func (p *greenParser) parseFnDecl() {
	p.eat(token.PUB)
	p.eat(token.FN)
	p.eat(token.IDENT)
	p.parseGenericParamList()
	p.parseParamList()
	if p.eat(token.ARROW) {
		p.parseTypeUntil(token.LBRACE, token.NEWLINE, token.EOF)
	}
	if p.at(token.LBRACE) {
		p.parseBlock()
	}
}

func (p *greenParser) parseParamList() {
	if !p.at(token.LPAREN) {
		return
	}
	p.b.StartNode(GkParamList)
	p.emit()
	for !p.at(token.RPAREN) && !p.at(token.EOF) {
		if p.at(token.COMMA) || p.at(token.NEWLINE) {
			p.emit()
			continue
		}
		start := p.pos
		p.b.StartNode(GkParam)
		p.eat(token.MUT)
		p.eat(token.IDENT)
		p.eat(token.UNDERSCORE)
		if p.eat(token.COLON) {
			p.parseTypeUntil(token.COMMA, token.RPAREN, token.ASSIGN)
		}
		if p.eat(token.ASSIGN) {
			p.parseExprUntil(token.COMMA, token.RPAREN)
		}
		p.recoverIfStalled(start)
		p.b.FinishNode()
	}
	p.eat(token.RPAREN)
	p.b.FinishNode()
}

func (p *greenParser) parseAggregateDecl(kind GreenKind) {
	p.eat(token.PUB)
	p.emit() // struct/enum/interface
	p.eat(token.IDENT)
	p.parseGenericParamList()
	if !p.at(token.LBRACE) {
		return
	}
	p.emit()
	if kind == GkEnumDecl {
		p.b.StartNode(GkVariantList)
	}
	for !p.at(token.RBRACE) && !p.at(token.EOF) {
		if p.at(token.NEWLINE) || p.at(token.COMMA) {
			p.emit()
			continue
		}
		cp := p.b.Checkpoint()
		p.parseAnnotations()
		if p.kindAfterPub() == token.FN {
			p.b.StartNodeAt(GkFnDecl, cp)
			p.parseFnDecl()
			p.b.FinishNode()
			continue
		}
		switch kind {
		case GkStructDecl:
			p.b.StartNodeAt(GkField, cp)
			start := p.pos
			p.parseFieldLike()
			p.recoverIfStalled(start)
			p.b.FinishNode()
		case GkEnumDecl:
			p.b.StartNodeAt(GkVariant, cp)
			start := p.pos
			p.parseVariant()
			p.recoverIfStalled(start)
			p.b.FinishNode()
		case GkInterfaceDecl:
			p.b.StartNodeAt(GkNamedType, cp)
			start := p.pos
			p.parseTypeUntil(token.NEWLINE, token.RBRACE)
			p.recoverIfStalled(start)
			p.b.FinishNode()
		}
	}
	if kind == GkEnumDecl {
		p.b.FinishNode()
	}
	p.eat(token.RBRACE)
}

func (p *greenParser) parseFieldLike() {
	p.eat(token.PUB)
	p.eat(token.IDENT)
	if p.eat(token.COLON) {
		p.parseTypeUntil(token.ASSIGN, token.COMMA, token.NEWLINE, token.RBRACE)
	}
	if p.eat(token.ASSIGN) {
		p.parseExprUntil(token.COMMA, token.NEWLINE, token.RBRACE)
	}
}

func (p *greenParser) parseVariant() {
	p.eat(token.IDENT)
	if p.at(token.LPAREN) {
		p.b.StartNode(GkFieldList)
		p.emit()
		for !p.at(token.RPAREN) && !p.at(token.EOF) {
			if p.at(token.COMMA) || p.at(token.NEWLINE) {
				p.emit()
				continue
			}
			p.parseTypeUntil(token.COMMA, token.RPAREN)
		}
		p.eat(token.RPAREN)
		p.b.FinishNode()
	}
}

func (p *greenParser) parseTypeAlias() {
	p.eat(token.PUB)
	p.eat(token.TYPE)
	p.eat(token.IDENT)
	p.parseGenericParamList()
	if p.eat(token.ASSIGN) {
		p.parseTypeUntil(token.NEWLINE, token.EOF)
	}
}

func (p *greenParser) parseGenericParamList() {
	if !p.at(token.LT) {
		return
	}
	p.b.StartNode(GkGenericParamList)
	p.emit()
	for !p.at(token.GT) && !p.at(token.EOF) {
		if p.at(token.SHR) {
			p.emit()
			break
		}
		if p.at(token.COMMA) || p.at(token.NEWLINE) {
			p.emit()
			continue
		}
		start := p.pos
		p.b.StartNode(GkGenericParam)
		p.eat(token.IDENT)
		if p.eat(token.COLON) {
			p.parseGenericBounds()
		}
		if p.pos == start {
			p.emit()
		}
		p.b.FinishNode()
	}
	p.eat(token.GT)
	p.b.FinishNode()
}

func (p *greenParser) parseGenericBounds() {
	for !p.atAny(token.COMMA, token.GT, token.SHR, token.NEWLINE) && !p.at(token.EOF) {
		start := p.pos
		p.b.StartNode(GkGenericBound)
		p.parseTypeUntil(token.PLUS, token.COMMA, token.GT, token.SHR, token.NEWLINE)
		p.recoverIfStalled(start)
		p.b.FinishNode()
		if !p.eat(token.PLUS) {
			return
		}
	}
}

func (p *greenParser) parseStmt() {
	cp := p.b.Checkpoint()
	p.parseAnnotations()
	kind := p.stmtKind()
	if kind == GkNone {
		start := p.pos
		p.b.StartNodeAt(GkExprStmt, cp)
		p.parseExprsUntil(token.NEWLINE, token.RBRACE, token.EOF)
		p.recoverIfStalled(start)
		p.b.FinishNode()
		return
	}
	p.b.StartNodeAt(kind, cp)
	p.parseStmtBody(kind)
	p.b.FinishNode()
}

func (p *greenParser) stmtKind() GreenKind {
	if p.at(token.LABEL) && p.kindAt(p.pos+1) == token.COLON && p.kindAt(p.pos+2) == token.FOR {
		return GkForStmt
	}
	switch p.kindAfterPub() {
	case token.LET:
		return GkLetStmt
	case token.RETURN:
		return GkReturnStmt
	case token.BREAK:
		return GkBreakStmt
	case token.CONTINUE:
		return GkContinueStmt
	case token.DEFER:
		return GkDeferStmt
	case token.FOR:
		return GkForStmt
	}
	return GkNone
}

func (p *greenParser) parseStmtBody(kind GreenKind) {
	switch kind {
	case GkLetStmt, GkLetDecl:
		p.parseLet()
	case GkReturnStmt, GkBreakStmt, GkContinueStmt, GkDeferStmt:
		p.emit()
		p.parseExprsUntil(token.NEWLINE, token.RBRACE, token.EOF)
	case GkForStmt:
		p.parseForStmt()
	}
}

func (p *greenParser) parseForStmt() {
	if p.at(token.LABEL) && p.kindAt(p.pos+1) == token.COLON {
		p.emit()
		p.emit()
	}
	p.eat(token.FOR)
	switch {
	case p.at(token.LBRACE):
		// Infinite loop: `for { ... }`.
	case p.eat(token.LET):
		p.parsePatternUntil(token.ASSIGN, token.LBRACE, token.NEWLINE, token.EOF)
		p.eat(token.ASSIGN)
		p.parseExprsUntil(token.LBRACE, token.NEWLINE, token.EOF)
	case p.forHeaderHasInBeforeBody():
		p.parsePattern(token.LBRACE, token.NEWLINE, token.EOF)
		if p.atIdentValue("in") {
			p.emit()
		}
		p.parseExprsUntil(token.LBRACE, token.NEWLINE, token.EOF)
	default:
		p.parseExprsUntil(token.LBRACE, token.NEWLINE, token.EOF)
	}
	if p.at(token.LBRACE) {
		p.parseBlock()
	}
}

func (p *greenParser) parseLet() {
	p.eat(token.PUB)
	p.eat(token.LET)
	p.eat(token.MUT)
	p.parsePatternUntil(token.COLON, token.ASSIGN, token.NEWLINE, token.RBRACE, token.EOF)
	if p.eat(token.COLON) {
		p.parseTypeUntil(token.ASSIGN, token.NEWLINE, token.RBRACE, token.EOF)
	}
	if p.eat(token.ASSIGN) {
		p.parseExprUntil(token.NEWLINE, token.RBRACE, token.EOF)
	}
}

func (p *greenParser) parseBlock() {
	p.b.StartNode(GkBlock)
	p.eat(token.LBRACE)
	for !p.at(token.RBRACE) && !p.at(token.EOF) {
		if p.at(token.NEWLINE) {
			p.emit()
			continue
		}
		p.parseStmt()
	}
	p.eat(token.RBRACE)
	p.b.FinishNode()
}

func (p *greenParser) parsePatternUntil(stops ...token.Kind) {
	for !p.atAny(stops...) && !p.at(token.EOF) {
		start := p.pos
		p.parsePattern(stops...)
		p.recoverIfStalled(start)
	}
}

func (p *greenParser) parsePattern(stops ...token.Kind) GreenCheckpoint {
	cp := p.b.Checkpoint()
	p.parsePatternRange(stops...)
	for p.at(token.BITOR) && !tokenKindIn(stops, token.BITOR) {
		p.b.StartNodeAt(GkOrPat, cp)
		p.emit()
		p.parsePatternRange(stops...)
		p.b.FinishNode()
	}
	return cp
}

func (p *greenParser) parsePatternRange(stops ...token.Kind) GreenCheckpoint {
	cp := p.b.Checkpoint()
	p.parsePatternAtom(stops...)
	if p.at(token.DOTDOT) || p.at(token.DOTDOTEQ) {
		p.b.StartNodeAt(GkRangePat, cp)
		p.emit()
		if !p.patternAtEnd(stops...) {
			p.parsePatternAtom(stops...)
		}
		p.b.FinishNode()
	}
	return cp
}

func (p *greenParser) parsePatternAtom(stops ...token.Kind) {
	if p.atAny(stops...) || p.at(token.EOF) {
		return
	}
	switch p.peek().Kind {
	case token.UNDERSCORE:
		p.parseLeaf(GkWildcardPat)
	case token.INT, token.FLOAT, token.STRING, token.RAWSTRING, token.CHAR, token.BYTE:
		p.parseLeaf(GkLiteralPat)
	case token.MINUS:
		p.b.StartNode(GkLiteralPat)
		p.emit()
		if !p.patternAtEnd(stops...) {
			p.parsePatternAtom(stops...)
		}
		p.b.FinishNode()
	case token.DOTDOT, token.DOTDOTEQ:
		p.b.StartNode(GkRangePat)
		p.emit()
		if !p.patternAtEnd(stops...) {
			p.parsePatternAtom(stops...)
		}
		p.b.FinishNode()
	case token.LPAREN:
		p.parseTuplePattern()
	case token.IDENT:
		p.parseNamedPattern(stops...)
	default:
		p.b.StartNode(GkError)
		p.emit()
		p.b.FinishNode()
	}
}

func (p *greenParser) parseTuplePattern() {
	p.b.StartNode(GkTuplePat)
	p.eat(token.LPAREN)
	for !p.at(token.RPAREN) && !p.at(token.EOF) {
		if p.at(token.COMMA) || p.at(token.NEWLINE) {
			p.emit()
			continue
		}
		start := p.pos
		p.parsePattern(token.COMMA, token.RPAREN)
		p.recoverIfStalled(start)
	}
	p.eat(token.RPAREN)
	p.b.FinishNode()
}

func (p *greenParser) parseNamedPattern(stops ...token.Kind) {
	cp := p.b.Checkpoint()
	segments := 0
	for {
		if !p.eat(token.IDENT) {
			break
		}
		segments++
		if !(p.at(token.DOT) && p.kindAt(p.pos+1) == token.IDENT) {
			break
		}
		p.emit()
	}
	if segments == 1 && (p.previousTokenValue() == "true" || p.previousTokenValue() == "false") {
		p.b.StartNodeAt(GkLiteralPat, cp)
		p.b.FinishNode()
		return
	}
	if segments == 1 && p.at(token.AT) {
		p.b.StartNodeAt(GkBindingPat, cp)
		p.emit()
		if !p.patternAtEnd(stops...) {
			p.parsePatternRange(stops...)
		}
		p.b.FinishNode()
		return
	}
	if p.at(token.LPAREN) {
		p.b.StartNodeAt(GkVariantPat, cp)
		p.emit()
		for !p.at(token.RPAREN) && !p.at(token.EOF) {
			if p.at(token.COMMA) || p.at(token.NEWLINE) {
				p.emit()
				continue
			}
			start := p.pos
			p.parsePattern(token.COMMA, token.RPAREN)
			p.recoverIfStalled(start)
		}
		p.eat(token.RPAREN)
		p.b.FinishNode()
		return
	}
	if p.at(token.LBRACE) {
		p.b.StartNodeAt(GkStructPat, cp)
		p.emit()
		for !p.at(token.RBRACE) && !p.at(token.EOF) {
			if p.at(token.COMMA) || p.at(token.NEWLINE) {
				p.emit()
				continue
			}
			if p.at(token.DOTDOT) || p.at(token.DOTDOTEQ) {
				p.emit()
				continue
			}
			start := p.pos
			p.b.StartNode(GkStructPatField)
			if p.eat(token.IDENT) {
				if p.eat(token.COLON) {
					p.parsePattern(token.COMMA, token.RBRACE)
				}
			} else {
				p.parsePattern(token.COMMA, token.RBRACE)
			}
			p.recoverIfStalled(start)
			p.b.FinishNode()
		}
		p.eat(token.RBRACE)
		p.b.FinishNode()
		return
	}
	if segments > 1 {
		p.b.StartNodeAt(GkVariantPat, cp)
		p.b.FinishNode()
		return
	}
	p.b.StartNodeAt(GkIdentPat, cp)
	p.b.FinishNode()
}

func (p *greenParser) patternAtEnd(stops ...token.Kind) bool {
	return p.atAny(stops...) || p.at(token.BITOR) || p.at(token.EOF)
}

func (p *greenParser) parseTypeUntil(stops ...token.Kind) {
	for !p.atAny(stops...) && !p.at(token.EOF) {
		start := p.pos
		if p.parseTypeAtom(stops...) {
			continue
		}
		p.emit()
		if p.pos == start {
			return
		}
	}
}

func (p *greenParser) parseTypeAtom(stops ...token.Kind) bool {
	switch {
	case p.at(token.FN):
		p.parseFunctionType(stops...)
		p.parseOptionalTypeSuffixes()
		return true
	case p.at(token.LPAREN):
		if p.looksLikeUnitType() {
			p.parseUnitType()
		} else {
			p.parseTupleType()
		}
		p.parseOptionalTypeSuffixes()
		return true
	case p.at(token.LBRACKET):
		p.parseListType()
		p.parseOptionalTypeSuffixes()
		return true
	case p.at(token.IDENT) && p.peek().Value == "Self" && p.kindAt(p.pos+1) != token.DOT:
		p.b.StartNode(GkSelfType)
		p.emit()
		p.b.FinishNode()
		p.parseOptionalTypeSuffixes()
		return true
	case p.at(token.IDENT) || p.at(token.UNDERSCORE):
		p.b.StartNode(GkNamedType)
		p.emit()
		for p.at(token.DOT) && p.kindAt(p.pos+1) == token.IDENT {
			p.emit()
			p.emit()
		}
		if p.at(token.LT) {
			p.parseTypeArgList()
		}
		p.b.FinishNode()
		p.parseOptionalTypeSuffixes()
		return true
	}
	return false
}

func (p *greenParser) parseFunctionType(stops ...token.Kind) {
	p.b.StartNode(GkFunctionType)
	p.eat(token.FN)
	if p.at(token.LPAREN) {
		p.b.StartNode(GkParamList)
		p.emit()
		for !p.at(token.RPAREN) && !p.at(token.EOF) {
			if p.at(token.COMMA) || p.at(token.NEWLINE) {
				p.emit()
				continue
			}
			start := p.pos
			p.b.StartNode(GkParam)
			p.parseTypeUntil(token.COMMA, token.RPAREN)
			p.recoverIfStalled(start)
			p.b.FinishNode()
		}
		p.eat(token.RPAREN)
		p.b.FinishNode()
	}
	if p.eat(token.ARROW) {
		p.parseTypeUntil(stops...)
	}
	p.b.FinishNode()
}

func (p *greenParser) parseTupleType() {
	p.b.StartNode(GkTupleType)
	p.eat(token.LPAREN)
	for !p.at(token.RPAREN) && !p.at(token.EOF) {
		if p.at(token.COMMA) || p.at(token.NEWLINE) {
			p.emit()
			continue
		}
		start := p.pos
		p.parseTypeUntil(token.COMMA, token.RPAREN)
		p.recoverIfStalled(start)
	}
	p.eat(token.RPAREN)
	p.b.FinishNode()
}

func (p *greenParser) parseUnitType() {
	p.b.StartNode(GkUnitType)
	p.eat(token.LPAREN)
	for p.at(token.NEWLINE) {
		p.emit()
	}
	p.eat(token.RPAREN)
	p.b.FinishNode()
}

func (p *greenParser) parseListType() {
	p.b.StartNode(GkListType)
	p.eat(token.LBRACKET)
	for !p.at(token.RBRACKET) && !p.at(token.EOF) {
		if p.at(token.COMMA) || p.at(token.NEWLINE) {
			p.emit()
			continue
		}
		start := p.pos
		p.parseTypeUntil(token.COMMA, token.RBRACKET)
		p.recoverIfStalled(start)
	}
	p.eat(token.RBRACKET)
	p.b.FinishNode()
}

func (p *greenParser) parseTypeArgList() {
	if !p.at(token.LT) {
		return
	}
	p.b.StartNode(GkGenericParamList)
	p.emit()
	for !p.at(token.EOF) {
		if p.pendingTypeArgClosers > 0 {
			p.pendingTypeArgClosers--
			break
		}
		if p.at(token.GT) {
			p.emit()
			break
		}
		if p.at(token.SHR) {
			p.emit()
			p.pendingTypeArgClosers++
			break
		}
		if p.at(token.COMMA) || p.at(token.NEWLINE) {
			p.emit()
			continue
		}
		start := p.pos
		p.b.StartNode(GkGenericParam)
		p.parseTypeUntil(token.COMMA, token.GT, token.SHR, token.NEWLINE)
		if p.pos == start {
			p.emit()
		}
		p.b.FinishNode()
		if p.pendingTypeArgClosers > 0 {
			p.pendingTypeArgClosers--
			break
		}
	}
	p.b.FinishNode()
}

func (p *greenParser) parseOptionalTypeSuffixes() {
	for p.at(token.QUESTION) || p.at(token.QQ) {
		p.b.StartNode(GkOptionalType)
		p.emit()
		p.b.FinishNode()
	}
}

func (p *greenParser) parseExprUntil(stops ...token.Kind) {
	if p.atAny(stops...) || p.at(token.EOF) {
		return
	}
	p.parseExprBP(0, stops...)
}

func (p *greenParser) parseExprsUntil(stops ...token.Kind) {
	for !p.atAny(stops...) && !p.at(token.EOF) {
		start := p.pos
		p.parseExprUntil(stops...)
		p.recoverIfStalled(start)
	}
}

func (p *greenParser) parseExprBP(minBP int, stops ...token.Kind) GreenCheckpoint {
	lhs := p.b.Checkpoint()
	p.parsePrefix(stops...)
	for {
		if p.atAny(stops...) || p.at(token.EOF) {
			break
		}
		if p.parsePostfix(lhs, stops...) {
			continue
		}
		lbp, rbp, ok := infixBindingPower(p.peek().Kind)
		if !ok || lbp < minBP {
			break
		}
		p.b.StartNodeAt(p.infixKind(), lhs)
		p.emit()
		p.parseExprBP(rbp, stops...)
		p.b.FinishNode()
	}
	return lhs
}

func (p *greenParser) parsePrefix(stops ...token.Kind) {
	if p.atAny(stops...) || p.at(token.EOF) {
		return
	}
	if p.atLoopExprStart() {
		p.parseLoop()
		return
	}
	switch p.peek().Kind {
	case token.MINUS, token.NOT, token.BITNOT, token.STAR:
		p.b.StartNode(GkUnary)
		p.emit()
		p.parseExprBP(13, stops...)
		p.b.FinishNode()
	case token.INT:
		p.parseLeaf(GkIntLit)
	case token.FLOAT:
		p.parseLeaf(GkFloatLit)
	case token.STRING:
		p.parseLeaf(GkStringLit)
	case token.RAWSTRING:
		p.parseLeaf(GkRawStringLit)
	case token.CHAR:
		p.parseLeaf(GkCharLit)
	case token.BYTE:
		p.parseLeaf(GkByteLit)
	case token.IDENT, token.UNDERSCORE, token.LABEL:
		if p.peek().Kind == token.IDENT && (p.peek().Value == "true" || p.peek().Value == "false") {
			p.parseLeaf(GkBoolLit)
			return
		}
		p.parseLeaf(GkIdent)
	case token.LPAREN:
		p.parseParenOrTuple()
	case token.LBRACKET:
		p.parseList()
	case token.LBRACE:
		if p.looksLikeMapLit() {
			p.parseMap()
		} else {
			p.parseBlock()
		}
	case token.IF:
		p.parseIf()
	case token.MATCH:
		p.parseMatch()
	case token.BITOR, token.OR:
		p.parseClosure(stops...)
	default:
		p.b.StartNode(GkError)
		p.emit()
		p.b.FinishNode()
	}
}

func (p *greenParser) parsePostfix(lhs GreenCheckpoint, stops ...token.Kind) bool {
	switch p.peek().Kind {
	case token.LPAREN:
		p.b.StartNodeAt(GkCall, lhs)
		p.emit()
		for !p.at(token.RPAREN) && !p.at(token.EOF) {
			if p.at(token.COMMA) || p.at(token.NEWLINE) {
				p.emit()
				continue
			}
			p.parseExprUntil(token.COMMA, token.RPAREN)
		}
		p.eat(token.RPAREN)
		p.b.FinishNode()
		return true
	case token.LBRACKET:
		p.b.StartNodeAt(GkIndex, lhs)
		p.emit()
		p.parseExprUntil(token.RBRACKET)
		p.eat(token.RBRACKET)
		p.b.FinishNode()
		return true
	case token.DOT, token.QDOT:
		p.b.StartNodeAt(GkFieldAccess, lhs)
		p.emit()
		p.eat(token.IDENT)
		p.b.FinishNode()
		return true
	case token.QUESTION, token.ASQUESTION:
		p.b.StartNodeAt(GkQuestion, lhs)
		p.emit()
		if !p.atAny(stops...) && !p.at(token.EOF) && p.peek().Kind == token.IDENT {
			p.parseTypeUntil(stops...)
		}
		p.b.FinishNode()
		return true
	case token.COLONCOLON:
		p.b.StartNodeAt(GkTurbofish, lhs)
		p.emit()
		if p.at(token.LT) {
			p.parseGenericParamList()
		}
		p.b.FinishNode()
		return true
	case token.LBRACE:
		p.b.StartNodeAt(GkStructLit, lhs)
		p.emit()
		for !p.at(token.RBRACE) && !p.at(token.EOF) {
			if p.at(token.COMMA) || p.at(token.NEWLINE) {
				p.emit()
				continue
			}
			start := p.pos
			p.b.StartNode(GkStructLitField)
			if p.at(token.DOTDOT) || p.at(token.DOTDOTEQ) {
				p.emit()
				p.parseExprUntil(token.COMMA, token.RBRACE)
			} else {
				p.eat(token.IDENT)
				if p.eat(token.COLON) {
					p.parseExprUntil(token.COMMA, token.RBRACE)
				}
			}
			p.recoverIfStalled(start)
			p.b.FinishNode()
		}
		p.eat(token.RBRACE)
		p.b.FinishNode()
		return true
	}
	return false
}

func (p *greenParser) parseParenOrTuple() {
	kind := GkParen
	if p.looksLikeTupleExpr() {
		kind = GkTuple
	}
	p.b.StartNode(kind)
	p.emit()
	for !p.at(token.RPAREN) && !p.at(token.EOF) {
		if p.at(token.COMMA) || p.at(token.NEWLINE) {
			p.emit()
			continue
		}
		p.parseExprUntil(token.COMMA, token.RPAREN)
	}
	p.eat(token.RPAREN)
	p.b.FinishNode()
}

func (p *greenParser) parseList() {
	p.b.StartNode(GkList)
	p.emit()
	for !p.at(token.RBRACKET) && !p.at(token.EOF) {
		if p.at(token.COMMA) || p.at(token.NEWLINE) {
			p.emit()
			continue
		}
		p.parseExprUntil(token.COMMA, token.RBRACKET)
	}
	p.eat(token.RBRACKET)
	p.b.FinishNode()
}

func (p *greenParser) parseMap() {
	p.b.StartNode(GkMap)
	p.eat(token.LBRACE)
	if p.eat(token.COLON) {
		for p.at(token.NEWLINE) {
			p.emit()
		}
		p.eat(token.RBRACE)
		p.b.FinishNode()
		return
	}
	for !p.at(token.RBRACE) && !p.at(token.EOF) {
		if p.at(token.COMMA) || p.at(token.NEWLINE) {
			p.emit()
			continue
		}
		start := p.pos
		p.b.StartNode(GkMapEntry)
		p.parseExprUntil(token.COLON, token.COMMA, token.RBRACE)
		if p.eat(token.COLON) {
			p.parseExprUntil(token.COMMA, token.RBRACE)
		}
		p.recoverIfStalled(start)
		p.b.FinishNode()
	}
	p.eat(token.RBRACE)
	p.b.FinishNode()
}

func (p *greenParser) parseIf() {
	kind := GkIf
	if p.kindAt(p.pos+1) == token.LET {
		kind = GkIfLet
	}
	p.b.StartNode(kind)
	p.emit()
	if p.eat(token.LET) {
		p.parsePatternUntil(token.ASSIGN, token.LBRACE, token.NEWLINE, token.EOF)
		p.eat(token.ASSIGN)
	}
	p.parseExprsUntil(token.LBRACE, token.NEWLINE, token.EOF)
	if p.at(token.LBRACE) {
		p.parseBlock()
	}
	if p.at(token.ELSE) {
		p.b.StartNode(GkElse)
		p.emit()
		if p.at(token.IF) {
			p.parseIf()
		} else if p.at(token.LBRACE) {
			p.parseBlock()
		}
		p.b.FinishNode()
	}
	p.b.FinishNode()
}

func (p *greenParser) parseMatch() {
	p.b.StartNode(GkMatch)
	p.emit()
	p.parseExprsUntil(token.LBRACE, token.NEWLINE, token.EOF)
	if p.eat(token.LBRACE) {
		p.b.StartNode(GkMatchArmList)
		for !p.at(token.RBRACE) && !p.at(token.EOF) {
			if p.at(token.COMMA) || p.at(token.NEWLINE) {
				p.emit()
				continue
			}
			p.b.StartNode(GkMatchArm)
			p.parsePatternUntil(token.IF, token.ARROW, token.RBRACE)
			if p.eat(token.IF) {
				p.parseExprsUntil(token.ARROW, token.RBRACE)
			}
			p.eat(token.ARROW)
			p.parseExprsUntil(token.COMMA, token.NEWLINE, token.RBRACE)
			p.b.FinishNode()
		}
		p.b.FinishNode()
		p.eat(token.RBRACE)
	}
	p.b.FinishNode()
}

func (p *greenParser) parseLoop() {
	p.b.StartNode(GkLoop)
	if p.at(token.LABEL) && p.kindAt(p.pos+1) == token.COLON {
		p.emit()
		p.emit()
	}
	if p.atIdentValue("loop") {
		p.emit()
	}
	if p.at(token.LBRACE) {
		p.parseBlock()
	}
	p.b.FinishNode()
}

func (p *greenParser) parseClosure(stops ...token.Kind) {
	p.b.StartNode(GkClosure)
	if p.at(token.OR) {
		p.b.StartNode(GkParamList)
		p.emit()
		p.b.FinishNode()
		p.parseClosureBody(stops...)
		p.b.FinishNode()
		return
	}
	if p.at(token.BITOR) {
		p.parseClosureParamList()
	}
	if p.eat(token.ARROW) {
		p.parseTypeUntil(token.LBRACE, token.NEWLINE, token.EOF)
	}
	p.parseClosureBody(stops...)
	p.b.FinishNode()
}

func (p *greenParser) parseClosureParamList() {
	p.b.StartNode(GkParamList)
	p.eat(token.BITOR)
	for !p.at(token.BITOR) && !p.at(token.EOF) {
		if p.at(token.COMMA) || p.at(token.NEWLINE) {
			p.emit()
			continue
		}
		start := p.pos
		p.b.StartNode(GkParam)
		p.parsePatternUntil(token.COLON, token.COMMA, token.BITOR)
		if p.eat(token.COLON) {
			p.parseTypeUntil(token.COMMA, token.BITOR)
		}
		p.recoverIfStalled(start)
		p.b.FinishNode()
	}
	p.eat(token.BITOR)
	p.b.FinishNode()
}

func (p *greenParser) parseClosureBody(stops ...token.Kind) {
	if p.at(token.LBRACE) {
		p.parseBlock()
		return
	}
	bodyStops := append([]token.Kind{}, stops...)
	bodyStops = append(bodyStops, token.NEWLINE, token.RBRACE, token.EOF)
	p.parseExprUntil(bodyStops...)
}

func (p *greenParser) parseLeaf(kind GreenKind) {
	p.b.StartNode(kind)
	p.emit()
	p.b.FinishNode()
}

func (p *greenParser) parseBalancedNode(kind GreenKind, open, close token.Kind) {
	p.b.StartNode(kind)
	depth := 0
	for !p.at(token.EOF) {
		if p.at(open) {
			depth++
		}
		if p.at(close) {
			p.emit()
			depth--
			if depth <= 0 {
				break
			}
			continue
		}
		p.emit()
	}
	p.b.FinishNode()
}

func infixBindingPower(kind token.Kind) (int, int, bool) {
	switch kind {
	case token.QQ:
		return 2, 2, true
	case token.OR:
		return 3, 4, true
	case token.AND:
		return 4, 5, true
	case token.EQ, token.NEQ, token.LT, token.GT, token.LEQ, token.GEQ:
		return 5, 6, true
	case token.DOTDOT, token.DOTDOTEQ:
		return 6, 7, true
	case token.BITOR:
		return 7, 8, true
	case token.BITXOR:
		return 8, 9, true
	case token.BITAND:
		return 9, 10, true
	case token.SHL, token.SHR:
		return 10, 11, true
	case token.PLUS, token.MINUS:
		return 11, 12, true
	case token.STAR, token.SLASH, token.PERCENT:
		return 12, 13, true
	case token.ASSIGN, token.PLUSEQ, token.MINUSEQ, token.STAREQ, token.SLASHEQ, token.PERCENTEQ,
		token.BITANDEQ, token.BITOREQ, token.BITXOREQ, token.SHLEQ, token.SHREQ, token.CHANARROW:
		return 1, 1, true
	}
	return 0, 0, false
}

func (p *greenParser) infixKind() GreenKind {
	switch p.peek().Kind {
	case token.DOTDOT:
		return GkRangeExcl
	case token.DOTDOTEQ:
		return GkRangeIncl
	case token.ASSIGN, token.PLUSEQ, token.MINUSEQ, token.STAREQ, token.SLASHEQ, token.PERCENTEQ,
		token.BITANDEQ, token.BITOREQ, token.BITXOREQ, token.SHLEQ, token.SHREQ:
		return GkAssignStmt
	case token.CHANARROW:
		return GkChanSendStmt
	}
	return GkBinary
}

func (p *greenParser) looksLikeMapLit() bool {
	if !p.at(token.LBRACE) {
		return false
	}
	i := p.pos + 1
	for p.kindAt(i) == token.NEWLINE {
		i++
	}
	if p.kindAt(i) == token.COLON {
		i++
		for p.kindAt(i) == token.NEWLINE {
			i++
		}
		return p.kindAt(i) == token.RBRACE
	}
	if p.kindAt(i) == token.RBRACE || tokenStartsBlockStmt(p.kindAt(i)) {
		return false
	}
	parenDepth, bracketDepth, braceDepth := 0, 0, 0
	for ; i < len(p.toks); i++ {
		switch p.kindAt(i) {
		case token.EOF:
			return false
		case token.NEWLINE:
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 {
				return false
			}
		case token.LPAREN:
			parenDepth++
		case token.RPAREN:
			if parenDepth > 0 {
				parenDepth--
			}
		case token.LBRACKET:
			bracketDepth++
		case token.RBRACKET:
			if bracketDepth > 0 {
				bracketDepth--
			}
		case token.LBRACE:
			braceDepth++
		case token.RBRACE:
			if braceDepth == 0 {
				return false
			}
			braceDepth--
		case token.COLON:
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 {
				return true
			}
		}
	}
	return false
}

func tokenStartsBlockStmt(kind token.Kind) bool {
	switch kind {
	case token.LET, token.RETURN, token.BREAK, token.CONTINUE, token.DEFER, token.FOR, token.PUB, token.LABEL:
		return true
	}
	return false
}

func tokenKindIn(kinds []token.Kind, target token.Kind) bool {
	for _, kind := range kinds {
		if kind == target {
			return true
		}
	}
	return false
}

func (p *greenParser) looksLikeTupleExpr() bool {
	if !p.at(token.LPAREN) {
		return false
	}
	i := p.pos + 1
	for p.kindAt(i) == token.NEWLINE {
		i++
	}
	if p.kindAt(i) == token.RPAREN {
		return true
	}
	parenDepth, bracketDepth, braceDepth := 0, 0, 0
	for ; i < len(p.toks); i++ {
		switch p.kindAt(i) {
		case token.EOF:
			return false
		case token.LPAREN:
			parenDepth++
		case token.RPAREN:
			if parenDepth == 0 {
				return false
			}
			parenDepth--
		case token.LBRACKET:
			bracketDepth++
		case token.RBRACKET:
			if bracketDepth > 0 {
				bracketDepth--
			}
		case token.LBRACE:
			braceDepth++
		case token.RBRACE:
			if braceDepth > 0 {
				braceDepth--
			}
		case token.COMMA:
			if parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 {
				return true
			}
		}
	}
	return false
}

func (p *greenParser) looksLikeUnitType() bool {
	if !p.at(token.LPAREN) {
		return false
	}
	i := p.pos + 1
	for p.kindAt(i) == token.NEWLINE {
		i++
	}
	return p.kindAt(i) == token.RPAREN
}

func (p *greenParser) atLoopExprStart() bool {
	if p.atIdentValue("loop") && p.kindAt(p.pos+1) == token.LBRACE {
		return true
	}
	return p.at(token.LABEL) &&
		p.kindAt(p.pos+1) == token.COLON &&
		p.kindAt(p.pos+2) == token.IDENT &&
		p.tokenValueAt(p.pos+2) == "loop" &&
		p.kindAt(p.pos+3) == token.LBRACE
}

func (p *greenParser) forHeaderHasInBeforeBody() bool {
	parenDepth, bracketDepth := 0, 0
	for i := p.pos; i < len(p.toks); i++ {
		switch p.kindAt(i) {
		case token.EOF, token.NEWLINE:
			return false
		case token.LBRACE:
			return false
		case token.LPAREN:
			parenDepth++
		case token.RPAREN:
			if parenDepth > 0 {
				parenDepth--
			}
		case token.LBRACKET:
			bracketDepth++
		case token.RBRACKET:
			if bracketDepth > 0 {
				bracketDepth--
			}
		case token.IDENT:
			if parenDepth == 0 && bracketDepth == 0 && p.tokenValueAt(i) == "in" {
				return true
			}
		}
	}
	return false
}

func (p *greenParser) useLooksGrouped(start int) bool {
	sawColonColon := false
	for i := start; i < len(p.toks); i++ {
		switch p.kindAt(i) {
		case token.NEWLINE, token.EOF:
			return false
		case token.COLONCOLON:
			sawColonColon = true
		case token.LBRACE:
			return sawColonColon
		}
	}
	return false
}

func (p *greenParser) kindAfterPub() token.Kind {
	if p.at(token.PUB) {
		return p.kindAt(p.pos + 1)
	}
	return p.peek().Kind
}

func (p *greenParser) at(kind token.Kind) bool {
	return p.peek().Kind == kind
}

func (p *greenParser) atAny(kinds ...token.Kind) bool {
	cur := p.peek().Kind
	for _, kind := range kinds {
		if cur == kind {
			return true
		}
	}
	return false
}

func (p *greenParser) kindAt(idx int) token.Kind {
	if idx < 0 || idx >= len(p.toks) {
		return token.EOF
	}
	return p.toks[idx].Kind
}

func (p *greenParser) atIdentValue(value string) bool {
	return p.at(token.IDENT) && p.peek().Value == value
}

func (p *greenParser) atUseAliasKeyword() bool {
	return p.atIdentValue("as")
}

func (p *greenParser) tokenValueAt(idx int) string {
	if idx < 0 || idx >= len(p.toks) {
		return ""
	}
	return p.toks[idx].Value
}

func (p *greenParser) previousTokenValue() string {
	if p.pos <= 0 || p.pos-1 >= len(p.toks) {
		return ""
	}
	return p.toks[p.pos-1].Value
}

func (p *greenParser) peek() token.Token {
	if p.pos < 0 || p.pos >= len(p.toks) {
		return token.Token{Kind: token.EOF}
	}
	return p.toks[p.pos]
}

func (p *greenParser) eat(kind token.Kind) bool {
	if !p.at(kind) {
		return false
	}
	p.emit()
	return true
}

func (p *greenParser) recoverIfStalled(start int) {
	if p.pos != start || p.at(token.EOF) {
		return
	}
	p.b.StartNode(GkErrorExtra)
	p.emit()
	p.b.FinishNode()
}

func (p *greenParser) emit() {
	if p.pos < 0 || p.pos >= len(p.toks) {
		return
	}
	tk := p.toks[p.pos]
	if tk.Kind == token.EOF {
		return
	}
	emitToken(p.b, tk, p.src, p.leading[p.pos], p.trailing[p.pos], p.triviaIDs)
	p.pos++
}
