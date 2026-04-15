package lsp

import (
	"sort"
	"strings"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/lexer"
	"github.com/osty/osty/internal/resolve"
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
	if offset > len(src) {
		offset = len(src)
	}
	start := offset
	for start > 0 && lexer.IsIdentCont(src[start-1]) {
		start--
	}
	prefix = string(src[start:offset])

	if start > 0 && src[start-1] == '.' {
		recvEnd := start - 1
		recvStart := recvEnd
		for recvStart > 0 && lexer.IsIdentCont(src[recvStart-1]) {
			recvStart--
		}
		afterDot = string(src[recvStart:recvEnd])
	}
	return prefix, afterDot
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
	sort.Slice(items, func(i, j int) bool {
		return items[i].Label < items[j].Label
	})
	return items
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
	sort.Slice(items, func(i, j int) bool {
		return items[i].Label < items[j].Label
	})
	return items
}

// completionItemFromSym maps a resolver Symbol to a user-facing
// CompletionItem. Kind, detail (type signature), and documentation
// (doc comment) are all pulled from check.Result + ast decl nodes.
func completionItemFromSym(label string, sym *resolve.Symbol, r *check.Result) CompletionItem {
	item := CompletionItem{
		Label: label,
		Kind:  completionKindFor(sym.Kind),
	}
	if r != nil {
		if t := r.LookupSymType(sym); t != nil {
			if sym.Kind == resolve.SymFn {
				item.Detail = "fn " + label + fnTypeTail(t)
			} else {
				item.Detail = t.String()
			}
		}
	}
	if doc := symbolDoc(sym); doc != "" {
		item.Documentation = &MarkupContent{
			Kind:  MarkupKindMarkdown,
			Value: doc,
		}
	}
	// Packages sort first in the list because they're the most common
	// "I'm about to type `pkg.`" target. Locals get default order.
	switch sym.Kind {
	case resolve.SymPackage:
		item.SortText = "0_" + label
	case resolve.SymLet, resolve.SymParam:
		item.SortText = "1_" + label
	case resolve.SymFn, resolve.SymVariant:
		item.SortText = "2_" + label
	default:
		item.SortText = "3_" + label
	}
	return item
}

// completionKindFor maps a resolver SymbolKind to the LSP enum.
func completionKindFor(k resolve.SymbolKind) CompletionItemKind {
	switch k {
	case resolve.SymFn:
		return CompletionItemFunction
	case resolve.SymLet:
		return CompletionItemVariable
	case resolve.SymParam:
		return CompletionItemVariable
	case resolve.SymStruct:
		return CompletionItemStruct
	case resolve.SymEnum:
		return CompletionItemEnum
	case resolve.SymInterface:
		return CompletionItemInterface
	case resolve.SymTypeAlias:
		return CompletionItemStruct
	case resolve.SymVariant:
		return CompletionItemEnumMember
	case resolve.SymGeneric:
		return CompletionItemTypeParameter
	case resolve.SymPackage:
		return CompletionItemModule
	case resolve.SymBuiltin:
		return CompletionItemKeyword
	}
	return CompletionItemValue
}
