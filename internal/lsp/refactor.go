package lsp

import (
	"bytes"
	"strings"

	"github.com/osty/osty/internal/airepair"
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/canonical"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/selfhost"
)

// LSP code-action kind strings (LSP 3.17 §codeActionKind). Only the
// kinds the server emits are listed.
const (
	// Source-action kinds are surfaced in editors as bulk operations
	// on the entire file (e.g. "Organize Imports", "Fix All"); they
	// are not attached to a specific diagnostic.
	CodeActionSource                = "source"
	CodeActionSourceOrganizeImports = "source.organizeImports"
	CodeActionSourceFixAll          = "source.fixAll"
	CodeActionSourceFixAllOsty      = "source.fixAll.osty"
)

type keyedUse struct {
	view  selfhost.LSPUseDeclView
	group int
	key   string
	text  string
}

// wantsKind reports whether the client's `context.only` filter (if
// any) admits the given action kind. An empty filter means "everything
// is admissible". A filter entry is treated as a prefix match so that
// a client asking for `"source"` gets every `source.*` subtype back —
// this matches how LSP 3.17 defines `CodeActionKind` inheritance.
func wantsKind(only []string, kind string) bool {
	return LSPWantsCodeActionKind(only, kind)
}

// organizeImportsAction builds a `source.organizeImports` action when
// one would actually change the document. The action:
//
//   - Removes `use` declarations that are never referenced (modulo
//     side-effect imports prefixed with `_`).
//   - Drops duplicates whose effective alias + target path coincide
//     — two `use foo` entries collapse to one.
//   - Groups into: stdlib (`std.*`), external, FFI — matching
//     `osty fmt`'s ordering rules — and sorts alphabetically within
//     each group by raw path.
//
// FFI blocks are treated as opaque: we still
// remove them if unused but don't attempt to rewrite their bodies.
//
// Returns nil if no changes would be made so the action isn't offered
// when the file is already clean.
func organizeImportsAction(doc *document) *CodeAction {
	a := doc.analysis
	if a == nil || a.file == nil || len(a.file.Uses) == 0 {
		return nil
	}
	views := useDeclViews(a.file.Uses)
	if len(views) == 0 {
		return nil
	}
	// Bail out if any meaningful trivia sits between consecutive use
	// decls — line comments, doc comments, or annotations that the
	// AST doesn't attach to the UseDecl itself. Rewriting the block
	// would silently relocate or delete those bytes. The user can
	// still delete unused imports via the per-diagnostic quick fix,
	// so organizing is just unavailable, not broken.
	if hasTriviaBetweenUseViews(doc.src, views) {
		return nil
	}
	unused := unusedUseOffsets(doc)

	kept := make([]keyedUse, 0, len(views))
	seen := make(map[string]bool, len(views))
	for _, v := range views {
		if unused[v.PosOffset] {
			continue
		}
		text := LSPUseSourceText(doc.src, v.PosOffset, v.EndOffset)
		if text == "" {
			continue
		}
		group := LSPUseGroup(v.IsFFI, v.Path)
		key := LSPUseKey(v.IsFFI, v.FFIPath, v.RawPath, v.Path)
		// Dedup by (group, key, alias) — two identical `use` lines
		// collapse to one.
		dedupKey := LSPKeyWithAlias(group, key, v.Alias)
		if seen[dedupKey] {
			continue
		}
		seen[dedupKey] = true
		kept = append(kept, keyedUse{view: v, group: group, key: key, text: text})
	}
	kept = sortImportEntries(kept)

	// Build the replacement block. Separate groups with a blank line so
	// the output matches what `osty fmt` would emit after the reorder.
	var b strings.Builder
	prevGroup := -1
	for i, k := range kept {
		if i > 0 && k.group != prevGroup {
			b.WriteByte('\n')
		}
		b.WriteString(k.text)
		b.WriteByte('\n')
		prevGroup = k.group
	}
	newText := b.String()

	// Replacement range covers the full `use` block: from the first
	// use's start to just past the last use's terminating newline. We
	// must NOT swallow a blank line that belongs to the subsequent
	// decl, so we stop at the newline of the last use's line.
	startOff := views[0].PosOffset
	endOff := LSPEndOfLineOffset(doc.src, views[len(views)-1].EndOffset)
	oldText := string(doc.src[startOff:endOff])
	if newText == oldText {
		return nil
	}

	rng := doc.analysis.lines.rangeFromOffsets(startOff, endOff)
	return &CodeAction{
		Title: "Organize imports",
		Kind:  CodeActionSourceOrganizeImports,
		Edit: &WorkspaceEdit{
			Changes: map[string][]TextEdit{
				doc.uri: {{Range: rng, NewText: newText}},
			},
		},
	}
}

// useDeclViews projects a slice of *ast.UseDecl into value-typed
// views, dropping nil entries so downstream code never has to nil-check.
// This is the only function that touches *ast.UseDecl pointers in
// the organize-imports path.
func useDeclViews(uses []*ast.UseDecl) []selfhost.LSPUseDeclView {
	out := make([]selfhost.LSPUseDeclView, 0, len(uses))
	for _, u := range uses {
		if u == nil {
			continue
		}
		out = append(out, selfhost.LSPUseDeclView{
			PosOffset: u.PosV.Offset,
			EndOffset: u.EndV.Offset,
			Path:      u.Path,
			RawPath:   u.RawPath,
			Alias:     u.Alias,
			IsFFI:     u.IsFFI(),
			FFIPath:   u.FFIPath(),
		})
	}
	return out
}

func sortImportEntries(in []keyedUse) []keyedUse {
	if len(in) <= 1 {
		return in
	}
	keys := make([]LSPImportSortKey, 0, len(in))
	for _, item := range in {
		keys = append(keys, LSPImportSortKey{
			Group: item.group,
			Key:   item.key,
			Alias: item.view.Alias,
		})
	}
	indexes := SortLSPImportIndexes(keys)
	out := make([]keyedUse, 0, len(indexes))
	for _, idx := range indexes {
		if idx < 0 || idx >= len(in) {
			continue
		}
		out = append(out, in[idx])
	}
	return out
}

// hasTriviaBetweenUseViews reports whether non-whitespace bytes
// appear between the end of one UseDecl's line and the start of the
// next. Osty's parser strips comments before handing the token
// stream to the AST builder, so a `// ...` line between two `use`
// decls has no AST node — it only shows up when we scan the raw
// source. Finding ANY non-whitespace / non-`use` byte in that gap
// tells us there's trivia we shouldn't stomp.
func hasTriviaBetweenUseViews(src []byte, views []selfhost.LSPUseDeclView) bool {
	for i := 0; i+1 < len(views); i++ {
		gapStart := views[i].EndOffset
		gapEnd := views[i+1].PosOffset
		if gapStart < 0 || gapEnd > len(src) || gapStart >= gapEnd {
			continue
		}
		if LSPHasTriviaBetweenOffsets(src, gapStart, gapEnd) {
			return true
		}
	}
	return false
}

// unusedUseOffsets returns the set of UseDecl source offsets flagged
// L0003 ("unused import") by the analysis pipeline's lint pass. Keyed
// on the decl's PosV.Offset so callers can match against an
// LSPUseDeclView without holding the original AST pointer.
func unusedUseOffsets(doc *document) map[int]bool {
	out := map[int]bool{}
	a := doc.analysis
	if a == nil || a.lint == nil {
		return out
	}
	for _, d := range a.lint.Diags {
		if d.Code != diag.CodeUnusedImport {
			continue
		}
		for _, sp := range d.Spans {
			out[sp.Span.Start.Offset] = true
		}
	}
	return out
}

// ---- Fix All ----

// fixAllAction collects every machine-applicable suggestion the
// analysis pipeline produced for this file and folds them into a
// single WorkspaceEdit. When airepair can adapt foreign syntax into
// valid Osty, fix-all offers that full-document rewrite as well. The
// action is offered only when at least one applicable fix exists so
// the lightbulb doesn't light up on clean files.
//
// Resolver / checker diagnostics that carry suggestions are pulled
// in as well; those already feed the per-diagnostic quick fixes, but
// fix-all rolls them into one bulk edit for a single-click cleanup.
func fixAllAction(doc *document) *CodeAction {
	a := doc.analysis
	if a == nil || a.file == nil {
		return nil
	}
	// Parser canonicalization handles syntax-only rewrites such as
	// `len(x)` -> `x.len()` and `append(x, y)` -> `x.push(y)`, but it
	// deliberately does not rewrite semantic JS habits like `.length`.
	// When that property is present, prefer airepair's broader repair so
	// fix-all doesn't stop after a partial canonicalization.
	if bytes.Contains(doc.src, []byte(".length")) {
		if edit := airepairFixAllEdit(doc); edit != nil {
			return &CodeAction{
				Title: "Fix all auto-fixable problems",
				Kind:  CodeActionSourceFixAllOsty,
				Edit: &WorkspaceEdit{
					Changes: map[string][]TextEdit{
						doc.uri: []TextEdit{*edit},
					},
				},
			}
		}
	}
	if edit := parserCanonicalFixAllEdit(doc); edit != nil {
		return &CodeAction{
			Title: "Fix all auto-fixable problems",
			Kind:  CodeActionSourceFixAllOsty,
			Edit: &WorkspaceEdit{
				Changes: map[string][]TextEdit{
					doc.uri: []TextEdit{*edit},
				},
			},
		}
	}
	if edit := airepairFixAllEdit(doc); edit != nil {
		return &CodeAction{
			Title: "Fix all auto-fixable problems",
			Kind:  CodeActionSourceFixAllOsty,
			Edit: &WorkspaceEdit{
				Changes: map[string][]TextEdit{
					doc.uri: []TextEdit{*edit},
				},
			},
		}
	}
	edits := collectMachineApplicable(doc)
	if len(edits) == 0 {
		return nil
	}
	// Overlapping edits within one WorkspaceEdit are illegal per LSP
	// 3.17; clients reject the bundle outright when any two ranges
	// touch. Sort by start + drop any edit whose range intersects
	// one we've already kept — earlier sources (parse, resolve,
	// check) win over lint, which matches the "errors first" intent
	// of fix-all.
	edits = resolveOverlaps(edits)
	return &CodeAction{
		Title: "Fix all auto-fixable problems",
		Kind:  CodeActionSourceFixAllOsty,
		Edit: &WorkspaceEdit{
			Changes: map[string][]TextEdit{
				doc.uri: edits,
			},
		},
	}
}

func parserCanonicalFixAllEdit(doc *document) *TextEdit {
	if doc == nil || doc.analysis == nil || doc.analysis.file == nil {
		return nil
	}
	parsed := parser.ParseDetailed(doc.src)
	for _, d := range parsed.Diagnostics {
		if d != nil && d.Severity == diag.Error {
			return nil
		}
	}
	if parsed.Provenance == nil || parsed.Provenance.Empty() {
		return nil
	}
	canonicalSrc := canonical.Source(doc.src, parsed.File)
	if len(canonicalSrc) == 0 || bytes.Equal(canonicalSrc, doc.src) {
		return nil
	}
	rng := doc.analysis.lines.rangeFromOffsets(0, len(doc.src))
	return &TextEdit{
		Range:   rng,
		NewText: string(canonicalSrc),
	}
}

func airepairFixAllEdit(doc *document) *TextEdit {
	if doc == nil || doc.analysis == nil {
		return nil
	}
	result := airepair.Analyze(airepair.Request{
		Source:   doc.src,
		Filename: doc.uri,
		Mode:     airepair.ModeFrontEndAssist,
	})
	if !result.Accepted || !result.Changed {
		return nil
	}
	repaired := result.Repaired
	if file := selfhost.LowerPublicFileFromRun(parser.ParseRun(repaired)); file != nil {
		if canonicalRepaired := canonical.Source(repaired, file); len(canonicalRepaired) > 0 {
			repaired = canonicalRepaired
		}
	}
	rng := doc.analysis.lines.rangeFromOffsets(0, len(doc.src))
	return &TextEdit{
		Range:   rng,
		NewText: string(repaired),
	}
}

// collectMachineApplicable harvests every diagnostic suggestion on
// the cached analysis whose MachineApplicable bit is set, converts
// each into a TextEdit, and returns them in the order they appeared.
// The cache already contains parse + resolve + check + lint diags
// (see analyzeSingleFileViaEngine / analysisForFileInPackage), so this is a
// single pass with no redundant lint work.
func collectMachineApplicable(doc *document) []TextEdit {
	a := doc.analysis
	var out []TextEdit
	for _, d := range a.diags {
		for _, sg := range d.Suggestions {
			if !sg.MachineApplicable {
				continue
			}
			// Skip suggestions whose span is malformed (zero Start
			// AND zero End with no insertion hint). These leak in
			// when a diag was built without pinning a span; applying
			// them would rewrite the top of the file.
			if sg.Span.Start.Offset == 0 && sg.Span.End.Offset == 0 &&
				sg.Span.Start.Line == 0 {
				continue
			}
			rng := a.lines.ostyRange(sg.Span)
			out = append(out, TextEdit{Range: rng, NewText: sg.Replacement})
		}
	}
	return out
}

// resolveOverlaps sorts edits by start position (ties broken by end)
// and drops any whose range intersects an edit already kept. Two
// ranges overlap when [s1,e1) ∩ [s2,e2) is non-empty. A pure-insert
// edit at position p (start == end) is treated as a point; two
// inserts at the same point are considered duplicates so we keep
// just the first.
//
// Input order encodes source priority: parse/resolve/check
// suggestions come before lint, so when two fixes collide the
// earlier-sourced (stronger) fix wins. After deduplication the
// result is in document order, which LSP clients prefer even though
// the spec doesn't strictly require it.
func resolveOverlaps(in []TextEdit) []TextEdit {
	converted := make([]LSPTextEdit, 0, len(in))
	for _, edit := range in {
		converted = append(converted, LSPTextEdit{
			StartLine:      edit.Range.Start.Line,
			StartCharacter: edit.Range.Start.Character,
			EndLine:        edit.Range.End.Line,
			EndCharacter:   edit.Range.End.Character,
			NewText:        edit.NewText,
		})
	}
	resolved := ResolveOverlappingLSPTextEdits(converted)
	out := make([]TextEdit, 0, len(resolved))
	for _, edit := range resolved {
		out = append(out, TextEdit{
			Range: Range{
				Start: Position{Line: edit.StartLine, Character: edit.StartCharacter},
				End:   Position{Line: edit.EndLine, Character: edit.EndCharacter},
			},
			NewText: edit.NewText,
		})
	}
	return out
}
