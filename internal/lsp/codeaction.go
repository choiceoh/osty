package lsp

import (
	"fmt"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
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
//     the resolver's own scope+Levenshtein logic, so the fixes line
//     up with the hints the compiler emitted in the first place.
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

// undefinedNameFixes asks the resolver for names within edit-distance
// 2 of the offending identifier and emits one "rename to X" action
// per candidate. Delegating the distance logic to resolve.Scope keeps
// the LSP suggestions in lockstep with the compiler's own hints — no
// parallel Levenshtein to drift out of sync.
func undefinedNameFixes(doc *document, d LSPDiagnostic) []CodeAction {
	start := doc.analysis.lines.lspToOsty(d.Range.Start)
	name := identifierAt(doc.src, start.Offset)
	if name == "" {
		return nil
	}
	a := doc.analysis
	if a.resolve == nil || a.resolve.FileScope == nil {
		return nil
	}
	candidates := a.resolve.FileScope.NearbyNames(name, 2)
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

// prefixUnderscoreFix produces a "silence by prefixing `_`" action
// for unused bindings / params. The rename scope is exactly the
// diagnostic's range so we don't accidentally touch usages elsewhere.
func prefixUnderscoreFix(doc *document, d LSPDiagnostic) CodeAction {
	start := doc.analysis.lines.lspToOsty(d.Range.Start)
	name := identifierAt(doc.src, start.Offset)
	return CodeAction{
		Title:       selfhost.LSPPrefixUnderscoreTitle(name),
		Kind:        CodeActionQuickFix,
		Diagnostics: []LSPDiagnostic{d},
		Edit: &WorkspaceEdit{
			Changes: map[string][]TextEdit{
				doc.uri: {{Range: d.Range, NewText: selfhost.LSPPrefixUnderscoreName(name)}},
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
	return selfhost.LSPIdentifierAt(src, off)
}
