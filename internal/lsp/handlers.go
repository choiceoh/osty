package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/format"
	"github.com/osty/osty/internal/repair"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
)

// ---- Lifecycle ----

// handleInitialize parses the client's capabilities (only the
// position-encoding hint is read), flips the server into the
// initialized state, and advertises our own capabilities. Currently
// we only speak UTF-16 — the LSP default — because the position
// converter in convert.go is hardcoded to that encoding.
func (s *Server) handleInitialize(req *rpcRequest) {
	var params InitializeParams
	if err := unmarshalParams(req, &params); err != nil {
		_ = s.conn.writeError(req.ID, errInvalidParams, err.Error())
		return
	}
	s.mu.Lock()
	s.initialized = true
	s.mu.Unlock()

	result := InitializeResult{
		Capabilities: ServerCapabilities{
			TextDocumentSync: &TextDocumentSyncOptions{
				OpenClose: true,
				Change:    SyncFull,
			},
			HoverProvider:              true,
			DefinitionProvider:         true,
			DocumentFormattingProvider: true,
			DocumentSymbolProvider:     true,
			ReferencesProvider:         true,
			RenameProvider:             true,
			WorkspaceSymbolProvider:    true,
			InlayHintProvider:          true,
			CodeActionProvider: &CodeActionOptions{
				CodeActionKinds: []string{
					CodeActionQuickFix,
					CodeActionSourceOrganizeImports,
					CodeActionSourceFixAllOsty,
					CodeActionSourceFixAll,
				},
			},
			CompletionProvider: &CompletionOptions{
				// `.` triggers member completion (pkg.fn, recv.method).
				TriggerCharacters: []string{"."},
			},
			SignatureHelpProvider: &SignatureHelpOptions{
				TriggerCharacters:   []string{"(", ","},
				RetriggerCharacters: []string{","},
			},
			SemanticTokensProvider: &SemanticTokensOptions{
				Legend: SemanticTokensLegend{
					TokenTypes:     semanticTokenTypes,
					TokenModifiers: semanticTokenModifiers,
				},
				Full: true,
			},
			PositionEncoding: "utf-16",
		},
		ServerInfo: &ServerInfo{
			Name:    ServerName,
			Version: "0.1.0",
		},
		PositionEncoding: "utf-16",
	}
	replyJSON(s.conn, req.ID, result)
}

// ---- Text synchronization ----

// handleDidOpen records the opened document, runs analysis, and
// pushes the first batch of diagnostics so the editor paints red
// squigglies immediately.
func (s *Server) handleDidOpen(req *rpcRequest) {
	var params DidOpenTextDocumentParams
	if err := unmarshalParams(req, &params); err != nil {
		s.log.Printf("didOpen: %v", err)
		return
	}
	s.refreshDoc(
		params.TextDocument.URI,
		params.TextDocument.Version,
		[]byte(params.TextDocument.Text),
	)
}

// handleDidChange applies full-text changes (we only advertise
// SyncFull, so each change event carries the whole new document) and
// republishes diagnostics. Incremental sync is a future optimization.
func (s *Server) handleDidChange(req *rpcRequest) {
	var params DidChangeTextDocumentParams
	if err := unmarshalParams(req, &params); err != nil {
		s.log.Printf("didChange: %v", err)
		return
	}
	prev := s.docs.get(params.TextDocument.URI)
	if prev == nil {
		s.log.Printf("didChange for untracked %q", params.TextDocument.URI)
		return
	}
	// Full sync: the last change event holds the definitive text.
	// An empty ContentChanges slice (spec-legal though unusual) means
	// "no new text", in which case we keep the previous buffer.
	src := prev.src
	if n := len(params.ContentChanges); n > 0 {
		src = []byte(params.ContentChanges[n-1].Text)
	}
	s.refreshDoc(params.TextDocument.URI, params.TextDocument.Version, src)
}

// handleDidClose forgets the document and sends an empty diagnostic
// set so the editor clears any stale markers.
func (s *Server) handleDidClose(req *rpcRequest) {
	var params DidCloseTextDocumentParams
	if err := unmarshalParams(req, &params); err != nil {
		s.log.Printf("didClose: %v", err)
		return
	}
	s.docs.remove(params.TextDocument.URI)
	s.wsIndex.invalidate()
	_ = s.conn.writeNotification("textDocument/publishDiagnostics",
		PublishDiagnosticsParams{
			URI:         params.TextDocument.URI,
			Diagnostics: []LSPDiagnostic{},
		})
}

// publishDiagnostics converts the cached diagnostic list into LSP
// form and pushes it to the client. Called from didOpen/didChange.
// Skips the notification when the payload is byte-identical to the
// previous publish — unchanged-diagnostics re-renders are pure
// noise for the editor.
func (s *Server) publishDiagnostics(doc *document) {
	diags := make([]LSPDiagnostic, 0, len(doc.analysis.diags))
	for _, d := range doc.analysis.diags {
		diags = append(diags, toLSPDiag(doc.analysis.lines, d))
	}
	if diagsEqual(doc.lastDiags, diags) {
		return
	}
	doc.lastDiags = diags
	v := doc.version
	_ = s.conn.writeNotification("textDocument/publishDiagnostics",
		PublishDiagnosticsParams{
			URI:         doc.uri,
			Version:     &v,
			Diagnostics: diags,
		})
}

// diagsEqual reports whether two published diagnostic slices carry
// identical payloads. LSPDiagnostic is all-comparable so element-wise
// `!=` suffices.
func diagsEqual(a, b []LSPDiagnostic) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// toLSPDiag is the severity + range mapping between our internal
// Diagnostic and the LSP wire form.
func toLSPDiag(li *lineIndex, d *diag.Diagnostic) LSPDiagnostic {
	// Prefer the primary span for the range. Fall back to the first
	// span, then to a zero range — clients tolerate the last case by
	// showing the diagnostic at the top of the file.
	var rng Range
	if sp := primarySpan(d); sp != nil {
		rng = li.ostyRange(*sp)
	} else if len(d.Spans) > 0 {
		rng = li.ostyRange(d.Spans[0].Span)
	}
	payload := LSPDiagnosticPayloadFor(d.Severity.String(), d.Message, d.Hint, d.Notes)
	return LSPDiagnostic{
		Range:    rng,
		Severity: DiagnosticSeverity(payload.Severity),
		Code:     d.Code,
		Source:   ServerName,
		Message:  payload.Message,
	}
}

// primarySpan finds the first LabeledSpan with Primary=true, or nil.
func primarySpan(d *diag.Diagnostic) *diag.Span {
	for i := range d.Spans {
		if d.Spans[i].Primary {
			return &d.Spans[i].Span
		}
	}
	return nil
}

// ---- Hover ----

// handleHover locates the identifier under the cursor, asks the
// checker for its type, and formats a small Markdown block. When no
// identifier is under the cursor (whitespace, punctuation) we return
// `null` per spec.
func (s *Server) handleHover(req *rpcRequest) {
	var params HoverParams
	if err := unmarshalParams(req, &params); err != nil {
		_ = s.conn.writeError(req.ID, errInvalidParams, err.Error())
		return
	}
	doc := s.docs.get(params.TextDocument.URI)
	if doc == nil {
		replyJSON(s.conn, req.ID, nil)
		return
	}
	pos := doc.analysis.lines.lspToOsty(params.Position)

	var (
		node     ast.Node
		sym      *resolve.Symbol
		fallback string
	)
	if id, hit := findIdentAt(doc.analysis, pos); id != nil {
		node, sym, fallback = id, hit, id.Name
	} else if nt, hit := findNamedTypeAt(doc.analysis, pos); nt != nil && hit != nil {
		// NamedType hovers require a resolved symbol — there's
		// nothing useful to show for an unknown type reference, so
		// we leave node nil and let the guard below return null.
		node, sym = nt, hit
	}
	if node == nil {
		replyJSON(s.conn, req.ID, nil)
		return
	}
	h := hoverForSymbol(sym, fallback, doc.analysis.check)
	h.Range = ptrRange(doc.analysis.lines.ostyRange(
		diag.Span{Start: node.Pos(), End: node.End()}))
	replyJSON(s.conn, req.ID, h)
}

// hoverForSymbol formats the markdown block shown on hover. When sym
// is nil we fall back to nameFallback (the raw identifier text) so
// unresolved references still get a minimal popup.
func hoverForSymbol(sym *resolve.Symbol, nameFallback string, r *check.Result) *Hover {
	var b strings.Builder
	b.WriteString("```osty\n")
	if sym != nil {
		writeSymSignature(&b, sym, r)
	} else {
		fmt.Fprintf(&b, "%s\n", nameFallback)
	}
	b.WriteString("```")
	if sym != nil {
		if doc := symbolDoc(sym); doc != "" {
			b.WriteString("\n\n")
			b.WriteString(doc)
		}
	}
	return &Hover{Contents: MarkupContent{Kind: MarkupKindMarkdown, Value: b.String()}}
}

// writeSymSignature emits a one-line declaration-shaped summary for
// a symbol. For values (let/param/fn) the checker's type is shown;
// for types (struct/enum/interface) we lead with the keyword.
func writeSymSignature(b *strings.Builder, sym *resolve.Symbol, r *check.Result) {
	typeText := ""
	if r != nil {
		if t := r.LookupSymType(sym); t != nil {
			typeText = t.String()
		}
	}
	b.WriteString(LSPHoverSignatureLine(sym.Kind.String(), sym.Name, typeText))
	b.WriteByte('\n')
}

// symbolDoc extracts the leading `///` comment from a symbol's
// declaration AST node. Returns the empty string for builtins, for
// symbols without docs, and for AST node types we don't yet know how
// to read.
func symbolDoc(sym *resolve.Symbol) string {
	switch n := sym.Decl.(type) {
	case *ast.FnDecl:
		return n.DocComment
	case *ast.StructDecl:
		return n.DocComment
	case *ast.EnumDecl:
		return n.DocComment
	case *ast.InterfaceDecl:
		return n.DocComment
	case *ast.TypeAliasDecl:
		return n.DocComment
	case *ast.LetDecl:
		return n.DocComment
	case *ast.Variant:
		return n.DocComment
	}
	return ""
}

// ---- Definition ----

// handleDefinition locates the identifier under the cursor and, if
// its resolver-assigned symbol has an AST declaration (i.e. it's not
// a builtin), replies with a Location pointing at the declaration
// site. Builtins return `null` because there's no source to jump to.
func (s *Server) handleDefinition(req *rpcRequest) {
	var params DefinitionParams
	if err := unmarshalParams(req, &params); err != nil {
		_ = s.conn.writeError(req.ID, errInvalidParams, err.Error())
		return
	}
	doc := s.docs.get(params.TextDocument.URI)
	if doc == nil {
		replyJSON(s.conn, req.ID, nil)
		return
	}
	pos := doc.analysis.lines.lspToOsty(params.Position)

	var sym *resolve.Symbol
	if _, s2 := findIdentAt(doc.analysis, pos); s2 != nil {
		sym = s2
	} else if _, s2 := findNamedTypeAt(doc.analysis, pos); s2 != nil {
		sym = s2
	}
	if sym == nil || sym.Decl == nil || sym.Pos.Line == 0 {
		replyJSON(s.conn, req.ID, nil)
		return
	}
	// Delegate to the same name-aware resolver that references/rename
	// use so "jump to definition" lands on the identifier, not on
	// the `pub fn ` prefix.
	if loc, ok := s.declLocation(doc, sym); ok {
		replyJSON(s.conn, req.ID, loc)
		return
	}
	rng := doc.analysis.lines.ostyRange(diag.Span{
		Start: sym.Decl.Pos(),
		End:   sym.Decl.End(),
	})
	replyJSON(s.conn, req.ID, Location{URI: params.TextDocument.URI, Range: rng})
}

// ---- Formatting ----

// handleFormatting runs the same repair+format pipeline as `osty fmt` and
// returns a single TextEdit that replaces the whole document. If the source
// still has parse errors after repair, the formatter refuses to produce
// output; we reply with an empty edit list so the client doesn't rewrite
// the buffer with `null`.
func (s *Server) handleFormatting(req *rpcRequest) {
	var params DocumentFormattingParams
	if err := unmarshalParams(req, &params); err != nil {
		_ = s.conn.writeError(req.ID, errInvalidParams, err.Error())
		return
	}
	doc := s.docs.get(params.TextDocument.URI)
	if doc == nil {
		replyJSON(s.conn, req.ID, []TextEdit{})
		return
	}
	repaired := repair.Source(doc.src)
	out, _, err := format.Source(repaired.Source)
	if err != nil {
		replyJSON(s.conn, req.ID, []TextEdit{})
		return
	}
	if bytes.Equal(out, doc.src) {
		// No-op formatting; avoid churning the editor's dirty flag.
		replyJSON(s.conn, req.ID, []TextEdit{})
		return
	}
	// Replace [start-of-file, end-of-file) with the formatted text.
	endLine := uint32(0)
	if n := len(doc.analysis.lines.lines); n > 0 {
		endLine = uint32(n - 1)
	}
	lastLineStart := 0
	if n := len(doc.analysis.lines.lines); n > 0 {
		lastLineStart = doc.analysis.lines.lines[n-1]
	}
	endChar := utf16UnitsInPrefix(doc.src[lastLineStart:])
	edit := TextEdit{
		Range: Range{
			Start: Position{Line: 0, Character: 0},
			End:   Position{Line: endLine, Character: endChar},
		},
		NewText: string(out),
	}
	replyJSON(s.conn, req.ID, []TextEdit{edit})
}

// ---- Document symbols ----

// handleDocumentSymbol walks the top-level declarations and emits
// one DocumentSymbol per entry. Structs, enums and interfaces carry
// nested children (fields, variants, methods).
//
// Range and SelectionRange are set to the same full-declaration
// span for every entry. Narrowing SelectionRange to the name would
// need a per-decl rescan we haven't wired through.
func (s *Server) handleDocumentSymbol(req *rpcRequest) {
	var params DocumentSymbolParams
	if err := unmarshalParams(req, &params); err != nil {
		_ = s.conn.writeError(req.ID, errInvalidParams, err.Error())
		return
	}
	doc := s.docs.get(params.TextDocument.URI)
	if doc == nil || doc.analysis.file == nil {
		replyJSON(s.conn, req.ID, []DocumentSymbol{})
		return
	}
	li := doc.analysis.lines
	entries := walkFileDecls(doc.analysis.file)
	syms := make([]DocumentSymbol, 0, len(entries))
	for _, e := range entries {
		syms = append(syms, documentSymbolFromEntry(li, e))
	}
	replyJSON(s.conn, req.ID, syms)
}

// documentSymbolFromEntry turns a declEntry into the tree shape
// `textDocument/documentSymbol` returns.
func documentSymbolFromEntry(li *lineIndex, e declEntry) DocumentSymbol {
	rng := li.ostyRange(diag.Span{Start: e.node.Pos(), End: e.node.End()})
	var children []DocumentSymbol
	if len(e.nested) > 0 {
		children = make([]DocumentSymbol, 0, len(e.nested))
		for _, c := range e.nested {
			children = append(children, documentSymbolFromEntry(li, c))
		}
	}
	return DocumentSymbol{
		Name:           e.name,
		Kind:           e.kind,
		Range:          rng,
		SelectionRange: rng,
		Children:       children,
	}
}

// ---- Lookup helpers ----

// findIdentAt scans the resolver's Refs map for an Ident whose span
// contains pos. Returns the Ident and its resolved symbol, or (nil,
// nil) when no identifier sits under the cursor.
//
// O(n) in the number of idents; fine for the buffer sizes humans
// edit. If/when we need to scale, replace with an interval tree
// indexed by byte offset.
func findIdentAt(a *docAnalysis, pos token.Pos) (*ast.Ident, *resolve.Symbol) {
	if a == nil || a.resolve == nil {
		return nil, nil
	}
	var best *ast.Ident
	for id := range a.resolve.Refs {
		if containsPos(id.Pos(), id.End(), pos) {
			// Prefer the narrowest span (useful once we have more
			// nested node types, though Idents don't nest).
			if best == nil || spanWidth(id) < spanWidth(best) {
				best = id
			}
		}
	}
	if best == nil {
		return nil, nil
	}
	return best, a.resolve.Refs[best]
}

// findNamedTypeAt mirrors findIdentAt for NamedType head references.
func findNamedTypeAt(a *docAnalysis, pos token.Pos) (*ast.NamedType, *resolve.Symbol) {
	if a == nil || a.resolve == nil {
		return nil, nil
	}
	var best *ast.NamedType
	for nt := range a.resolve.TypeRefs {
		if containsPos(nt.Pos(), nt.End(), pos) {
			if best == nil || spanWidth(nt) < spanWidth(best) {
				best = nt
			}
		}
	}
	if best == nil {
		return nil, nil
	}
	return best, a.resolve.TypeRefs[best]
}

// containsPos reports whether pos lies within [start, end). LSP
// clients routinely point at the first character *after* the token
// when the cursor is "just past" it, so we also accept pos == end.
func containsPos(start, end, pos token.Pos) bool {
	return LSPContainsPosition(start.Line, start.Column, end.Line, end.Column, pos.Line, pos.Column)
}

// spanWidth is a rough size metric: bytes from Pos to End. Used to
// break ties when multiple nodes contain a position.
func spanWidth(n ast.Node) int {
	return n.End().Offset - n.Pos().Offset
}

// ---- Wire helpers ----

// unmarshalParams decodes req.Params into `target`, tolerating a
// missing Params field (producing the zero value of target).
func unmarshalParams(req *rpcRequest, target any) error {
	if len(req.Params) == 0 || string(req.Params) == "null" {
		return nil
	}
	return json.Unmarshal(req.Params, target)
}

// replyJSON marshals `v` as the success result of req.ID. On
// marshal failure it logs and sends an internal-error response so
// the client isn't left waiting.
func replyJSON(c *conn, id json.RawMessage, v any) {
	if v == nil {
		_ = c.writeResponse(id, json.RawMessage("null"))
		return
	}
	raw, err := json.Marshal(v)
	if err != nil {
		_ = c.writeError(id, errInternalError, err.Error())
		return
	}
	_ = c.writeResponse(id, raw)
}

// ptrRange is a helper because struct literals can't take the
// address of their own fields in Go without a temporary.
func ptrRange(r Range) *Range { return &r }

// (The full handleCompletion implementation lives in completion.go
// so the scope/member logic stays isolated from the rest of the
// handler surface.)
