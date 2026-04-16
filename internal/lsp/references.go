package lsp

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
)

// handleReferences answers `textDocument/references`. We locate the
// symbol under the cursor, then walk every loaded package's Refs /
// TypeRefs to find matching identifiers. Include the declaration
// itself when the client asked for it (the standard IDE default).
//
// Scope of the search follows whatever analysis mode the document is
// in: single-file buffers only scan their own refs, package/workspace
// docs scan every loaded file.
func (s *Server) handleReferences(req *rpcRequest) {
	var params ReferenceParams
	if err := unmarshalParams(req, &params); err != nil {
		_ = s.conn.writeError(req.ID, errInvalidParams, err.Error())
		return
	}
	doc := s.docs.get(params.TextDocument.URI)
	if doc == nil || doc.analysis == nil {
		replyJSON(s.conn, req.ID, []Location{})
		return
	}
	target := s.targetSymbolAt(doc, params.Position)
	if target == nil {
		replyJSON(s.conn, req.ID, []Location{})
		return
	}
	locs := s.findReferences(doc, target, params.Context.IncludeDeclaration)
	replyJSON(s.conn, req.ID, locs)
}

// handleRename answers `textDocument/rename`. Same symbol search as
// references; the payload shape swaps `[]Location` for a
// WorkspaceEdit whose `changes` map carries one TextEdit per
// reference. Declaration is always renamed regardless of any
// includeDeclaration flag — renaming a binding without touching its
// declaration would produce broken code.
func (s *Server) handleRename(req *rpcRequest) {
	var params RenameParams
	if err := unmarshalParams(req, &params); err != nil {
		_ = s.conn.writeError(req.ID, errInvalidParams, err.Error())
		return
	}
	if params.NewName == "" {
		_ = s.conn.writeError(req.ID, errInvalidParams, "new name is empty")
		return
	}
	doc := s.docs.get(params.TextDocument.URI)
	if doc == nil || doc.analysis == nil {
		replyJSON(s.conn, req.ID, nil)
		return
	}
	target := s.targetSymbolAt(doc, params.Position)
	if target == nil {
		replyJSON(s.conn, req.ID, nil)
		return
	}
	if target.Kind == resolve.SymBuiltin {
		_ = s.conn.writeError(req.ID, errInvalidRequest, "cannot rename a builtin")
		return
	}
	edits := s.renameEditsFor(doc, target, params.NewName)
	replyJSON(s.conn, req.ID, &WorkspaceEdit{Changes: edits})
}

// targetSymbolAt resolves the identifier under an LSP Position to the
// resolver symbol it points at, using whichever findIdentAt /
// findNamedTypeAt matches the cursor.
func (s *Server) targetSymbolAt(doc *document, lspPos Position) *resolve.Symbol {
	pos := doc.analysis.lines.lspToOsty(lspPos)
	if _, sym := findIdentAt(doc.analysis, pos); sym != nil {
		return sym
	}
	if _, sym := findNamedTypeAt(doc.analysis, pos); sym != nil {
		return sym
	}
	return nil
}

// findReferences walks every loaded package and returns Locations
// for every Ident / NamedType-head whose resolver symbol is
// `target`. Usages are narrowed to the exact name span so rename
// edits don't sweep punctuation; for a qualified NamedType like
// `auth.User`, the head `auth` is the reference and the range stops
// at the first segment.
//
// When the analysis has no packages attached (single-file mode),
// the doc's own Refs/TypeRefs are the search space. Duplicates
// (same URI + same start position) are collapsed before return.
func (s *Server) findReferences(doc *document, target *resolve.Symbol, includeDecl bool) []Location {
	var out []Location
	visit := func(uri string, src []byte, li *lineIndex, refs map[*ast.Ident]*resolve.Symbol, typeRefs map[*ast.NamedType]*resolve.Symbol) {
		for id, sym := range refs {
			if sym != target {
				continue
			}
			out = append(out, Location{
				URI:   uri,
				Range: li.ostyRange(diag.Span{Start: id.Pos(), End: id.End()}),
			})
		}
		for nt, sym := range typeRefs {
			if sym != target {
				continue
			}
			// `auth.User` is a single NamedType whose head reference
			// is `auth`. Narrow to the head identifier so the rename
			// doesn't chew through the whole path.
			startOff := nt.Pos().Offset
			endOff := startOff + len(target.Name)
			if len(nt.Path) > 0 && nt.Path[0] == target.Name {
				endOff = startOff + len(nt.Path[0])
			}
			if endOff > len(src) {
				endOff = len(src)
			}
			out = append(out, Location{
				URI:   uri,
				Range: li.rangeFromOffsets(startOff, endOff),
			})
		}
	}

	if len(doc.analysis.packages) == 0 {
		visit(doc.uri, doc.src, doc.analysis.lines, doc.analysis.resolve.Refs, doc.analysis.resolve.TypeRefs)
	} else {
		for _, pkg := range doc.analysis.packages {
			for _, pf := range pkg.Files {
				uri := pathToURI(pf.Path)
				li := newLineIndex(pf.Source)
				visit(uri, pf.Source, li, pf.Refs, pf.TypeRefs)
			}
		}
	}

	if includeDecl && target.Decl != nil {
		if loc, ok := s.declLocation(doc, target); ok {
			out = append(out, loc)
		}
	}

	return sortDedupLocations(out)
}

// sortDedupLocations orders locations by URI/start position, then drops exact
// duplicates using the self-hosted reference result policy.
// Two things can produce duplicates: (a) declaration-include adding
// an entry that also appears in Refs for symbols whose decl site is
// itself a reference (enum variants bound via their type body); (b)
// parser fragments where an ident is recorded in both Refs and TypeRefs.
func sortDedupLocations(locs []Location) []Location {
	converted := make([]selfhost.LSPLocation, 0, len(locs))
	for _, loc := range locs {
		converted = append(converted, selfhost.LSPLocation{
			URI:            loc.URI,
			StartLine:      loc.Range.Start.Line,
			StartCharacter: loc.Range.Start.Character,
			EndLine:        loc.Range.End.Line,
			EndCharacter:   loc.Range.End.Character,
		})
	}
	resolved := selfhost.SortDedupLSPLocations(converted)
	out := make([]Location, 0, len(resolved))
	for _, loc := range resolved {
		out = append(out, Location{
			URI: loc.URI,
			Range: Range{
				Start: Position{Line: loc.StartLine, Character: loc.StartCharacter},
				End:   Position{Line: loc.EndLine, Character: loc.EndCharacter},
			},
		})
	}
	return out
}

// renameEditsFor groups the target symbol's usages by URI and emits
// one TextEdit per reference, plus the declaration. Returns a map
// suitable for WorkspaceEdit.Changes.
//
// findReferences already sorts globally by (URI, line, column), so
// per-URI partitions preserve that order without an extra sort pass.
func (s *Server) renameEditsFor(doc *document, target *resolve.Symbol, newName string) map[string][]TextEdit {
	locs := s.findReferences(doc, target, true)
	out := map[string][]TextEdit{}
	for _, loc := range locs {
		out[loc.URI] = append(out[loc.URI], TextEdit{
			Range:   loc.Range,
			NewText: newName,
		})
	}
	return out
}

// declLocation converts a resolver Symbol's declaration site into an
// LSP Location pointing at just the name (not the whole decl).
// Returns ok=false for symbols without a source decl (builtins,
// prelude entries) or when the lexer can't find the name token.
func (s *Server) declLocation(doc *document, sym *resolve.Symbol) (Location, bool) {
	if sym == nil || sym.Decl == nil || sym.Pos.Line == 0 {
		return Location{}, false
	}
	// Prefer the package file that actually contains this decl so
	// the returned URI points to the right source file, not the
	// currently-open document.
	if len(doc.analysis.packages) > 0 {
		for _, pkg := range doc.analysis.packages {
			for _, pf := range pkg.Files {
				if containsNode(pf.File, sym.Decl) {
					li := newLineIndex(pf.Source)
					if s, e, ok := nameRangeForSymbol(pf.Source, sym); ok {
						return Location{
							URI:   pathToURI(pf.Path),
							Range: li.rangeFromOffsets(s, e),
						}, true
					}
					// Fallback: full decl span if name search failed.
					return Location{
						URI:   pathToURI(pf.Path),
						Range: li.ostyRange(diag.Span{Start: sym.Decl.Pos(), End: sym.Decl.End()}),
					}, true
				}
			}
		}
	}
	if s, e, ok := nameRangeForSymbol(doc.src, sym); ok {
		return Location{
			URI:   doc.uri,
			Range: doc.analysis.lines.rangeFromOffsets(s, e),
		}, true
	}
	return Location{
		URI:   doc.uri,
		Range: doc.analysis.lines.ostyRange(diag.Span{Start: sym.Decl.Pos(), End: sym.Decl.End()}),
	}, true
}

// containsNode reports whether `decl` was parsed from `file`. Checks
// by pointer equality against every top-level declaration in the
// file, plus common nested declaration sites (methods on type bodies).
// Good enough for rename/reference locations because resolver symbols
// only ever point at top-level-attached declarations.
func containsNode(file *ast.File, decl ast.Node) bool {
	if file == nil || decl == nil {
		return false
	}
	for _, d := range file.Decls {
		if ast.Node(d) == decl {
			return true
		}
		// Descend one level to cover methods/fields/variants.
		switch n := d.(type) {
		case *ast.StructDecl:
			for _, f := range n.Fields {
				if ast.Node(f) == decl {
					return true
				}
			}
			for _, m := range n.Methods {
				if ast.Node(m) == decl {
					return true
				}
			}
		case *ast.EnumDecl:
			for _, v := range n.Variants {
				if ast.Node(v) == decl {
					return true
				}
			}
			for _, m := range n.Methods {
				if ast.Node(m) == decl {
					return true
				}
			}
		case *ast.InterfaceDecl:
			for _, m := range n.Methods {
				if ast.Node(m) == decl {
					return true
				}
			}
		}
	}
	return false
}
