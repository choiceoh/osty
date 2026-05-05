package lsp

import (
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
)

// handleCompletion answers `textDocument/completion`. Behavior splits
// by context:
//
//   - After `.` on a package alias (`fs.⟨cursor⟩`): suggest every
//     exported symbol from the selfhost import surface, with the
//     PkgScope path kept only as compatibility fallback.
//   - After `.` on any other receiver: fall back to a safe, empty
//     list (member dispatch requires type-checker awareness we don't
//     yet surface here; sending nothing is better than a sea of
//     irrelevant global names).
//   - Otherwise: suggest selfhost structured package symbols first,
//     then fill in locals, parameters, and builtins from the legacy
//     lexical scope while those are still compatibility-only.
//
// The response sets IsIncomplete=false so VS Code doesn't thrash the
// server on every keystroke; the list is deterministic across runs
// because we sort by label before sending.
func (s *Server) handleCompletion(req *rpcRequest) {
	var params CompletionParams
	if err := unmarshalParams(req, &params); err != nil {
		_ = s.conn.writeError(req.ID, errInvalidParams, err.Error())
		return
	}
	doc := s.docs.get(params.TextDocument.URI)
	if doc == nil || doc.analysis == nil {
		replyJSON(s.conn, req.ID, &CompletionList{Items: []CompletionItem{}})
		return
	}

	pos := doc.analysis.lines.lspToOsty(params.Position)
	prefix, afterDot := precedingContext(doc.src, pos.Offset)

	if afterDot != "" {
		items := s.completionAfterDot(doc, afterDot, prefix)
		replyJSON(s.conn, req.ID, &CompletionList{Items: items})
		return
	}

	items := s.completionInScope(doc, prefix)
	replyJSON(s.conn, req.ID, &CompletionList{Items: items})
}

// precedingContext inspects the bytes just before the cursor. It
// returns:
//
//   - `prefix`: the partial identifier being typed (may be empty).
//   - `afterDot`: when the cursor sits immediately after `ident.`,
//     the receiver identifier; "" when we are not in a dot-access
//     position.
//
// Purely lexical — no string/comment awareness. Callers that want
// to suppress completion inside literals should gate on the parsed
// AST before invoking this.
func precedingContext(src []byte, offset int) (prefix, afterDot string) {
	ctx := LSPPrecedingCompletionContext(src, offset)
	return ctx.Prefix, ctx.AfterDot
}

// completionAfterDot resolves `recvName` against the selfhost import
// surfaces captured in analysis. The legacy file-scope path remains as a
// compatibility fallback while downstream consumers migrate off Scope.
func (s *Server) completionAfterDot(doc *document, recvName, prefix string) []CompletionItem {
	a := doc.analysis
	if items, ok := completionAfterStructuredImport(a, recvName, prefix); ok {
		return items
	}
	if a.resolve == nil || a.resolve.FileScope == nil {
		return nil
	}
	sym := a.resolve.FileScope.Lookup(recvName)
	if sym == nil || sym.Kind != resolve.SymPackage {
		// Instance member access requires type information propagation
		// we haven't wired through to the LSP surface yet. Empty list
		// is the safe default; the client keeps its own word-completion
		// fallback.
		return nil
	}
	pkg := sym.Package
	if pkg == nil || pkg.PkgScope == nil {
		return nil
	}
	candidates := make([]LSPCompletionCandidateView, 0, len(pkg.PkgScope.Symbols()))
	for name, member := range pkg.PkgScope.Symbols() {
		view := completionSymbolView(name, member, a.check)
		candidates = append(candidates, LSPCompletionCandidateView{
			Name:     view.Name,
			Kind:     view.Kind,
			TypeText: view.TypeText,
			DocText:  view.DocText,
			Include:  member.Pub,
		})
	}
	return completionItemsFromPolicy(LSPCompletionItemsForCandidates(candidates, prefix))
}

func completionAfterStructuredImport(a *docAnalysis, recvName, prefix string) ([]CompletionItem, bool) {
	if a == nil {
		return nil, false
	}
	for _, surface := range a.structuredImports {
		if surface.alias != recvName {
			continue
		}
		candidates := make([]LSPCompletionCandidateView, 0, len(surface.symbols))
		for _, sym := range surface.symbols {
			candidates = append(candidates, LSPCompletionCandidateView{
				Name:     sym.name,
				Kind:     sym.kind,
				TypeText: sym.typeText,
				Include:  true,
			})
		}
		return completionItemsFromPolicy(LSPCompletionItemsForCandidates(candidates, prefix)), true
	}
	return nil, false
}

// completionInScope emits one item per visible name. Package-level symbols
// come from selfhost structured facts; the scope walk remains only to supply
// locals, parameters, and builtins not yet exposed in those facts.
func (s *Server) completionInScope(doc *document, prefix string) []CompletionItem {
	a := doc.analysis
	var candidates []LSPCompletionCandidateView
	for _, sym := range a.structuredSymbols {
		candidates = append(candidates, LSPCompletionCandidateView{
			Name:     sym.name,
			Kind:     sym.kind,
			TypeText: sym.typeText,
			Include:  !sym.builtin && sym.depth == 0,
		})
	}
	if a.resolve == nil || a.resolve.FileScope == nil {
		return completionItemsFromPolicy(LSPCompletionItemsForCandidates(candidates, prefix))
	}
	for sc := a.resolve.FileScope; sc != nil; sc = sc.Parent() {
		for name, sym := range sc.Symbols() {
			view := completionSymbolView(name, sym, a.check)
			candidates = append(candidates, LSPCompletionCandidateView{
				Name:     view.Name,
				Kind:     view.Kind,
				TypeText: view.TypeText,
				DocText:  view.DocText,
				Include:  true,
			})
		}
	}
	return completionItemsFromPolicy(LSPCompletionItemsForCandidates(candidates, prefix))
}

func sortCompletionItems(in []CompletionItem) []CompletionItem {
	if len(in) <= 1 {
		return in
	}
	labels := make([]string, 0, len(in))
	for _, item := range in {
		labels = append(labels, item.Label)
	}
	indexes := SortLSPCompletionIndexes(labels)
	out := make([]CompletionItem, 0, len(indexes))
	for _, idx := range indexes {
		if idx < 0 || idx >= len(in) {
			continue
		}
		out = append(out, in[idx])
	}
	return out
}

// completionItemFromSym maps a resolver Symbol to a user-facing
// CompletionItem. The pointer-typed extraction lives in
// completionSymbolView; the assembly itself runs off the value-typed
// LSPSymbolView so the policy is portable.
func completionItemFromSym(label string, sym *resolve.Symbol, r *check.Result) CompletionItem {
	return completionItemFromView(completionSymbolView(label, sym, r))
}

func completionItemFromStructuredSymbol(sym structuredSymbol) CompletionItem {
	return completionItemFromView(selfhost.LSPSymbolView{
		Name:     sym.name,
		Kind:     sym.kind,
		TypeText: sym.typeText,
		HasSym:   true,
	})
}

func completionItemFromStructuredImportSymbol(sym structuredImportSymbol) CompletionItem {
	return completionItemFromView(selfhost.LSPSymbolView{
		Name:     sym.name,
		Kind:     sym.kind,
		TypeText: sym.typeText,
		HasSym:   true,
	})
}

// completionSymbolView projects a resolver Symbol into the
// value-typed view consumed by completionItemFromView.
func completionSymbolView(label string, sym *resolve.Symbol, r *check.Result) selfhost.LSPSymbolView {
	typeText := ""
	if r != nil {
		if t := r.LookupSymType(sym); t != nil {
			typeText = t.String()
		}
	}
	return selfhost.LSPSymbolView{
		Name:     label,
		Kind:     sym.Kind.String(),
		TypeText: typeText,
		DocText:  symbolDoc(sym),
		HasSym:   true,
	}
}

// completionItemFromView assembles a CompletionItem from a
// pre-extracted view. Kind/Detail/SortText delegate to the
// self-hosted policy; the markdown documentation flows through as a
// raw doc string.
func completionItemFromView(view selfhost.LSPSymbolView) CompletionItem {
	policy := LSPCompletionItemForSymbolView(view)
	return completionItemFromData(policy)
}

func completionItemsFromPolicy(items []LSPCompletionItemData) []CompletionItem {
	out := make([]CompletionItem, 0, len(items))
	for _, item := range items {
		out = append(out, completionItemFromData(item))
	}
	return out
}

func completionItemFromData(policy LSPCompletionItemData) CompletionItem {
	item := CompletionItem{
		Label:    policy.Label,
		Kind:     CompletionItemKind(policy.Kind),
		SortText: policy.SortText,
	}
	if policy.Detail != "" {
		item.Detail = policy.Detail
	}
	if policy.Documentation != "" {
		item.Documentation = &MarkupContent{
			Kind:  MarkupKindMarkdown,
			Value: policy.Documentation,
		}
	}
	return item
}
