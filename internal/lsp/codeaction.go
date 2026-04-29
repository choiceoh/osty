package lsp

import (
	"fmt"
	"sort"

	"github.com/osty/osty/internal/diag"
)

// handleCodeAction answers `textDocument/codeAction`. It produces two
// families of results:
//
//   - Diagnostic-attached quick fixes: one fix per problem the editor
//     attached to the request (those it has cached for the current
//     cursor range) whose code we know how to patch.
//
//   - Source actions: bulk refactors that operate on the whole file
//     regardless of where the cursor sits. Currently:
//
//   - source.organizeImports — sort, dedupe, and drop unused
//     `use` declarations.
//
//   - source.fixAll[.osty] — apply every machine-applicable
//     compiler/lint suggestion in one edit.
//
// The `context.only` filter the client sends narrows what we return
// — `["source.organizeImports"]` on save yields just that action, an
// empty filter yields everything applicable.
//
// Quick-fix coverage:
//   - E0500 (undefined name): suggest rename-to-nearest-match using
//     selfhost structured package symbols plus the legacy scope fallback
//     for locals/params.
//   - L0001 / L0002 (unused binding / parameter): suggest prefixing
//     the name with `_` to silence the lint.
//   - L0003 (unused import): suggest deleting the whole `use` line.
func (s *Server) handleCodeAction(req *rpcRequest) {
	var params CodeActionParams
	if err := unmarshalParams(req, &params); err != nil {
		_ = s.conn.writeError(req.ID, errInvalidParams, err.Error())
		return
	}
	doc := s.docs.get(params.TextDocument.URI)
	if doc == nil || doc.analysis == nil {
		replyJSON(s.conn, req.ID, []CodeAction{})
		return
	}
	only := params.Context.Only
	var actions []CodeAction
	// Diagnostic-attached quick fixes (kind = quickfix).
	if wantsKind(only, CodeActionQuickFix) {
		for _, d := range params.Context.Diagnostics {
			switch d.Code {
			case diag.CodeUndefinedName:
				actions = append(actions, undefinedNameFixes(doc, d)...)
			case diag.CodeUnusedLet, diag.CodeUnusedParam:
				actions = append(actions, prefixUnderscoreFix(doc, d))
			case diag.CodeUnusedImport:
				actions = append(actions, removeLineFix(doc, d))
			}
		}
	}
	// Source actions (kind = source.*). These are triggered on save
	// or via the command palette, independent of the cursor range.
	if wantsKind(only, CodeActionSourceOrganizeImports) {
		if a := organizeImportsAction(doc); a != nil {
			actions = append(actions, *a)
		}
	}
	if wantsKind(only, CodeActionSourceFixAllOsty) || wantsKind(only, CodeActionSourceFixAll) {
		if a := fixAllAction(doc); a != nil {
			actions = append(actions, *a)
		}
	}
	replyJSON(s.conn, req.ID, actions)
}

// undefinedNameFixes suggests visible names within edit-distance 2 of the
// offending identifier. Selfhost structured symbols provide the package-level
// candidate set first; the Go scope fallback adds locals/params while those
// are still only exposed through compatibility state.
func undefinedNameFixes(doc *document, d LSPDiagnostic) []CodeAction {
	start := doc.analysis.lines.lspToOsty(d.Range.Start)
	name := identifierAt(doc.src, start.Offset)
	if name == "" {
		return nil
	}
	a := doc.analysis
	candidates := nearbyStructuredSymbolNames(a.structuredSymbols, name, 2)
	if a.resolve != nil && a.resolve.FileScope != nil {
		candidates = mergeNearbyNames(candidates, a.resolve.FileScope.NearbyNames(name, 2))
	}
	if len(candidates) == 0 {
		return nil
	}
	var out []CodeAction
	for _, c := range candidates {
		out = append(out, CodeAction{
			Title:       fmt.Sprintf("Rename to `%s`", c),
			Kind:        CodeActionQuickFix,
			Diagnostics: []LSPDiagnostic{d},
			Edit: &WorkspaceEdit{
				Changes: map[string][]TextEdit{
					doc.uri: {{Range: d.Range, NewText: c}},
				},
			},
			IsPreferred: len(candidates) == 1,
		})
	}
	return out
}

func nearbyStructuredSymbolNames(symbols []structuredSymbol, target string, maxDistance int) []string {
	type candidate struct {
		name string
		dist int
	}
	seen := map[string]bool{}
	var candidates []candidate
	for _, sym := range symbols {
		if sym.name == "" || sym.builtin || sym.depth != 0 || seen[sym.name] {
			continue
		}
		dist := lspLevenshteinBounded(target, sym.name, maxDistance)
		if dist > maxDistance {
			continue
		}
		seen[sym.name] = true
		candidates = append(candidates, candidate{name: sym.name, dist: dist})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].dist != candidates[j].dist {
			return candidates[i].dist < candidates[j].dist
		}
		return candidates[i].name < candidates[j].name
	})
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.name)
	}
	return out
}

func mergeNearbyNames(primary []string, fallback []string) []string {
	if len(primary) == 0 {
		return fallback
	}
	seen := make(map[string]bool, len(primary)+len(fallback))
	out := make([]string, 0, len(primary)+len(fallback))
	for _, name := range primary {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, name := range fallback {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func lspLevenshteinBounded(a, b string, limit int) int {
	ar := []rune(a)
	br := []rune(b)
	if absInt(len(ar)-len(br)) > limit {
		return limit + 1
	}
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		rowMin := curr[0]
		for j := 1; j <= len(br); j++ {
			cost := 0
			if ar[i-1] != br[j-1] {
				cost = 1
			}
			curr[j] = minInt(
				prev[j]+1,
				curr[j-1]+1,
				prev[j-1]+cost,
			)
			if curr[j] < rowMin {
				rowMin = curr[j]
			}
		}
		if rowMin > limit {
			return limit + 1
		}
		prev, curr = curr, prev
	}
	if prev[len(br)] > limit {
		return limit + 1
	}
	return prev[len(br)]
}

func minInt(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		return c
	}
	return a
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// prefixUnderscoreFix produces a "silence by prefixing `_`" action
// for unused bindings / params. The rename scope is exactly the
// diagnostic's range so we don't accidentally touch usages elsewhere.
func prefixUnderscoreFix(doc *document, d LSPDiagnostic) CodeAction {
	start := doc.analysis.lines.lspToOsty(d.Range.Start)
	name := identifierAt(doc.src, start.Offset)
	return CodeAction{
		Title:       LSPPrefixUnderscoreTitle(name),
		Kind:        CodeActionQuickFix,
		Diagnostics: []LSPDiagnostic{d},
		Edit: &WorkspaceEdit{
			Changes: map[string][]TextEdit{
				doc.uri: {{Range: d.Range, NewText: LSPPrefixUnderscoreName(name)}},
			},
		},
		IsPreferred: true,
	}
}

// removeLineFix produces a "delete this declaration" action. Uses the
// diagnostic's primary range expanded to the enclosing line(s).
func removeLineFix(doc *document, d LSPDiagnostic) CodeAction {
	// Expand start to beginning-of-line and end to one past the
	// newline so the deletion doesn't leave a blank line behind.
	rng := Range{
		Start: Position{Line: d.Range.Start.Line, Character: 0},
		End:   Position{Line: d.Range.End.Line + 1, Character: 0},
	}
	return CodeAction{
		Title:       "Remove unused import",
		Kind:        CodeActionQuickFix,
		Diagnostics: []LSPDiagnostic{d},
		Edit: &WorkspaceEdit{
			Changes: map[string][]TextEdit{
				doc.uri: {{Range: rng, NewText: ""}},
			},
		},
		IsPreferred: true,
	}
}

// identifierAt reads an identifier starting at byte offset `off`.
// Returns "" when the cursor isn't on an ident — common with
// synthesized diagnostics that don't have real source text.
func identifierAt(src []byte, off int) string {
	return LSPIdentifierAt(src, off)
}
