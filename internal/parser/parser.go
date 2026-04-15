// Package parser parses an Osty token stream into an AST.
//
// The parser is a hand-written recursive-descent with a Pratt-style operator
// precedence loop for expressions. Error recovery is best-effort: on an
// unexpected token inside a declaration the parser syncs on the next
// top-level keyword so one malformed construct does not cascade into dozens
// of spurious errors.
package parser

import (
	"fmt"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/lexer"
	"github.com/osty/osty/internal/token"
)

// Error is retained as an alias for back-compat. New code should use
// diag.Diagnostic directly.
type Error = diag.Diagnostic

// Parser consumes a pre-lexed token stream.
type Parser struct {
	toks []token.Token
	pos  int
	errs []*diag.Diagnostic

	// noStructLit is set while parsing the head of `if`, `for`, and
	// `match` — contexts where a bare `Ident { ... }` is the start of the
	// body block rather than a struct literal. The spec (§4.2) requires
	// such literals to be parenthesized in these positions.
	noStructLit bool

	// suppressedAt tracks byte offsets where an error has already been
	// reported, so cascading "expected X, got Y" messages produced by
	// recovery don't pile up at the same source location. Using the raw
	// offset (rather than a full Pos) is sufficient — every distinct
	// location has a unique offset — and avoids comparing three fields
	// for every emission.
	suppressedAt map[int]bool
}

// Parse lexes src and returns the parsed File along with collected errors.
func Parse(src []byte) (*ast.File, []error) {
	file, diags := ParseDiagnostics(src)
	errs := make([]error, len(diags))
	for i, d := range diags {
		errs[i] = d
	}
	return file, errs
}

// ParseDiagnostics is the structured form of Parse. It returns the rich
// diagnostic objects so callers (CLI, LSP, tests) can render them with
// caret snippets, hints, and codes.
func ParseDiagnostics(src []byte) (*ast.File, []*diag.Diagnostic) {
	l := lexer.New(src)
	toks := l.Lex()
	file, diags := ParseTokens(toks)
	// Prepend lex errors so they show up before parse errors in order.
	return file, append(l.Errors(), diags...)
}

// ParseTokens parses a pre-lexed token stream. Useful for callers that
// need parallel access to the lexer's output beyond just tokens — the
// formatter, for instance, wants lexer.Comments() on the same Lexer
// instance, which isn't reachable through ParseDiagnostics.
func ParseTokens(toks []token.Token) (*ast.File, []*diag.Diagnostic) {
	p := newParser(toks)
	file := p.parseFile()
	return file, p.errs
}

// newParser allocates a parser initialized with the given token stream
// and an empty suppression map. Centralizing initialization avoids
// subtle bugs in sub-parsers (e.g. string-interpolation re-parses)
// forgetting to initialize one of the map fields.
func newParser(toks []token.Token) *Parser {
	return &Parser{toks: toks, suppressedAt: map[int]bool{}}
}

// ---- Basic token helpers ----

func (p *Parser) peek() token.Token {
	if p.pos >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[p.pos]
}

func (p *Parser) peekAt(n int) token.Token {
	if p.pos+n >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[p.pos+n]
}

func (p *Parser) advance() token.Token {
	t := p.peek()
	if t.Kind != token.EOF {
		p.pos++
	}
	return t
}

// lastEnd returns the End position of the most recently consumed token.
// Used to build accurate node spans — the AST's EndV should point to the
// end of the last token that belongs to the node, not the start of the
// following (unconsumed) token.
func (p *Parser) lastEnd() token.Pos {
	if p.pos == 0 {
		return p.toks[0].Pos
	}
	// Walk back over any trailing NEWLINE tokens; the node's "end" should
	// be the end of the last significant token.
	i := p.pos - 1
	for i > 0 && p.toks[i].Kind == token.NEWLINE {
		i--
	}
	return p.toks[i].End
}

// skipNewlines swallows any consecutive NEWLINE tokens.
func (p *Parser) skipNewlines() {
	for p.peek().Kind == token.NEWLINE {
		p.advance()
	}
}

// peekPastNewlines returns the next token that is not a NEWLINE without
// consuming any tokens. Used for limited look-ahead when a syntactic form
// (e.g. `} else`) may straddle an implicit statement terminator.
func (p *Parser) peekPastNewlines() token.Token {
	for i := p.pos; i < len(p.toks); i++ {
		if p.toks[i].Kind != token.NEWLINE {
			return p.toks[i]
		}
	}
	return p.toks[len(p.toks)-1]
}

// peekPastAnnotations returns the kind of the first token that is neither
// an annotation (`#[...]`) nor a NEWLINE. Used to decide how to handle
// annotations that precede non-declaration constructs such as `use`.
func (p *Parser) peekPastAnnotations() token.Kind {
	i := p.pos
	for i < len(p.toks) && p.toks[i].Kind == token.HASH {
		i++
		if i >= len(p.toks) || p.toks[i].Kind != token.LBRACKET {
			return p.toks[i].Kind
		}
		i++
		depth := 1
		for i < len(p.toks) && depth > 0 {
			switch p.toks[i].Kind {
			case token.LBRACKET:
				depth++
			case token.RBRACKET:
				depth--
			case token.EOF:
				return token.EOF
			}
			i++
		}
		for i < len(p.toks) && p.toks[i].Kind == token.NEWLINE {
			i++
		}
	}
	if i >= len(p.toks) {
		return token.EOF
	}
	return p.toks[i].Kind
}

func (p *Parser) at(k token.Kind) bool { return p.peek().Kind == k }

func (p *Parser) eat(k token.Kind) bool {
	if p.at(k) {
		p.advance()
		return true
	}
	return false
}

// expect consumes the next token if it matches k, otherwise reports an
// error and returns the current token (unconsumed) for caller context.
func (p *Parser) expect(k token.Kind) token.Token {
	if p.at(k) {
		return p.advance()
	}
	t := p.peek()
	p.errorf(t.Pos, "expected %s, got %s", k, t.Kind)
	return t
}

// consumeFieldName parses the identifier after `.` or `?.`. On
// mismatch we consume an INT or FLOAT (their printed form `.Value`
// re-lexes as DOT plus the same kind, so fmt(fmt(x)) is stable) and
// leave every other kind in place — both to avoid eating structural
// tokens and to avoid emitting a field name that would re-lex into
// multiple tokens (e.g. `obj."a b"` → `obj.a b` splits into two
// idents on the second pass).
func (p *Parser) consumeFieldName() string {
	if p.at(token.IDENT) {
		return p.advance().Value
	}
	t := p.peek()
	p.errorf(t.Pos, "expected field name after `.`, got %s", t.Kind)
	switch t.Kind {
	case token.INT, token.FLOAT:
		p.advance()
		return t.Value
	}
	return ""
}

// errorf reports a basic error with a position and printf-style message.
// Cascading errors at the same position are suppressed so a single mistake
// doesn't generate a column of duplicates.
func (p *Parser) errorf(pos token.Pos, format string, args ...any) {
	if p.suppressedAt[pos.Offset] {
		return
	}
	p.suppressedAt[pos.Offset] = true
	p.errs = append(p.errs, diag.New(diag.Error, fmt.Sprintf(format, args...)).
		PrimaryPos(pos, "").
		Build())
}

// emit records a fully-built diagnostic. Use this when constructing
// hint-bearing diagnostics with diag.New(...).Code(...).Build().
func (p *Parser) emit(d *diag.Diagnostic) {
	pos := d.PrimaryPos()
	if p.suppressedAt[pos.Offset] {
		return
	}
	p.suppressedAt[pos.Offset] = true
	p.errs = append(p.errs, d)
}

// errorAt is the span-aware counterpart of errorf. The span is the
// extent the caret will underline.
func (p *Parser) errorAt(start, end token.Pos, format string, args ...any) {
	if p.suppressedAt[start.Offset] {
		return
	}
	p.suppressedAt[start.Offset] = true
	p.errs = append(p.errs, diag.New(diag.Error, fmt.Sprintf(format, args...)).
		Primary(diag.Span{Start: start, End: end}, "").
		Build())
}

// syncDecl advances past tokens until we reach something that plausibly
// starts a new top-level declaration. Called after a fatal parse error in
// a declaration so subsequent declarations still parse cleanly.
//
// Sync points:
//   - `pub`, `fn`, `struct`, `enum`, `interface`, `type`, `let`, `use`
//     at the start of a line (i.e. immediately after a NEWLINE).
//   - EOF.
func (p *Parser) syncDecl() {
	for !p.at(token.EOF) {
		if p.atDeclStart() {
			return
		}
		p.advance()
	}
}

// atDeclStart reports whether the current token begins a top-level
// declaration or annotation. It is conservative — a keyword inside an
// expression context will still be classified as a decl start, which is
// acceptable because syncDecl is only called after an error has already
// been reported.
func (p *Parser) atDeclStart() bool {
	switch p.peek().Kind {
	case token.HASH, token.PUB, token.FN, token.STRUCT, token.ENUM,
		token.INTERFACE, token.TYPE, token.USE, token.LET:
		return true
	}
	return false
}

// syncStmt advances past tokens until a likely statement boundary inside a
// block: NEWLINE, `}`, or a known statement-start keyword. Used after a
// parse error inside a block so following statements still parse.
func (p *Parser) syncStmt() {
	for !p.at(token.EOF) {
		switch p.peek().Kind {
		case token.NEWLINE:
			p.advance()
			return
		case token.RBRACE:
			return
		case token.LET, token.RETURN, token.BREAK, token.CONTINUE,
			token.DEFER, token.FOR, token.IF, token.MATCH:
			return
		}
		p.advance()
	}
}

// consumeNameLike consumes the next token if it is an identifier or any
// keyword, returning the source text. Used in contexts (annotation arg
// names, member access via `.`) where keyword text appears as a name.
func (p *Parser) consumeNameLike() (string, bool) {
	t := p.peek()
	if t.Kind == token.IDENT {
		p.advance()
		return t.Value, true
	}
	if _, isKw := token.Keywords[t.Value]; isKw && t.Value != "" {
		p.advance()
		return t.Value, true
	}
	return "", false
}

// expectTypeGT consumes a `>` token that closes a generic argument list.
// When the lexer has produced `>>` (from a nested `Map<..., List<..>>`) or
// `>=` / `>>=`, we split the leading `>` off and rewrite the remaining token
// in place. This is the standard workaround for angle-bracket generics.
func (p *Parser) expectTypeGT() {
	switch p.peek().Kind {
	case token.GT:
		p.advance()
	case token.SHR:
		// Split `>>` -> `>` `>`: consume one, leave one.
		t := p.peek()
		t.Kind = token.GT
		t.Pos.Offset++
		t.Pos.Column++
		p.toks[p.pos] = t
	case token.GEQ:
		// Split `>=` -> `>` `=`.
		t := p.peek()
		t.Kind = token.ASSIGN
		t.Pos.Offset++
		t.Pos.Column++
		p.toks[p.pos] = t
	case token.SHREQ:
		// Split `>>=` -> `>` `>=`.
		t := p.peek()
		t.Kind = token.GEQ
		t.Pos.Offset++
		t.Pos.Column++
		p.toks[p.pos] = t
	default:
		p.expect(token.GT)
	}
}

// ---- File / top level ----

func (p *Parser) parseFile() *ast.File {
	f := &ast.File{PosV: p.peek().Pos}
	p.skipNewlines()

	// A file is a "script" if we encounter a top-level statement that is
	// not a declaration. We classify while parsing and accumulate into
	// either Decls or Stmts depending on what we see. `use` declarations
	// are TopLevelDecls (v0.2 §2.2) and may appear anywhere among the
	// other top-level decls.
	for !p.at(token.EOF) {
		errsBefore := len(p.errs)
		switch {
		case p.at(token.HASH) && p.peekPastAnnotations() == token.USE:
			// v0.3 §18.1: annotations are not permitted on `use`
			// statements. Emit a targeted diagnostic, then consume the
			// stray annotations so the use declaration still parses.
			first := p.peek()
			for p.at(token.HASH) {
				_ = p.parseAnnotations()
			}
			p.emit(diag.New(diag.Error,
				"annotations are not allowed on `use` statements").
				Code(diag.CodeAnnotationBadTarget).
				PrimaryPos(first.Pos, "annotation on a `use`").
				Note("v0.3 §18.1: annotations may appear on top-level declarations (fn/struct/enum/interface/type/let), struct fields, variants, and methods — but not on imports").
				Build())
			f.Uses = append(f.Uses, p.parseUseDecl())
		case p.at(token.USE):
			f.Uses = append(f.Uses, p.parseUseDecl())
		case p.isDeclStart():
			d := p.parseDecl()
			if d != nil {
				f.Decls = append(f.Decls, d)
			}
		default:
			s := p.parseStmt()
			if s != nil {
				f.Stmts = append(f.Stmts, s)
			}
		}
		// If this pass produced errors, sync forward to the next plausible
		// declaration so a single malformed construct does not cascade.
		if len(p.errs) > errsBefore && !p.atDeclStart() && !p.at(token.USE) {
			p.syncDecl()
		}
		p.skipNewlines()
	}
	f.EndV = p.lastEnd()
	return f
}

// isDeclStart returns whether the current token begins a top-level
// declaration rather than a free-standing statement. It looks past any
// leading `#[...]` annotations to find the actual decl head.
func (p *Parser) isDeclStart() bool {
	i := p.pos
	annotated := false
	for i < len(p.toks) && p.toks[i].Kind == token.HASH {
		annotated = true
		i++ // past #
		if i >= len(p.toks) || p.toks[i].Kind != token.LBRACKET {
			return false
		}
		i++ // past [
		depth := 1
		for i < len(p.toks) && depth > 0 {
			switch p.toks[i].Kind {
			case token.LBRACKET:
				depth++
			case token.RBRACKET:
				depth--
			case token.EOF:
				return false
			}
			i++
		}
		for i < len(p.toks) && p.toks[i].Kind == token.NEWLINE {
			i++
		}
	}
	if i >= len(p.toks) {
		return false
	}
	k := p.toks[i].Kind
	hasPub := k == token.PUB
	if hasPub && i+1 < len(p.toks) {
		k = p.toks[i+1].Kind
	}
	switch k {
	case token.FN, token.STRUCT, token.ENUM, token.INTERFACE, token.TYPE:
		return true
	case token.LET:
		// `pub let ...` and any annotated `let ...` are declarations.
		// Plain `let ...` (no pub, no annotation) is a statement so it
		// can appear at the top of a script file.
		return hasPub || annotated
	}
	return false
}

// ---- Use declarations ----

func (p *Parser) parseUseDecl() *ast.UseDecl {
	u := &ast.UseDecl{PosV: p.peek().Pos}
	p.expect(token.USE)

	// Two forms:
	//   use std.fs                  -> dotted path
	//   use github.com/user/lib     -> slashed URL-like path
	//   use go "net/http" { ... }   -> FFI import
	if p.at(token.IDENT) && p.peek().Value == "go" && p.peekAt(1).Kind == token.STRING {
		p.advance() // go
		str := p.advance()
		u.IsGoFFI = true
		u.GoPath = stringLitText(str)
		// Optional `as alias` before the FFI body (§12.1).
		if p.at(token.IDENT) && p.peek().Value == "as" {
			p.advance()
			u.Alias = p.expect(token.IDENT).Value
		}
		// Optional FFI body. v0.2 R16/R17: only `fn` (no body) and `struct`
		// (no methods, no defaults, no generics) are permitted.
		if p.eat(token.LBRACE) {
			p.skipNewlines()
			for !p.at(token.RBRACE) && !p.at(token.EOF) {
				switch p.peek().Kind {
				case token.FN:
					fd := p.parseFnDecl()
					p.validateFFIFnSignature(fd, "function")
					u.GoBody = append(u.GoBody, fd)
				case token.STRUCT:
					sd := p.parseStructDecl()
					for _, m := range sd.Methods {
						p.validateFFIFnSignature(m, "struct method")
					}
					if len(sd.Generics) > 0 {
						p.emit(diag.New(diag.Error,
							"`use go` struct declarations may not have generic parameters").
							Code(diag.CodeUseGoUnsupported).
							PrimaryPos(sd.PosV, "").
							Build())
					}
					for _, fld := range sd.Fields {
						if fld.Default != nil {
							p.emit(diag.New(diag.Error,
								"`use go` struct fields may not have defaults").
								Code(diag.CodeUseGoUnsupported).
								PrimaryPos(fld.PosV, "").
								Build())
						}
					}
					u.GoBody = append(u.GoBody, sd)
				default:
					p.errorf(p.peek().Pos,
						"`use go` body may only contain `fn` and `struct` declarations, got %s",
						p.peek().Kind)
					p.advance()
				}
				p.skipNewlines()
			}
			p.expect(token.RBRACE)
		}
		u.EndV = p.lastEnd()
		return u
	}

	// v0.2 R15: a UsePath is either DottedPath (`std.fs`,
	// `myapp.auth.login`) OR UrlishPath (`github.com/user/lib`,
	// `domain.tld/seg/seg`). The two forms cannot be mixed in one path:
	// once a `/` is seen, no further `.`-separator may appear; once a
	// `.`-separator is seen without an upcoming `/`, the path is dotted.
	var parts []string
	var raw strings.Builder
	first := p.expect(token.IDENT)
	parts = append(parts, first.Value)
	raw.WriteString(first.Value)
	mode := "" // "dotted" or "urlish" once a `/` appears.
	// Consume `.IDENT` (allowed both before mode is decided AND in the
	// domain prefix of urlish) or `/IDENT` (commits to urlish).
	for {
		switch {
		case mode == "" && p.at(token.DOT) && p.peekAt(1).Kind == token.IDENT:
			// Still in the domain/dotted prefix; `.IDENT` extends it.
			p.advance()
			raw.WriteByte('.')
			t := p.advance()
			parts = append(parts, t.Value)
			raw.WriteString(t.Value)
		case mode == "urlish" && p.at(token.DOT):
			// In urlish mode (after first `/`), path segments are atomic
			// IDENTs — no dots allowed.
			t := p.peek()
			p.emit(diag.New(diag.Error,
				"path segment after `/` may not contain `.`").
				Code(diag.CodeUsePathMixed).
				Primary(diag.Span{Start: t.Pos, End: t.End}, "`.` here").
				Note("v0.2 R15: a `use` path is either a dotted std/local path (`std.fs`) OR a urlish package path (`github.com/x/y`). They cannot be mixed").
				Hint("rewrite the path using only `.` (for std/local) or only `/` (for urlish packages)").
				Build())
			p.advance()
		case p.at(token.SLASH) && p.peekAt(1).Kind == token.IDENT:
			mode = "urlish"
			p.advance()
			raw.WriteByte('/')
			t := p.advance()
			parts = append(parts, t.Value)
			raw.WriteString(t.Value)
		default:
			goto done
		}
	}
done:
	// Once we know it's urlish, the first segment must contain a `.`
	// (domain-shaped) per R15. If it's purely dotted, we're done.
	if mode == "urlish" && !strings.Contains(parts[0], ".") {
		// First segment was a single IDENT, but parts[0] holds only the
		// IDENT before the first `/`. We need to check: did the path
		// reach a `/` after IDENT.IDENT.IDENT first? Walk parts to find
		// the first `/`-separated boundary in raw.
		// Simpler check: the raw text must have the form `<dotted>/<segs>`
		// where the part before the first `/` contains a `.`.
		domain := raw.String()
		if i := strings.Index(domain, "/"); i >= 0 {
			domain = domain[:i]
		}
		if !strings.Contains(domain, ".") {
			p.errorf(u.PosV,
				"urlish `use` path must begin with a dotted domain segment, got %q", domain)
		}
	}
	u.Path = parts
	u.RawPath = raw.String()

	// Optional `as alias`.
	if p.at(token.IDENT) && p.peek().Value == "as" {
		p.advance()
		alias := p.expect(token.IDENT)
		u.Alias = alias.Value
	}
	u.EndV = p.lastEnd()
	return u
}

// validateFFIFnSignature rejects spec-forbidden shapes on a `use go`
// fn or struct-method declaration: bodies (§12.1), generics,
// parameter defaults, and function- or channel-typed param/return
// slots (§12.5/§12.7). `kind` varies the diagnostic noun
// ("function" vs "struct method").
func (p *Parser) validateFFIFnSignature(fd *ast.FnDecl, kind string) {
	if fd.Body != nil {
		p.emit(diag.New(diag.Error,
			"`use go` "+kind+" declarations may not have a body").
			Code(diag.CodeUseGoFnHasBody).
			Primary(diag.Span{Start: fd.Body.PosV, End: fd.Body.EndV}, "").
			Note("§12.1: FFI declarations forward to the Go function — there is no Osty body").
			Build())
		fd.Body = nil
	}
	if len(fd.Generics) > 0 {
		p.emit(diag.New(diag.Error,
			"`use go` "+kind+" declarations may not have generic parameters").
			Code(diag.CodeUseGoUnsupported).
			PrimaryPos(fd.PosV, "").
			Build())
	}
	for _, prm := range fd.Params {
		if prm.Default != nil {
			p.emit(diag.New(diag.Error,
				"`use go` "+kind+" parameters may not have defaults").
				Code(diag.CodeUseGoUnsupported).
				PrimaryPos(prm.PosV, "").
				Build())
		}
		if _, isFn := prm.Type.(*ast.FnType); isFn {
			p.emit(diag.New(diag.Error,
				"`use go` "+kind+" parameters may not be function-typed").
				Code(diag.CodeUseGoUnsupported).
				PrimaryPos(prm.PosV, "").
				Note("§12.7: closures cannot cross the FFI boundary — expose the behaviour as a named Go `fn` instead").
				Build())
		}
		if isChannelTypeAST(prm.Type) {
			p.emit(diag.New(diag.Error,
				"`use go` "+kind+" parameters may not be channel-typed").
				Code(diag.CodeUseGoUnsupported).
				PrimaryPos(prm.PosV, "").
				Note("§12.5/§12.7: Go channels obtained via FFI are not integrated with Osty's structured concurrency").
				Build())
		}
	}
	if _, isFn := fd.ReturnType.(*ast.FnType); isFn {
		p.emit(diag.New(diag.Error,
			"`use go` "+kind+" return types may not be function-typed").
			Code(diag.CodeUseGoUnsupported).
			PrimaryPos(fd.PosV, "").
			Note("§12.7: function values cannot cross the FFI boundary").
			Build())
	}
	if isChannelTypeAST(fd.ReturnType) {
		p.emit(diag.New(diag.Error,
			"`use go` "+kind+" return types may not be channel-typed").
			Code(diag.CodeUseGoUnsupported).
			PrimaryPos(fd.PosV, "").
			Note("§12.5/§12.7: Go channels obtained via FFI are not integrated with Osty's structured concurrency").
			Build())
	}
}

// isChannelTypeAST matches a top-level `Channel<…>` or `Chan<…>`.
// Nested occurrences are conservatively accepted because the Go side
// may legally hold them as opaque slice/map elements.
func isChannelTypeAST(t ast.Type) bool {
	n, ok := t.(*ast.NamedType)
	if !ok {
		return false
	}
	if len(n.Path) != 1 {
		return false
	}
	return n.Path[0] == "Channel" || n.Path[0] == "Chan"
}

// stringLitText returns the concatenation of all literal text parts, using
// the raw text of any interpolation segments (best-effort). FFI paths
// should never contain interpolations.

func stringLitText(t token.Token) string {
	var b strings.Builder
	for _, pt := range t.Parts {
		if pt.Kind == token.PartText {
			b.WriteString(pt.Text)
		}
	}
	return b.String()
}

// ---- Declarations ----

func (p *Parser) parseDecl() ast.Decl {
	// A leading `///` doc comment is attached to the pub/fn/struct/...
	// token by the lexer. Capture it before we consume tokens.
	doc := p.peek().LeadingDoc
	annots := p.parseAnnotations()
	// Doc comment on the very first annotation `#` token belongs to the
	// declaration as a whole.
	if doc == "" && len(annots) > 0 {
		// (A future improvement could carry doc on the annotations
		// themselves; for now we keep it on the decl.)
	}
	pub := p.eat(token.PUB)
	switch p.peek().Kind {
	case token.FN:
		fd := p.parseFnDecl()
		fd.Pub = pub
		fd.DocComment = doc
		fd.Annotations = annots
		return fd
	case token.STRUCT:
		sd := p.parseStructDecl()
		sd.Pub = pub
		sd.DocComment = doc
		sd.Annotations = annots
		return sd
	case token.ENUM:
		ed := p.parseEnumDecl()
		ed.Pub = pub
		ed.DocComment = doc
		ed.Annotations = annots
		return ed
	case token.INTERFACE:
		id := p.parseInterfaceDecl()
		id.Pub = pub
		id.DocComment = doc
		id.Annotations = annots
		return id
	case token.TYPE:
		td := p.parseTypeAliasDecl()
		td.Pub = pub
		td.DocComment = doc
		td.Annotations = annots
		return td
	case token.LET:
		ld := p.parseLetDecl()
		ld.Pub = pub
		ld.DocComment = doc
		ld.Annotations = annots
		return ld
	}
	p.errorf(p.peek().Pos, "expected declaration, got %s", p.peek().Kind)
	p.advance()
	return nil
}

// parseAnnotations consumes zero or more `#[...]` annotations and returns
// them. Each annotation's name is validated against the v0.2 R26 permitted
// set; unknown names are reported but the AST is still built so downstream
// passes can choose how to recover.
func (p *Parser) parseAnnotations() []*ast.Annotation {
	var out []*ast.Annotation
	for p.at(token.HASH) {
		hash := p.advance()
		p.expect(token.LBRACKET)
		nameTok := p.expect(token.IDENT)
		a := &ast.Annotation{PosV: hash.Pos, Name: nameTok.Value}
		if p.eat(token.LPAREN) {
			p.skipNewlines()
			for !p.at(token.RPAREN) && !p.at(token.EOF) {
				arg := &ast.AnnotationArg{PosV: p.peek().Pos}
				// Annotation arg names accept identifiers and bare
				// keywords. The v0.2 spec uses `use` as a deprecated()
				// arg name, which would otherwise collide with the
				// `use` keyword.
				key, ok := p.consumeNameLike()
				if !ok {
					p.errorf(p.peek().Pos, "expected annotation arg name, got %s", p.peek().Kind)
					p.advance()
					if !p.eat(token.COMMA) {
						break
					}
					continue
				}
				arg.Key = key
				if p.eat(token.ASSIGN) {
					arg.Value = p.parseDefaultExpr()
				}
				a.Args = append(a.Args, arg)
				if !p.eat(token.COMMA) {
					break
				}
				p.skipNewlines()
			}
			p.expect(token.RPAREN)
		}
		p.expect(token.RBRACKET)
		a.EndV = p.lastEnd()
		if !ast.IsAllowedAnnotation(a.Name) {
			p.emit(diag.New(diag.Error,
				fmt.Sprintf("unknown annotation `#[%s]`", a.Name)).
				Code(diag.CodeUnknownAnnotation).
				Primary(diag.Span{Start: a.PosV, End: a.EndV}, "unknown annotation").
				Note("v0.2 R26 fixes the v0.9 set: `#[json(...)]` for struct fields and `#[deprecated(...)]` for top-level decls and methods").
				Hint("if you need a different annotation, file a spec change — user-defined annotations are not allowed in v0.9").
				Build())
		}
		out = append(out, a)
		p.skipNewlines()
	}
	return out
}

func (p *Parser) parseFnDecl() *ast.FnDecl {
	f := &ast.FnDecl{PosV: p.peek().Pos}
	p.expect(token.FN)
	name := p.expect(token.IDENT)
	f.Name = name.Value
	if p.at(token.LT) {
		f.Generics = p.parseGenericParams()
	}
	p.expect(token.LPAREN)
	// Receiver: `self` or `mut self` as first parameter.
	if !p.at(token.RPAREN) {
		if p.at(token.IDENT) && p.peek().Value == "self" {
			f.Recv = &ast.Receiver{PosV: p.peek().Pos}
			p.advance()
			if p.eat(token.COMMA) {
				// continue with normal params
			}
		} else if p.at(token.MUT) && p.peekAt(1).Kind == token.IDENT && p.peekAt(1).Value == "self" {
			mutPos := p.peek().Pos
			f.Recv = &ast.Receiver{PosV: mutPos, Mut: true, MutPos: mutPos}
			p.advance()
			p.advance()
			if p.eat(token.COMMA) {
				// continue
			}
		}
		f.Params = p.parseParamList()
	}
	p.expect(token.RPAREN)
	if p.eat(token.ARROW) {
		f.ReturnType = p.parseType()
	}
	// Body is optional only in interface declarations; parse if present.
	if p.at(token.LBRACE) {
		f.Body = p.parseBlock()
	}
	f.EndV = p.lastEnd()
	return f
}

// parseGenericParams parses `<T, U: Ordered + Hashable, V>`.
func (p *Parser) parseGenericParams() []*ast.GenericParam {
	p.expect(token.LT)
	var out []*ast.GenericParam
	for !p.at(token.GT) && !p.at(token.SHR) && !p.at(token.GEQ) && !p.at(token.SHREQ) && !p.at(token.EOF) {
		g := &ast.GenericParam{PosV: p.peek().Pos}
		g.Name = p.expect(token.IDENT).Value
		if p.eat(token.COLON) {
			g.Constraints = append(g.Constraints, p.parseType())
			for p.eat(token.PLUS) {
				g.Constraints = append(g.Constraints, p.parseType())
			}
		}
		out = append(out, g)
		if !p.eat(token.COMMA) {
			break
		}
	}
	p.expectTypeGT()
	return out
}

// parseParamList parses a comma-separated list of parameters until `)`.
// Consumes nothing past the closing paren.
func (p *Parser) parseParamList() []*ast.Param {
	var params []*ast.Param
	p.skipNewlines()
	for !p.at(token.RPAREN) && !p.at(token.EOF) {
		prm := &ast.Param{PosV: p.peek().Pos}
		prm.Name = p.expect(token.IDENT).Value
		p.expect(token.COLON)
		prm.Type = p.parseType()
		if p.eat(token.ASSIGN) {
			prm.Default = p.parseDefaultExpr()
		}
		params = append(params, prm)
		p.skipNewlines()
		if !p.eat(token.COMMA) {
			break
		}
		p.skipNewlines()
	}
	return params
}

// parseDefaultExpr parses the restricted form allowed for parameter and
// field defaults (v0.2 R18):
//
//	Literal | '-' (INT_LIT | FLOAT_LIT) | 'None'
//	       | 'Ok' '(' Literal ')' | 'Err' '(' Literal ')'
//	       | '[' ']' | '{' ':' '}' | '(' ')'
//
// Anything else is parsed (to keep error recovery clean) but reported as
// an error.
func (p *Parser) parseDefaultExpr() ast.Expr {
	t := p.peek()
	pos := t.Pos
	switch t.Kind {
	case token.INT, token.FLOAT, token.STRING, token.RAWSTRING, token.CHAR, token.BYTE:
		return p.parsePrimary()
	case token.MINUS:
		// `-` followed by a numeric literal.
		if k := p.peekAt(1).Kind; k == token.INT || k == token.FLOAT {
			p.advance()
			inner := p.parsePrimary()
			return &ast.UnaryExpr{PosV: pos, EndV: inner.End(), Op: token.MINUS, X: inner}
		}
	case token.IDENT:
		switch t.Value {
		case "true", "false":
			return p.parsePrimary()
		case "None":
			p.advance()
			return &ast.Ident{PosV: pos, EndV: t.End, Name: "None"}
		case "Ok", "Err":
			name := p.advance().Value
			if !p.eat(token.LPAREN) {
				p.errorf(p.peek().Pos, "expected `(` after `%s` in default value", name)
				return &ast.Ident{PosV: pos, EndV: t.End, Name: name}
			}
			inner := p.parseDefaultExpr()
			p.expect(token.RPAREN)
			return &ast.CallExpr{
				PosV: pos, EndV: p.lastEnd(),
				Fn:   &ast.Ident{PosV: pos, EndV: t.End, Name: name},
				Args: []*ast.Arg{{PosV: pos, Value: inner}},
			}
		}
	case token.LBRACKET:
		// `[]` empty list literal.
		if p.peekAt(1).Kind == token.RBRACKET {
			p.advance()
			p.advance()
			return &ast.ListExpr{PosV: pos, EndV: p.lastEnd()}
		}
	case token.LBRACE:
		// `{:}` empty map literal.
		if p.peekAt(1).Kind == token.COLON && p.peekAt(2).Kind == token.RBRACE {
			p.advance()
			p.advance()
			p.advance()
			return &ast.MapExpr{PosV: pos, EndV: p.lastEnd(), Empty: true}
		}
	case token.LPAREN:
		// `()` unit.
		if p.peekAt(1).Kind == token.RPAREN {
			p.advance()
			p.advance()
			return &ast.TupleExpr{PosV: pos, EndV: p.lastEnd()}
		}
	}
	p.emit(diag.New(diag.Error,
		fmt.Sprintf("default value must be a literal, got %s", t.Kind)).
		Code(diag.CodeDefaultExprNotLiteral).
		Primary(diag.Span{Start: pos, End: t.End}, "not a literal").
		Note("v0.2 R18: parameter and field defaults are restricted to literals, `None`, `Ok(lit)`, `Err(lit)`, `[]`, `{:}`, or `()`").
		Hint("if you need a computed default, build the value at the call site instead").
		Build())
	// Best-effort recovery: parse a regular expression so subsequent
	// parameters still parse.
	return p.parseExpr()
}

func (p *Parser) parseStructDecl() *ast.StructDecl {
	s := &ast.StructDecl{PosV: p.peek().Pos}
	p.expect(token.STRUCT)
	s.Name = p.expect(token.IDENT).Value
	if p.at(token.LT) {
		s.Generics = p.parseGenericParams()
	}
	p.expect(token.LBRACE)
	p.skipNewlines()
	for !p.at(token.RBRACE) && !p.at(token.EOF) {
		// Capture LeadingDoc BEFORE consuming annotations: the lexer
		// attaches `///` blocks to the next significant token, which
		// for an annotated decl is the `#` of the annotation. Reading
		// it here ensures both bare and annotated declarations carry
		// their doc comment downstream.
		doc := p.peek().LeadingDoc
		annots := p.parseAnnotations()
		if p.isFieldStart() {
			f := p.parseField()
			f.Annotations = annots
			f.DocComment = doc
			s.Fields = append(s.Fields, f)
		} else if p.at(token.FN) || (p.at(token.PUB) && p.peekAt(1).Kind == token.FN) {
			pub := p.eat(token.PUB)
			m := p.parseFnDecl()
			m.Pub = pub
			m.DocComment = doc
			m.Annotations = annots
			s.Methods = append(s.Methods, m)
		} else {
			p.errorf(p.peek().Pos, "unexpected %s in struct body", p.peek().Kind)
			p.advance()
		}
		p.skipNewlines()
		// Accept optional `,` between fields.
		p.eat(token.COMMA)
		p.skipNewlines()
	}
	p.expect(token.RBRACE)
	s.EndV = p.lastEnd()
	return s
}

// isFieldStart returns whether the current token sequence begins a field.
// A field is `[pub] ident :`. We need 2-3 token lookahead.
func (p *Parser) isFieldStart() bool {
	k := p.peek().Kind
	if k == token.PUB {
		k = p.peekAt(1).Kind
		if k == token.IDENT && p.peekAt(2).Kind == token.COLON {
			return true
		}
		return false
	}
	if k == token.IDENT && p.peekAt(1).Kind == token.COLON {
		return true
	}
	return false
}

func (p *Parser) parseField() *ast.Field {
	f := &ast.Field{PosV: p.peek().Pos}
	f.Pub = p.eat(token.PUB)
	f.Name = p.expect(token.IDENT).Value
	p.expect(token.COLON)
	f.Type = p.parseType()
	if p.eat(token.ASSIGN) {
		// v0.2 R18 applies to field defaults the same as parameter defaults.
		f.Default = p.parseDefaultExpr()
	}
	return f
}

func (p *Parser) parseEnumDecl() *ast.EnumDecl {
	e := &ast.EnumDecl{PosV: p.peek().Pos}
	p.expect(token.ENUM)
	e.Name = p.expect(token.IDENT).Value
	if p.at(token.LT) {
		e.Generics = p.parseGenericParams()
	}
	p.expect(token.LBRACE)
	p.skipNewlines()
	// Variants come first; then methods (each must begin with fn/pub).
	for !p.at(token.RBRACE) && !p.at(token.EOF) {
		annots := p.parseAnnotations()
		if p.at(token.FN) || (p.at(token.PUB) && p.peekAt(1).Kind == token.FN) {
			doc := p.peek().LeadingDoc
			pub := p.eat(token.PUB)
			m := p.parseFnDecl()
			m.Pub = pub
			m.DocComment = doc
			m.Annotations = annots
			e.Methods = append(e.Methods, m)
		} else if p.at(token.IDENT) {
			doc := p.peek().LeadingDoc
			v := &ast.Variant{
				PosV:        p.peek().Pos,
				Name:        p.advance().Value,
				Annotations: annots,
				DocComment:  doc,
			}
			if p.eat(token.LPAREN) {
				for !p.at(token.RPAREN) && !p.at(token.EOF) {
					v.Fields = append(v.Fields, p.parseType())
					if !p.eat(token.COMMA) {
						break
					}
				}
				p.expect(token.RPAREN)
			}
			v.EndV = p.lastEnd()
			e.Variants = append(e.Variants, v)
		} else {
			p.errorf(p.peek().Pos, "unexpected %s in enum body", p.peek().Kind)
			p.advance()
		}
		p.skipNewlines()
		p.eat(token.COMMA)
		p.skipNewlines()
	}
	p.expect(token.RBRACE)
	e.EndV = p.lastEnd()
	return e
}

func (p *Parser) parseInterfaceDecl() *ast.InterfaceDecl {
	i := &ast.InterfaceDecl{PosV: p.peek().Pos}
	p.expect(token.INTERFACE)
	i.Name = p.expect(token.IDENT).Value
	if p.at(token.LT) {
		i.Generics = p.parseGenericParams()
	}
	p.expect(token.LBRACE)
	p.skipNewlines()
	for !p.at(token.RBRACE) && !p.at(token.EOF) {
		if p.at(token.FN) {
			doc := p.peek().LeadingDoc
			m := p.parseFnDecl()
			m.DocComment = doc
			i.Methods = append(i.Methods, m)
		} else if p.at(token.IDENT) {
			// Interface composition: bare type name.
			i.Extends = append(i.Extends, p.parseType())
		} else {
			p.errorf(p.peek().Pos, "unexpected %s in interface body", p.peek().Kind)
			p.advance()
		}
		p.skipNewlines()
		p.eat(token.COMMA)
		p.skipNewlines()
	}
	p.expect(token.RBRACE)
	i.EndV = p.lastEnd()
	return i
}

func (p *Parser) parseTypeAliasDecl() *ast.TypeAliasDecl {
	t := &ast.TypeAliasDecl{PosV: p.peek().Pos}
	p.expect(token.TYPE)
	t.Name = p.expect(token.IDENT).Value
	if p.at(token.LT) {
		t.Generics = p.parseGenericParams()
	}
	p.expect(token.ASSIGN)
	t.Target = p.parseType()
	t.EndV = p.lastEnd()
	return t
}

func (p *Parser) parseLetDecl() *ast.LetDecl {
	l := &ast.LetDecl{PosV: p.peek().Pos}
	p.expect(token.LET)
	if p.at(token.MUT) {
		l.MutPos = p.peek().Pos
	}
	l.Mut = p.eat(token.MUT)
	l.Name = p.expect(token.IDENT).Value
	if p.eat(token.COLON) {
		l.Type = p.parseType()
	}
	p.expect(token.ASSIGN)
	l.Value = p.parseExpr()
	l.EndV = p.lastEnd()
	return l
}

// ---- Types ----

func (p *Parser) parseType() ast.Type {
	t := p.parseTypeBase()
	// Postfix `?` on a type means Option<T>. v0.3 §2.4 allows any number
	// of trailing `?` (e.g. `Int??` is `Option<Option<Int>>`). The lexer
	// greedily matches `??` as the nil-coalescing token, so split it
	// here: consume one `?` now and rewrite the remaining `?` in place
	// so the loop picks it up next iteration.
	for {
		switch {
		case p.at(token.QUESTION):
			q := p.advance()
			t = &ast.OptionalType{PosV: t.Pos(), EndV: q.End, Inner: t}
		case p.at(token.QQ):
			qq := p.peek()
			t = &ast.OptionalType{PosV: t.Pos(), EndV: qq.Pos, Inner: t}
			next := qq
			next.Kind = token.QUESTION
			next.Pos.Offset++
			next.Pos.Column++
			p.toks[p.pos] = next
		default:
			return t
		}
	}
}

func (p *Parser) parseTypeBase() ast.Type {
	start := p.peek().Pos
	// Tuple or unit type: `(T1, T2, ...)` or `()`.
	if p.at(token.LPAREN) {
		p.advance()
		if p.eat(token.RPAREN) {
			return &ast.TupleType{PosV: start, EndV: p.lastEnd()}
		}
		var elems []ast.Type
		elems = append(elems, p.parseType())
		singleton := true
		for p.eat(token.COMMA) {
			if p.at(token.RPAREN) {
				break
			}
			elems = append(elems, p.parseType())
			singleton = false
		}
		p.expect(token.RPAREN)
		if singleton && len(elems) == 1 {
			return elems[0]
		}
		return &ast.TupleType{PosV: start, EndV: p.lastEnd(), Elems: elems}
	}
	// Function type: `fn(A, B) -> R`.
	if p.at(token.FN) {
		p.advance()
		p.expect(token.LPAREN)
		var params []ast.Type
		for !p.at(token.RPAREN) && !p.at(token.EOF) {
			params = append(params, p.parseType())
			if !p.eat(token.COMMA) {
				break
			}
		}
		p.expect(token.RPAREN)
		var ret ast.Type
		if p.eat(token.ARROW) {
			ret = p.parseType()
		}
		return &ast.FnType{PosV: start, EndV: p.lastEnd(), Params: params, ReturnType: ret}
	}
	// Named type with optional dotted path and generic args.
	if p.at(token.IDENT) {
		first := p.advance()
		path := []string{first.Value}
		for p.at(token.DOT) && p.peekAt(1).Kind == token.IDENT {
			p.advance()
			path = append(path, p.advance().Value)
		}
		var args []ast.Type
		if p.at(token.LT) {
			p.advance()
			for !p.at(token.GT) && !p.at(token.SHR) && !p.at(token.GEQ) && !p.at(token.SHREQ) && !p.at(token.EOF) {
				args = append(args, p.parseType())
				if !p.eat(token.COMMA) {
					break
				}
			}
			p.expectTypeGT()
		}
		return &ast.NamedType{PosV: start, EndV: p.lastEnd(), Path: path, Args: args}
	}
	p.errorf(p.peek().Pos, "expected type, got %s", p.peek().Kind)
	p.advance()
	return &ast.NamedType{PosV: start, EndV: p.lastEnd(), Path: []string{"<error>"}}
}

// ---- Statements ----

func (p *Parser) parseBlock() *ast.Block {
	b := &ast.Block{PosV: p.peek().Pos}
	p.expect(token.LBRACE)
	p.skipNewlines()
	for !p.at(token.RBRACE) && !p.at(token.EOF) {
		errsBefore := len(p.errs)
		s := p.parseStmt()
		if s != nil {
			b.Stmts = append(b.Stmts, s)
		}
		if len(p.errs) > errsBefore {
			p.syncStmt()
		}
		p.skipNewlines()
	}
	p.expect(token.RBRACE)
	b.EndV = p.lastEnd()
	return b
}

func (p *Parser) parseStmt() ast.Stmt {
	switch p.peek().Kind {
	case token.LET:
		return p.parseLetStmt()
	case token.RETURN:
		return p.parseReturnStmt()
	case token.BREAK:
		t := p.advance()
		return &ast.BreakStmt{PosV: t.Pos, EndV: t.End}
	case token.CONTINUE:
		t := p.advance()
		return &ast.ContinueStmt{PosV: t.Pos, EndV: t.End}
	case token.DEFER:
		return p.parseDeferStmt()
	case token.FOR:
		return p.parseForStmt()
	}
	// Expression statement, assignment, or channel send. Parse an
	// expression and check what follows.
	start := p.peek().Pos
	x := p.parseExpr()
	if isAssignOp(p.peek().Kind) {
		op := p.advance()
		rhs := p.parseExpr()
		var targets []ast.Expr
		if tup, ok := x.(*ast.TupleExpr); ok && op.Kind == token.ASSIGN {
			targets = tup.Elems
		} else {
			targets = []ast.Expr{x}
		}
		return &ast.AssignStmt{PosV: start, EndV: p.lastEnd(), Op: op.Kind, Targets: targets, Value: rhs}
	}
	if p.at(token.CHANARROW) {
		p.advance()
		rhs := p.parseExpr()
		return &ast.ChanSendStmt{PosV: start, EndV: p.lastEnd(), Channel: x, Value: rhs}
	}
	return &ast.ExprStmt{X: x}
}

func isAssignOp(k token.Kind) bool {
	switch k {
	case token.ASSIGN, token.PLUSEQ, token.MINUSEQ, token.STAREQ, token.SLASHEQ,
		token.PERCENTEQ, token.BITANDEQ, token.BITOREQ, token.BITXOREQ,
		token.SHLEQ, token.SHREQ:
		return true
	}
	return false
}

func (p *Parser) parseLetStmt() *ast.LetStmt {
	s := &ast.LetStmt{PosV: p.peek().Pos}
	p.expect(token.LET)
	if p.at(token.MUT) {
		s.MutPos = p.peek().Pos
	}
	s.Mut = p.eat(token.MUT)
	s.Pattern = p.parsePattern()
	if p.eat(token.COLON) {
		s.Type = p.parseType()
	}
	p.expect(token.ASSIGN)
	s.Value = p.parseExpr()
	s.EndV = p.lastEnd()
	return s
}

func (p *Parser) parseReturnStmt() *ast.ReturnStmt {
	t := p.advance() // return
	r := &ast.ReturnStmt{PosV: t.Pos, EndV: t.End}
	if !p.at(token.NEWLINE) && !p.at(token.RBRACE) && !p.at(token.EOF) {
		r.Value = p.parseExpr()
		r.EndV = p.lastEnd()
	}
	return r
}

func (p *Parser) parseDeferStmt() *ast.DeferStmt {
	t := p.advance() // defer
	d := &ast.DeferStmt{PosV: t.Pos}
	d.X = p.parseExpr()
	d.EndV = p.lastEnd()
	return d
}

func (p *Parser) parseForStmt() *ast.ForStmt {
	start := p.advance().Pos // for
	f := &ast.ForStmt{PosV: start}

	// `for { }` — infinite.
	if p.at(token.LBRACE) {
		f.Body = p.parseBlock()
		f.EndV = f.Body.End()
		return f
	}
	// `for let pat = expr { }` — for-let.
	if p.at(token.LET) {
		p.advance()
		f.IsForLet = true
		f.Pattern = p.parsePattern()
		p.expect(token.ASSIGN)
		prev := p.noStructLit
		p.noStructLit = true
		f.Iter = p.parseExpr()
		p.noStructLit = prev
		f.Body = p.parseBlock()
		f.EndV = f.Body.End()
		return f
	}
	// Ambiguous: `for EXPR { }` where EXPR might be `pattern in iterable`
	// or just a condition. Strategy: speculatively try to parse a pattern
	// followed by `in`. If that fails, fall back to parsing as expression.
	save := p.pos
	saveErrs := len(p.errs)
	pat := p.tryParseIterPattern()
	if pat != nil && p.at(token.IDENT) && p.peek().Value == "in" {
		p.advance()
		f.Pattern = pat
		prev := p.noStructLit
		p.noStructLit = true
		f.Iter = p.parseExpr()
		p.noStructLit = prev
		f.Body = p.parseBlock()
		f.EndV = f.Body.End()
		return f
	}
	// Restore and parse as condition expression.
	p.pos = save
	p.errs = p.errs[:saveErrs]
	prev := p.noStructLit
	p.noStructLit = true
	f.Iter = p.parseExpr()
	p.noStructLit = prev
	f.Body = p.parseBlock()
	f.EndV = f.Body.End()
	return f
}

// tryParseIterPattern attempts to parse a pattern suitable for a `for x in xs`
// loop. Returns nil if the input doesn't look like one. No panics are
// expected from parsePattern; callers handle the not-a-pattern case via a
// restore of Parser.pos.
func (p *Parser) tryParseIterPattern() ast.Pattern {
	switch p.peek().Kind {
	case token.IDENT, token.UNDERSCORE, token.LPAREN:
		return p.parsePattern()
	}
	return nil
}

// ---- Patterns ----

func (p *Parser) parsePattern() ast.Pattern {
	first := p.parsePatternOneAlt()
	if !p.at(token.BITOR) {
		return first
	}
	alts := []ast.Pattern{first}
	for p.eat(token.BITOR) {
		alts = append(alts, p.parsePatternOneAlt())
	}
	return &ast.OrPat{PosV: first.Pos(), EndV: alts[len(alts)-1].End(), Alts: alts}
}

func (p *Parser) parsePatternOneAlt() ast.Pattern {
	// Range pattern: literal `..` / `..=` literal or open range.
	if p.at(token.DOTDOT) || p.at(token.DOTDOTEQ) {
		return p.parseRangePatFromOpen()
	}
	pat := p.parsePatternAtom()
	// Check for binding: `name @ pattern`.
	if id, ok := pat.(*ast.IdentPat); ok && p.at(token.AT) {
		p.advance()
		sub := p.parsePatternOneAlt()
		return &ast.BindingPat{PosV: id.PosV, EndV: sub.End(), Name: id.Name, Pattern: sub}
	}
	// Check for range with start: `0..=9`, `10..20`.
	if lit, ok := patternToRangeStart(pat); ok && (p.at(token.DOTDOT) || p.at(token.DOTDOTEQ)) {
		inclusive := p.at(token.DOTDOTEQ)
		p.advance()
		var endExpr ast.Expr
		if isPatternRangeEnd(p.peek().Kind) {
			endExpr = p.parseRangeEndExpr()
		}
		return &ast.RangePat{PosV: pat.Pos(), EndV: p.lastEnd(), Start: lit, Stop: endExpr, Inclusive: inclusive}
	}
	return pat
}

func isPatternRangeEnd(k token.Kind) bool {
	switch k {
	case token.INT, token.FLOAT, token.CHAR, token.BYTE, token.MINUS:
		return true
	}
	return false
}

func (p *Parser) parseRangePatFromOpen() ast.Pattern {
	start := p.peek().Pos
	inclusive := p.at(token.DOTDOTEQ)
	p.advance()
	var endExpr ast.Expr
	if isPatternRangeEnd(p.peek().Kind) {
		endExpr = p.parseRangeEndExpr()
	}
	return &ast.RangePat{PosV: start, EndV: p.lastEnd(), Stop: endExpr, Inclusive: inclusive}
}

func (p *Parser) parseRangeEndExpr() ast.Expr {
	// Accept a signed literal or simple literal as range endpoint.
	if p.eat(token.MINUS) {
		x := p.parsePrimary()
		return &ast.UnaryExpr{PosV: x.Pos(), EndV: x.End(), Op: token.MINUS, X: x}
	}
	return p.parsePrimary()
}

func patternToRangeStart(pat ast.Pattern) (ast.Expr, bool) {
	lp, ok := pat.(*ast.LiteralPat)
	if !ok {
		return nil, false
	}
	return lp.Literal, true
}

func (p *Parser) parsePatternAtom() ast.Pattern {
	t := p.peek()
	switch t.Kind {
	case token.UNDERSCORE:
		p.advance()
		return &ast.WildcardPat{PosV: t.Pos, EndV: t.End}
	case token.INT, token.FLOAT, token.STRING, token.RAWSTRING, token.CHAR, token.BYTE:
		lit := p.parsePrimary()
		return &ast.LiteralPat{PosV: t.Pos, EndV: lit.End(), Literal: lit}
	case token.MINUS:
		// Negative numeric literal as pattern.
		p.advance()
		inner := p.parsePrimary()
		neg := &ast.UnaryExpr{PosV: t.Pos, EndV: inner.End(), Op: token.MINUS, X: inner}
		return &ast.LiteralPat{PosV: t.Pos, EndV: inner.End(), Literal: neg}
	case token.IDENT:
		// Could be: ident binding, variant pattern (Ident(...)), struct
		// pattern (Ident { ... }), or qualified variant (Pkg.Variant(...)).
		name := p.advance().Value
		path := []string{name}
		for p.at(token.DOT) && p.peekAt(1).Kind == token.IDENT {
			p.advance()
			path = append(path, p.advance().Value)
		}
		// true/false special literals by name.
		if len(path) == 1 && (name == "true" || name == "false") {
			lit := &ast.BoolLit{PosV: t.Pos, EndV: t.End, Value: name == "true"}
			return &ast.LiteralPat{PosV: t.Pos, EndV: t.End, Literal: lit}
		}
		// Variant with payload: Name(pat1, pat2).
		if p.at(token.LPAREN) {
			p.advance()
			var args []ast.Pattern
			for !p.at(token.RPAREN) && !p.at(token.EOF) {
				args = append(args, p.parsePattern())
				if !p.eat(token.COMMA) {
					break
				}
			}
			p.expect(token.RPAREN)
			return &ast.VariantPat{PosV: t.Pos, EndV: p.lastEnd(), Path: path, Args: args}
		}
		// Struct pattern: Name { field, field: pat, .. }.
		if p.at(token.LBRACE) {
			return p.parseStructPat(t.Pos, path)
		}
		// If the path is multi-segment, treat as bare variant reference
		// (e.g. `Color.Red`). Otherwise, it's an identifier binding.
		if len(path) > 1 {
			return &ast.VariantPat{PosV: t.Pos, EndV: p.lastEnd(), Path: path}
		}
		return &ast.IdentPat{PosV: t.Pos, EndV: t.End, Name: name}
	case token.LPAREN:
		p.advance()
		var elems []ast.Pattern
		for !p.at(token.RPAREN) && !p.at(token.EOF) {
			elems = append(elems, p.parsePattern())
			if !p.eat(token.COMMA) {
				break
			}
		}
		p.expect(token.RPAREN)
		return &ast.TuplePat{PosV: t.Pos, EndV: p.lastEnd(), Elems: elems}
	}
	p.errorf(t.Pos, "expected pattern, got %s", t.Kind)
	p.advance()
	return &ast.WildcardPat{PosV: t.Pos, EndV: t.End}
}

func (p *Parser) parseStructPat(pos token.Pos, path []string) ast.Pattern {
	p.expect(token.LBRACE)
	sp := &ast.StructPat{PosV: pos, Type: path}
	p.skipNewlines()
	for !p.at(token.RBRACE) && !p.at(token.EOF) {
		if p.at(token.DOTDOT) {
			p.advance()
			sp.Rest = true
			break
		}
		f := &ast.StructPatField{PosV: p.peek().Pos}
		f.Name = p.expect(token.IDENT).Value
		if p.eat(token.COLON) {
			f.Pattern = p.parsePattern()
		}
		sp.Fields = append(sp.Fields, f)
		if !p.eat(token.COMMA) {
			break
		}
		p.skipNewlines()
	}
	p.skipNewlines()
	p.expect(token.RBRACE)
	sp.EndV = p.lastEnd()
	return sp
}

// ---- Expressions (Pratt) ----

func (p *Parser) parseExpr() ast.Expr {
	return p.parseExprBP(0)
}

// Binding power table — higher binds tighter.
//
// Follows OSTY_GRAMMAR_v0.3 §R1. Levels:
//
//	2:  ?? (right-assoc)
//	3:  ||
//	4:  &&
//	5:  == != < > <= >=     (non-associative)
//	6:  .. ..=              (non-associative)
//	7:  | (bit-or)
//	8:  ^ (bit-xor)
//	9:  & (bit-and)
//	10: << >>
//	11: + -
//	12: * / %
//
// Non-associative levels: lbp == rbp so a same-level rhs cannot extend the
// chain — the Pratt loop bails on equal binding power. Right-associative
// `??` has rbp = lbp.
//
// Postfix (`.`, `?.`, `?`, `()`, `[]`, `::<>`) and unary (`-`, `!`, `~`)
// are handled in tryParsePostfix / parsePrefix.
func infixLBP(k token.Kind) (lbp, rbp int) {
	switch k {
	case token.QQ:
		// Right-associative: rbp <= lbp so a recursive call with min=rbp
		// re-enters at the same level.
		return 2, 2
	case token.OR:
		return 3, 4
	case token.AND:
		return 4, 5
	case token.EQ, token.NEQ, token.LT, token.GT, token.LEQ, token.GEQ:
		// Non-associative: rbp = lbp + 1 prevents a same-level operator
		// from being consumed in the inner recursive call. The outer
		// loop then catches a continuation explicitly and errors.
		return 5, 6
	case token.DOTDOT, token.DOTDOTEQ:
		return 6, 7
	case token.BITOR:
		return 7, 8
	case token.BITXOR:
		return 8, 9
	case token.BITAND:
		return 9, 10
	case token.SHL, token.SHR:
		return 10, 11
	case token.PLUS, token.MINUS:
		return 11, 12
	case token.STAR, token.SLASH, token.PERCENT:
		return 12, 13
	}
	return 0, 0
}

func (p *Parser) parseExprBP(minBP int) ast.Expr {
	left := p.parsePrefix()
	for {
		// Postfix operators: `.field`, `?.field`, `(args)`, `[i]`, `?`,
		// `::<T,U>`, struct literal `Ident { ... }`.
		if postfix := p.tryParsePostfix(left); postfix != nil {
			left = postfix
			continue
		}
		op := p.peek()
		lbp, rbp := infixLBP(op.Kind)
		if lbp == 0 || lbp < minBP {
			break
		}
		p.advance()
		if op.Kind == token.DOTDOT || op.Kind == token.DOTDOTEQ {
			// Range expression. The right-hand side is optional if the
			// next token does not begin an expression.
			var rhs ast.Expr
			if startsExpr(p.peek().Kind) {
				rhs = p.parseExprBP(rbp)
			}
			left = &ast.RangeExpr{
				PosV:      left.Pos(),
				EndV:      p.lastEnd(),
				Start:     left,
				Stop:      rhs,
				Inclusive: op.Kind == token.DOTDOTEQ,
			}
			// v0.2 R1: range is non-associative.
			if isNonAssocOp(p.peek().Kind) && sameLevel(op.Kind, p.peek().Kind) {
				next := p.peek()
				p.emit(diag.New(diag.Error,
					fmt.Sprintf("non-associative operator: chain of `%s` and `%s` requires parentheses",
						op.Kind, next.Kind)).
					Code(diag.CodeNonAssocChain).
					Primary(diag.Span{Start: next.Pos, End: next.End}, "second range operator here").
					Note("v0.2 R1: range operators do not chain — they are non-associative").
					Hint("group with parentheses, e.g. `(a..b)..c`").
					Build())
				break
			}
			continue
		}
		rhs := p.parseExprBP(rbp)
		left = &ast.BinaryExpr{
			PosV:  left.Pos(),
			EndV:  rhs.End(),
			Op:    op.Kind,
			Left:  left,
			Right: rhs,
		}
		// v0.2 R1: comparison (`a < b < c`) and range chains
		// (`a..b..c`) are non-associative. Pratt LBPs alone don't
		// enforce this for the OUTER chain, so explicitly reject a
		// follow-up operator at the same non-assoc level.
		if isNonAssocOp(op.Kind) && isNonAssocOp(p.peek().Kind) &&
			sameLevel(op.Kind, p.peek().Kind) {
			next := p.peek()
			p.emit(diag.New(diag.Error,
				fmt.Sprintf("non-associative operator: chain of `%s` and `%s` requires parentheses",
					op.Kind, next.Kind)).
				Code(diag.CodeNonAssocChain).
				Primary(diag.Span{Start: next.Pos, End: next.End}, "second operator here").
				Note("v0.2 R1: comparison and range operators do not chain — they are non-associative").
				Hint(fmt.Sprintf("rewrite as `(a %s b) %s c` or `a %s (b %s c)` to make grouping explicit",
					op.Kind, next.Kind, op.Kind, next.Kind)).
				Build())
			break
		}
	}
	return left
}

func isNonAssocOp(k token.Kind) bool {
	switch k {
	case token.EQ, token.NEQ, token.LT, token.GT, token.LEQ, token.GEQ,
		token.DOTDOT, token.DOTDOTEQ:
		return true
	}
	return false
}

// sameLevel reports whether two non-assoc operators share a precedence
// level (both comparison, or both range). Mixing comparison with range is
// also non-associative under v0.2 since they sit on different levels and
// would only meet in expressions already constrained by parens.
func sameLevel(a, b token.Kind) bool {
	cmp := func(k token.Kind) bool {
		switch k {
		case token.EQ, token.NEQ, token.LT, token.GT, token.LEQ, token.GEQ:
			return true
		}
		return false
	}
	rng := func(k token.Kind) bool { return k == token.DOTDOT || k == token.DOTDOTEQ }
	return (cmp(a) && cmp(b)) || (rng(a) && rng(b))
}

func startsExpr(k token.Kind) bool {
	switch k {
	case token.IDENT, token.INT, token.FLOAT, token.STRING, token.RAWSTRING,
		token.CHAR, token.BYTE,
		token.LPAREN, token.LBRACKET, token.LBRACE,
		token.MINUS, token.NOT, token.BITNOT, token.BITAND,
		token.IF, token.MATCH, token.FOR, token.RETURN,
		token.BITOR, // closure delimiter `|`
		token.UNDERSCORE:
		return true
	}
	return false
}

func (p *Parser) parsePrefix() ast.Expr {
	t := p.peek()
	switch t.Kind {
	case token.MINUS, token.NOT, token.BITNOT:
		op := p.advance()
		x := p.parsePrefix()
		return &ast.UnaryExpr{PosV: op.Pos, EndV: x.End(), Op: op.Kind, X: x}
	case token.DOTDOT, token.DOTDOTEQ:
		// Open-start range: `..end` or `..=end`.
		inclusive := t.Kind == token.DOTDOTEQ
		p.advance()
		var rhs ast.Expr
		if startsExpr(p.peek().Kind) {
			rhs = p.parseExprBP(91) // rbp of range
		}
		return &ast.RangeExpr{PosV: t.Pos, EndV: p.lastEnd(), Stop: rhs, Inclusive: inclusive}
	}
	return p.parsePrimary()
}

func (p *Parser) parsePrimary() ast.Expr {
	t := p.peek()
	switch t.Kind {
	case token.INT:
		p.advance()
		return &ast.IntLit{PosV: t.Pos, EndV: t.End, Text: t.Value}
	case token.FLOAT:
		p.advance()
		return &ast.FloatLit{PosV: t.Pos, EndV: t.End, Text: t.Value}
	case token.STRING, token.RAWSTRING:
		p.advance()
		sl := &ast.StringLit{
			PosV:     t.Pos,
			EndV:     t.End,
			IsRaw:    t.Kind == token.RAWSTRING,
			IsTriple: t.Triple,
		}
		for _, part := range t.Parts {
			if part.Kind == token.PartText {
				sl.Parts = append(sl.Parts, ast.StringPart{IsLit: true, Lit: part.Text})
			} else {
				// Re-parse the embedded expression tokens. We construct a
				// sub-parser seeded with the interpolation's tokens plus a
				// sentinel EOF.
				sub := newParser(append(append([]token.Token(nil), part.Expr...), token.Token{Kind: token.EOF}))
				expr := sub.parseExpr()
				p.errs = append(p.errs, sub.errs...)
				sl.Parts = append(sl.Parts, ast.StringPart{IsLit: false, Expr: expr})
			}
		}
		return sl
	case token.CHAR:
		p.advance()
		var r rune
		for _, rr := range t.Value {
			r = rr
			break
		}
		return &ast.CharLit{PosV: t.Pos, EndV: t.End, Value: r}
	case token.BYTE:
		p.advance()
		var b byte
		if len(t.Value) > 0 {
			b = t.Value[0]
		}
		return &ast.ByteLit{PosV: t.Pos, EndV: t.End, Value: b}
	case token.IDENT:
		p.advance()
		if t.Value == "true" {
			return &ast.BoolLit{PosV: t.Pos, EndV: t.End, Value: true}
		}
		if t.Value == "false" {
			return &ast.BoolLit{PosV: t.Pos, EndV: t.End, Value: false}
		}
		return &ast.Ident{PosV: t.Pos, EndV: t.End, Name: t.Value}
	case token.LPAREN:
		return p.parseParenOrTuple()
	case token.LBRACKET:
		return p.parseListLit()
	case token.LBRACE:
		return p.parseBlockOrMap()
	case token.IF:
		return p.parseIfExpr()
	case token.MATCH:
		return p.parseMatchExpr()
	case token.BITOR, token.OR:
		// `|...|` closure or `||` empty-param closure.
		return p.parseClosure()
	case token.UNDERSCORE:
		// `_` is a pattern wildcard; it is not a value. Give a focused
		// diagnostic instead of the generic "unexpected _ in expression"
		// message that otherwise follows.
		p.emit(diag.New(diag.Error,
			"`_` is a pattern wildcard and cannot appear as a value").
			Code(diag.CodeWildcardInExpr).
			Primary(diag.Span{Start: t.Pos, End: t.End}, "wildcard used as expression").
			Hint("use a concrete value or, for ignored bindings, `let _ = expr`").
			Build())
		p.advance()
		return &ast.Ident{PosV: t.Pos, EndV: t.End, Name: "_"}
	}
	p.errorf(t.Pos, "unexpected %s in expression", t.Kind)
	p.advance()
	return &ast.Ident{PosV: t.Pos, EndV: t.End, Name: "<error>"}
}

func (p *Parser) parseParenOrTuple() ast.Expr {
	lp := p.advance() // (
	// Inside parens the no-struct-lit restriction is lifted — struct
	// literals nested inside grouping are permitted (e.g. `if (T{..}) ==`).
	prev := p.noStructLit
	p.noStructLit = false
	defer func() { p.noStructLit = prev }()

	// Empty `()` — unit.
	if p.eat(token.RPAREN) {
		return &ast.TupleExpr{PosV: lp.Pos, EndV: p.lastEnd()}
	}
	p.skipNewlines()
	first := p.parseExpr()
	p.skipNewlines()
	// `(expr)` — parens.
	if p.at(token.RPAREN) {
		p.advance()
		return &ast.ParenExpr{PosV: lp.Pos, EndV: p.lastEnd(), X: first}
	}
	// Tuple.
	elems := []ast.Expr{first}
	for p.eat(token.COMMA) {
		p.skipNewlines()
		if p.at(token.RPAREN) {
			break
		}
		elems = append(elems, p.parseExpr())
		p.skipNewlines()
	}
	p.expect(token.RPAREN)
	return &ast.TupleExpr{PosV: lp.Pos, EndV: p.lastEnd(), Elems: elems}
}

func (p *Parser) parseListLit() ast.Expr {
	lb := p.advance() // [
	var elems []ast.Expr
	p.skipNewlines()
	for !p.at(token.RBRACKET) && !p.at(token.EOF) {
		elems = append(elems, p.parseExpr())
		p.skipNewlines()
		if !p.eat(token.COMMA) {
			break
		}
		p.skipNewlines()
	}
	p.expect(token.RBRACKET)
	return &ast.ListExpr{PosV: lb.Pos, EndV: p.lastEnd(), Elems: elems}
}

// parseBlockOrMap disambiguates `{ ... }` between a block expression and a
// map/set literal. Heuristics:
//
//   `{:}`              -> empty map
//   `{ "k": v, ... }`  -> map
//   `{ let x = ... }`  -> block (starts with keyword)
//   `{ expr, expr }`   -> ambiguous; treated as block (statements).
//                          A map literal requires at least one `:` entry.
//
// We look ahead: if after `{` we see `:` immediately, it's `{:}`. Else we
// scan the first top-level token. If we find a `:` before a statement
// terminator or `}`, it's a map. Otherwise it's a block.
func (p *Parser) parseBlockOrMap() ast.Expr {
	lb := p.peek()
	// `{:}` empty map.
	if p.peekAt(1).Kind == token.COLON && p.peekAt(2).Kind == token.RBRACE {
		p.advance() // {
		p.advance() // :
		p.advance() // }
		return &ast.MapExpr{PosV: lb.Pos, EndV: p.lastEnd(), Empty: true}
	}
	if p.looksLikeMapLiteral() {
		return p.parseMapLit()
	}
	// Otherwise, parse as block expression.
	return p.parseBlock()
}

// looksLikeMapLiteral peeks ahead after `{` to decide whether we are in a
// map literal. We accept the first key as an expression and check whether
// the next token after it is `:`.
func (p *Parser) looksLikeMapLiteral() bool {
	// Save parser state and speculatively parse one expression to see if
	// a `:` follows. Restored unconditionally. No panic is expected from
	// parseExpr; errors are collected and rolled back.
	save := p.pos
	saveErrs := len(p.errs)
	p.advance() // {
	p.skipNewlines()
	if p.at(token.RBRACE) {
		// Empty `{}` — treat as block with unit value, not map.
		p.pos = save
		p.errs = p.errs[:saveErrs]
		return false
	}
	// If the first token is a keyword that begins a statement, it's a block.
	switch p.peek().Kind {
	case token.LET, token.RETURN, token.BREAK, token.CONTINUE, token.DEFER, token.FOR:
		p.pos = save
		p.errs = p.errs[:saveErrs]
		return false
	}
	_ = p.parseExpr()
	result := p.at(token.COLON) && p.peekAt(1).Kind != token.COLON
	p.pos = save
	p.errs = p.errs[:saveErrs]
	return result
}

func (p *Parser) parseMapLit() ast.Expr {
	lb := p.advance() // {
	m := &ast.MapExpr{PosV: lb.Pos}
	p.skipNewlines()
	for !p.at(token.RBRACE) && !p.at(token.EOF) {
		k := p.parseExpr()
		p.expect(token.COLON)
		v := p.parseExpr()
		m.Entries = append(m.Entries, &ast.MapEntry{Key: k, Value: v})
		p.skipNewlines()
		if !p.eat(token.COMMA) {
			break
		}
		p.skipNewlines()
	}
	p.expect(token.RBRACE)
	m.EndV = p.lastEnd()
	return m
}

func (p *Parser) parseIfExpr() ast.Expr {
	start := p.advance().Pos // if
	e := &ast.IfExpr{PosV: start}
	if p.at(token.LET) {
		p.advance()
		e.IsIfLet = true
		e.Pattern = p.parsePattern()
		p.expect(token.ASSIGN)
	}
	prev := p.noStructLit
	p.noStructLit = true
	e.Cond = p.parseExpr()
	p.noStructLit = prev
	e.Then = p.parseBlock()
	// v0.2 O2: `} else` must sit on the same line. The lexer-emitted
	// NEWLINE between `}` and `else` is significant — if we see one, the
	// `if` is complete and any subsequent `else` is a syntax error
	// reported by the surrounding context.
	if p.eat(token.ELSE) {
		if p.at(token.IF) {
			e.Else = p.parseIfExpr()
		} else {
			e.Else = p.parseBlock()
		}
	} else if p.peekPastNewlines().Kind == token.ELSE {
		// User wrote `} \n else` — produce a focused diagnostic with the
		// hint pointing at the v0.2 O2 rule. We do NOT recover by
		// accepting the else (that would silently violate the rule), so
		// the orphan `else` keyword surfaces a follow-up error from the
		// surrounding context.
		// Find the `else` token.
		i := p.pos
		for i < len(p.toks) && p.toks[i].Kind == token.NEWLINE {
			i++
		}
		elseTok := p.toks[i]
		p.emit(diag.New(diag.Error,
			"`else` may not be separated from the closing `}` by a newline").
			Code(diag.CodeElseAcrossNewline).
			Primary(diag.Span{Start: elseTok.Pos, End: elseTok.End}, "orphaned `else` here").
			Note("v0.2 O2: `} else` must sit on the same line").
			Hint("move the `else` keyword onto the same line as the closing `}`, e.g. `} else {`").
			Build())
	}
	e.EndV = p.lastEnd()
	return e
}

func (p *Parser) parseMatchExpr() ast.Expr {
	start := p.advance().Pos // match
	m := &ast.MatchExpr{PosV: start}
	prev := p.noStructLit
	p.noStructLit = true
	m.Scrutinee = p.parseExpr()
	p.noStructLit = prev
	p.expect(token.LBRACE)
	p.skipNewlines()
	for !p.at(token.RBRACE) && !p.at(token.EOF) {
		a := &ast.MatchArm{PosV: p.peek().Pos}
		a.Pattern = p.parsePattern()
		if p.at(token.IF) {
			p.advance()
			prev := p.noStructLit
			p.noStructLit = true
			a.Guard = p.parseExpr()
			p.noStructLit = prev
		}
		p.expect(token.ARROW)
		a.Body = p.parseExpr()
		m.Arms = append(m.Arms, a)
		p.skipNewlines()
		if !p.eat(token.COMMA) {
			break
		}
		p.skipNewlines()
	}
	p.expect(token.RBRACE)
	m.EndV = p.lastEnd()
	return m
}

func (p *Parser) parseClosure() ast.Expr {
	start := p.peek().Pos
	// `||` is lexed as token.OR — treat as empty param list.
	if p.eat(token.OR) {
		body := p.parseClosureBody(false)
		return &ast.ClosureExpr{PosV: start, EndV: body.End(), Body: body}
	}
	p.expect(token.BITOR)
	var params []*ast.Param
	for !p.at(token.BITOR) && !p.at(token.EOF) {
		prm := &ast.Param{PosV: p.peek().Pos}
		// SPEC_GAPS G4: closure params accept LetPattern, not just IDENT.
		// A `(`, `_` or struct-typed-name at the start indicates a
		// destructuring pattern; otherwise, a plain identifier name.
		switch p.peek().Kind {
		case token.LPAREN, token.UNDERSCORE:
			// Use parsePatternOneAlt to avoid consuming `|` as an or-
			// pattern alternation (the `|` here closes the closure
			// parameter list).
			prm.Pattern = p.parsePatternOneAlt()
		case token.IDENT:
			// `User { name }` style destructure starts with an uppercase
			// type name followed by `{`.
			if isUpperName(p.peek().Value) && p.peekAt(1).Kind == token.LBRACE {
				prm.Pattern = p.parsePatternOneAlt()
			} else {
				prm.Name = p.advance().Value
			}
		default:
			p.errorf(p.peek().Pos, "expected closure parameter, got %s", p.peek().Kind)
			p.advance()
		}
		if p.eat(token.COLON) {
			prm.Type = p.parseType()
		}
		params = append(params, prm)
		if !p.eat(token.COMMA) {
			break
		}
	}
	p.expect(token.BITOR)
	var retType ast.Type
	if p.eat(token.ARROW) {
		retType = p.parseType()
	}
	// v0.2 R25: when a return type is specified, the closure body MUST be
	// a block. Otherwise either a block or single expression is allowed.
	body := p.parseClosureBody(retType != nil)
	return &ast.ClosureExpr{PosV: start, EndV: body.End(), Params: params, ReturnType: retType, Body: body}
}

func (p *Parser) parseClosureBody(requireBlock bool) ast.Expr {
	if p.at(token.LBRACE) {
		return p.parseBlock()
	}
	if requireBlock {
		t := p.peek()
		p.emit(diag.New(diag.Error,
			"closure with explicit return type requires a `{ ... }` block body").
			Code(diag.CodeClosureRetReqBlock).
			Primary(diag.Span{Start: t.Pos, End: t.End}, "expected `{` here").
			Note("v0.2 R25: when a closure declares its return type, the body must be a block").
			Hint("wrap the body in braces, e.g. `|x| -> Int { x * 2 }`").
			Build())
	}
	return p.parseExpr()
}

// tryParsePostfix handles postfix ops and returns a new expression if one
// was consumed, else nil.
func (p *Parser) tryParsePostfix(left ast.Expr) ast.Expr {
	switch p.peek().Kind {
	case token.DOT:
		p.advance()
		name := p.consumeFieldName()
		return &ast.FieldExpr{PosV: left.Pos(), EndV: p.lastEnd(), X: left, Name: name}
	case token.QDOT:
		p.advance()
		name := p.consumeFieldName()
		return &ast.FieldExpr{PosV: left.Pos(), EndV: p.lastEnd(), X: left, Name: name, IsOptional: true}
	case token.LPAREN:
		return p.parseCallArgs(left)
	case token.LBRACKET:
		return p.parseIndex(left)
	case token.QUESTION:
		q := p.advance()
		return &ast.QuestionExpr{PosV: left.Pos(), EndV: q.End, X: left}
	case token.COLONCOLON:
		// v0.2 O6: turbofish-only. `::` MUST be followed by `<`; any other
		// follow-up token is a syntax error rather than silent fallthrough.
		colcol := p.advance() // ::
		if !p.at(token.LT) {
			p.emit(diag.New(diag.Error,
				fmt.Sprintf("expected `<` after `::`, got %s", p.peek().Kind)).
				Code(diag.CodeTurbofishMissingLT).
				Primary(diag.Span{Start: colcol.Pos, End: p.peek().End}, "`::` here").
				Note("`::` is reserved for turbofish (explicit generic args), e.g. `parse::<Config>(text)`").
				Hint("did you mean `.`? Member access uses `.`, not `::`.").
				Build())
			return left
		}
		p.advance() // <
		var args []ast.Type
		for !p.at(token.GT) && !p.at(token.SHR) && !p.at(token.GEQ) && !p.at(token.SHREQ) && !p.at(token.EOF) {
			args = append(args, p.parseType())
			if !p.eat(token.COMMA) {
				break
			}
		}
		p.expectTypeGT()
		return &ast.TurbofishExpr{PosV: left.Pos(), EndV: p.lastEnd(), Base: left, Args: args}
	case token.LBRACE:
		// Struct literal: `Name { field: expr, .. }`. Only valid when the
		// left is a type reference (Ident or FieldExpr chain) and we're
		// not in a no-struct-lit context (`if`, `for`, `match` head).
		if !p.noStructLit && isTypeRef(left) {
			return p.parseStructLit(left)
		}
	}
	return nil
}

// isUpperName reports whether the first letter of name is uppercase
// ASCII. Used to distinguish type references (PascalCase) from values.
func isUpperName(name string) bool {
	if name == "" {
		return false
	}
	c := name[0]
	return c >= 'A' && c <= 'Z'
}

func isTypeRef(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.Ident:
		if v.Name == "" {
			return false
		}
		r := v.Name[0]
		return r >= 'A' && r <= 'Z'
	case *ast.FieldExpr:
		if v.Name == "" {
			return false
		}
		r := v.Name[0]
		return r >= 'A' && r <= 'Z' && isTypeRefOrPath(v.X)
	}
	return false
}

func isTypeRefOrPath(e ast.Expr) bool {
	switch e.(type) {
	case *ast.Ident, *ast.FieldExpr:
		return true
	}
	return false
}

func (p *Parser) parseCallArgs(fn ast.Expr) ast.Expr {
	p.advance() // (
	call := &ast.CallExpr{PosV: fn.Pos(), Fn: fn}
	p.skipNewlines()
	for !p.at(token.RPAREN) && !p.at(token.EOF) {
		arg := &ast.Arg{PosV: p.peek().Pos}
		// Keyword argument: ident `:` expr. Detect via 2-token lookahead.
		if p.at(token.IDENT) && p.peekAt(1).Kind == token.COLON {
			// Careful: a map literal might look similar, but `foo(k: v)` is a
			// keyword argument while `foo({k: v})` is a map. Inside a call
			// arg list, bare `name: value` is always a keyword argument.
			arg.Name = p.advance().Value
			p.advance() // :
		}
		arg.Value = p.parseExpr()
		call.Args = append(call.Args, arg)
		p.skipNewlines()
		if !p.eat(token.COMMA) {
			break
		}
		p.skipNewlines()
	}
	p.expect(token.RPAREN)
	call.EndV = p.lastEnd()
	return call
}

func (p *Parser) parseIndex(x ast.Expr) ast.Expr {
	p.advance() // [
	idx := p.parseExpr()
	p.expect(token.RBRACKET)
	return &ast.IndexExpr{PosV: x.Pos(), EndV: p.lastEnd(), X: x, Index: idx}
}

func (p *Parser) parseStructLit(typeExpr ast.Expr) ast.Expr {
	p.advance() // {
	sl := &ast.StructLit{PosV: typeExpr.Pos(), Type: typeExpr}
	p.skipNewlines()
	for !p.at(token.RBRACE) && !p.at(token.EOF) {
		// Spread: `..expr`.
		if p.at(token.DOTDOT) {
			p.advance()
			sl.Spread = p.parseExpr()
			p.skipNewlines()
			p.eat(token.COMMA)
			p.skipNewlines()
			continue
		}
		f := &ast.StructLitField{PosV: p.peek().Pos}
		f.Name = p.expect(token.IDENT).Value
		if p.eat(token.COLON) {
			f.Value = p.parseExpr()
		}
		sl.Fields = append(sl.Fields, f)
		p.skipNewlines()
		if !p.eat(token.COMMA) {
			break
		}
		p.skipNewlines()
	}
	p.expect(token.RBRACE)
	sl.EndV = p.lastEnd()
	return sl
}
