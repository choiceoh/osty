package lsp

import (
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
)

// handleWorkspaceSymbol answers `workspace/symbol`. The search scope
// is the entire workspace (root + every sibling package with `.osty`
// files), not just files the user has opened — so typing a query
// surfaces matches the user hasn't touched yet in this session.
//
// The index is lazily built on the first query via
// ensureWorkspaceIndex and invalidated by any didOpen / didChange /
// didClose so results stay fresh without a dedicated watcher.
// Falls back to open-document-only scan when the files aren't on
// disk (scratch buffers with `inmemory:` or similar URIs).
func (s *Server) handleWorkspaceSymbol(req *rpcRequest) {
	var params WorkspaceSymbolParams
	if err := unmarshalParams(req, &params); err != nil {
		_ = s.conn.writeError(req.ID, errInvalidParams, err.Error())
		return
	}
	query := strings.ToLower(params.Query)

	var out []SymbolInformation
	seen := map[string]bool{}

	// Global path: scan the workspace index.
	if root := s.workspaceRootForAny(); root != "" {
		for _, pkg := range s.ensureWorkspaceIndex(root) {
			for _, pf := range pkg.Files {
				if seen[pf.Path] {
					continue
				}
				seen[pf.Path] = true
				uri := pathToURI(pf.Path)
				li := newLineIndex(pf.Source)
				out = append(out, collectDeclSymbols(uri, li, pf.File, pkg.Name, query)...)
			}
		}
	}

	// Cover open documents that the workspace index didn't reach:
	// scratch buffers (non-file:// URIs) and open files whose on-disk
	// location wasn't part of any loaded package (typical for a
	// single-file session outside a package).
	s.docs.mu.Lock()
	for _, doc := range s.docs.m {
		if doc.analysis == nil {
			continue
		}
		if path, ok := fileURIPath(doc.uri); ok && seen[path] {
			continue
		}
		out = append(out, collectFileSymbols(doc.uri, doc.analysis, query)...)
	}
	s.docs.mu.Unlock()

	out = sortSymbolInformation(out)
	replyJSON(s.conn, req.ID, out)
}

func sortSymbolInformation(in []SymbolInformation) []SymbolInformation {
	if len(in) <= 1 {
		return in
	}
	keys := make([]selfhost.LSPSymbolSortKey, 0, len(in))
	for _, sym := range in {
		keys = append(keys, selfhost.LSPSymbolSortKey{
			Name: sym.Name,
			URI:  sym.Location.URI,
		})
	}
	indexes := selfhost.SortLSPSymbolIndexes(keys)
	out := make([]SymbolInformation, 0, len(indexes))
	for _, idx := range indexes {
		if idx < 0 || idx >= len(in) {
			continue
		}
		out = append(out, in[idx])
	}
	return out
}

// collectFileSymbols enumerates top-level decls from a single-file
// analysis (no package context).
func collectFileSymbols(uri string, a *docAnalysis, query string) []SymbolInformation {
	if a.file == nil {
		return nil
	}
	return collectDeclSymbols(uri, a.lines, a.file, "", query)
}

// collectDeclSymbols turns every top-level declaration in `file`
// into a SymbolInformation, filtered by substring match on the
// lowercase `query`. Struct fields, enum variants, and methods are
// emitted as separate entries with the containing decl's name as
// `ContainerName` so the client can render "Struct.method" groupings.
func collectDeclSymbols(uri string, li *lineIndex, file *ast.File, container, query string) []SymbolInformation {
	if file == nil {
		return nil
	}
	var out []SymbolInformation
	add := func(name, containerName string, kind SymbolKind, n ast.Node) {
		if name == "" {
			return
		}
		if query != "" && !strings.Contains(strings.ToLower(name), query) {
			return
		}
		out = append(out, SymbolInformation{
			Name: name,
			Kind: kind,
			Location: Location{
				URI:   uri,
				Range: li.ostyRange(diag.Span{Start: n.Pos(), End: n.End()}),
			},
			ContainerName: containerName,
		})
	}
	for _, e := range walkFileDecls(file) {
		entryContainer := container
		if e.container != "" {
			entryContainer = e.container
		}
		add(e.name, entryContainer, e.kind, e.node)
		for _, c := range e.nested {
			add(c.name, c.container, c.kind, c.node)
		}
	}
	return out
}

// declEntry is a normalized view of one declaration plus its nested
// members. walkFileDecls returns these so the document-outline and
// workspace-symbol handlers share one source of truth about what
// counts as a top-level declaration and which SymbolKind fits.
type declEntry struct {
	name      string
	container string
	kind      SymbolKind
	node      ast.Node
	// nested carries struct fields, enum variants, and methods.
	// Empty for flat decls (FnDecl, TypeAliasDecl, LetDecl).
	nested []declEntry
}

// walkFileDecls yields one declEntry per top-level declaration in
// `file`. Struct/enum/interface decls attach their members via the
// nested slice so callers choose whether to render them as a tree
// (DocumentSymbol) or a flattened list (SymbolInformation).
func walkFileDecls(file *ast.File) []declEntry {
	if file == nil {
		return nil
	}
	var out []declEntry
	for _, d := range file.Decls {
		if e, ok := declEntryFor(d); ok {
			out = append(out, e)
		}
	}
	return out
}

// declEntryFor maps one parser Decl node to a declEntry. New
// top-level declaration kinds must be added here so both the outline
// and workspace-search handlers pick them up.
func declEntryFor(d ast.Decl) (declEntry, bool) {
	switch n := d.(type) {
	case *ast.FnDecl:
		return declEntry{name: n.Name, kind: lspSymbolKindForDecl("fn", false), node: n}, true
	case *ast.StructDecl:
		nested := make([]declEntry, 0, len(n.Fields)+len(n.Methods))
		for _, f := range n.Fields {
			nested = append(nested, declEntry{name: f.Name, container: n.Name, kind: lspSymbolKindForMember("field"), node: f})
		}
		for _, m := range n.Methods {
			nested = append(nested, declEntry{name: m.Name, container: n.Name, kind: lspSymbolKindForMember("method"), node: m})
		}
		return declEntry{name: n.Name, kind: lspSymbolKindForDecl("struct", false), node: n, nested: nested}, true
	case *ast.EnumDecl:
		nested := make([]declEntry, 0, len(n.Variants)+len(n.Methods))
		for _, v := range n.Variants {
			nested = append(nested, declEntry{name: v.Name, container: n.Name, kind: lspSymbolKindForMember("variant"), node: v})
		}
		for _, m := range n.Methods {
			nested = append(nested, declEntry{name: m.Name, container: n.Name, kind: lspSymbolKindForMember("method"), node: m})
		}
		return declEntry{name: n.Name, kind: lspSymbolKindForDecl("enum", false), node: n, nested: nested}, true
	case *ast.InterfaceDecl:
		nested := make([]declEntry, 0, len(n.Methods))
		for _, m := range n.Methods {
			nested = append(nested, declEntry{name: m.Name, container: n.Name, kind: lspSymbolKindForMember("method"), node: m})
		}
		return declEntry{name: n.Name, kind: lspSymbolKindForDecl("interface", false), node: n, nested: nested}, true
	case *ast.TypeAliasDecl:
		return declEntry{name: n.Name, kind: lspSymbolKindForDecl("typeAlias", false), node: n}, true
	case *ast.LetDecl:
		return declEntry{name: n.Name, kind: lspSymbolKindForDecl("let", n.Mut), node: n}, true
	}
	return declEntry{}, false
}

func lspSymbolKindForDecl(kind string, mutable bool) SymbolKind {
	return SymbolKind(selfhost.LSPSymbolKindForDecl(kind, mutable))
}

func lspSymbolKindForMember(kind string) SymbolKind {
	return SymbolKind(selfhost.LSPSymbolKindForMember(kind))
}
