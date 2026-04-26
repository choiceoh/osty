package lsp

import (
	"strings"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
)

// handleCompletion answers `textDocument/completion`. Behavior splits
// by context:
//
//   - After `.` on a package alias (`fs.⟨cursor⟩`): suggest every
//     `pub` symbol in the target package's PkgScope, scored by name.
//   - After `.` on any other receiver: fall back to a safe, empty
//     list (member dispatch requires type-checker awareness we don't
//     yet surface here; sending nothing is better than a sea of
//     irrelevant global names).
//   - Otherwise: suggest every name visible in the current lexical
//     scope — locals, parameters, top-level decls in this package,
//     builtins, and use aliases.
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

// completionAfterDot resolves `recvName` against the document's file
// scope. When it binds a SymPackage with a loaded PkgScope, we emit
// one item per exported member.
func (s *Server) completionAfterDot(doc *document, recvName, prefix string) []CompletionItem {
	a := doc.analysis
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
	var items []CompletionItem
	for name, member := range pkg.PkgScope.Symbols() {
		if !member.Pub {
			continue
		}
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			continue
		}
		items = append(items, completionItemFromSym(name, member, a.check))
	}
	return sortCompletionItems(items)
}

// completionInScope emits one item per name visible from the scope
// that contains the cursor. Walks up from the file scope through
// every parent (package, prelude) so builtins, use aliases, and
// top-level declarations all appear.
func (s *Server) completionInScope(doc *document, prefix string) []CompletionItem {
	a := doc.analysis
	if a.resolve == nil || a.resolve.FileScope == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var items []CompletionItem
	for sc := a.resolve.FileScope; sc != nil; sc = sc.Parent() {
		for name, sym := range sc.Symbols() {
			if _, dup := seen[name]; dup {
				continue
			}
			if prefix != "" && !strings.HasPrefix(name, prefix) {
				continue
			}
			seen[name] = struct{}{}
			items = append(items, completionItemFromSym(name, sym, a.check))
		}
	}
	return sortCompletionItems(items)
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
	item := CompletionItem{
		Label:    view.Name,
		Kind:     CompletionItemKind(LSPCompletionKindForSymbolKind(view.Kind)),
		SortText: LSPCompletionSortTextForSymbolKind(view.Kind, view.Name),
	}
	if view.TypeText != "" {
		item.Detail = LSPCompletionDetail(view.Kind, view.Name, view.TypeText)
	}
	if view.DocText != "" {
		item.Documentation = &MarkupContent{
			Kind:  MarkupKindMarkdown,
			Value: view.DocText,
		}
	}
	return item
}
