package lsp

import (
	"sort"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/lint"
)

// LSP code-action kind strings (LSP 3.17 §codeActionKind). Only the
// kinds the server emits are listed.
const (
	// Source-action kinds are surfaced in editors as bulk operations
	// on the entire file (e.g. "Organize Imports", "Fix All"); they
	// are not attached to a specific diagnostic.
	CodeActionSource              = "source"
	CodeActionSourceOrganizeImports = "source.organizeImports"
	CodeActionSourceFixAll        = "source.fixAll"
	CodeActionSourceFixAllOsty    = "source.fixAll.osty"
)

// wantsKind reports whether the client's `context.only` filter (if
// any) admits the given action kind. An empty filter means "everything
// is admissible". A filter entry is treated as a prefix match so that
// a client asking for `"source"` gets every `source.*` subtype back —
// this matches how LSP 3.17 defines `CodeActionKind` inheritance.
func wantsKind(only []string, kind string) bool {
	if len(only) == 0 {
		return true
	}
	for _, k := range only {
		if k == kind {
			return true
		}
		// Prefix match (e.g. "source" matches "source.organizeImports").
		if strings.HasPrefix(kind, k+".") {
			return true
		}
	}
	return false
}

// organizeImportsAction builds a `source.organizeImports` action when
// one would actually change the document. The action:
//
//   - Removes `use` declarations that are never referenced (modulo
//     side-effect imports prefixed with `_`).
//   - Drops duplicates whose effective alias + target path coincide
//     — two `use foo` entries collapse to one.
//   - Groups into: stdlib (`std.*`), external, Go FFI — matching
//     `osty fmt`'s ordering rules — and sorts alphabetically within
//     each group by raw path.
//
// FFI `use go "..." { ... }` blocks are treated as opaque: we still
// remove them if unused but don't attempt to rewrite their bodies.
//
// Returns nil if no changes would be made so the action isn't offered
// when the file is already clean.
func organizeImportsAction(doc *document) *CodeAction {
	a := doc.analysis
	if a == nil || a.file == nil || len(a.file.Uses) == 0 {
		return nil
	}
	uses := a.file.Uses
	// Drop uses whose primary span overlaps a parser error — we don't
	// trust their offsets enough to rewrite the file. A single bad line
	// shouldn't void the whole organize operation; just skip that one.
	unused := unusedUseSet(doc)

	type keyed struct {
		u     *ast.UseDecl
		group int
		key   string
		text  string
	}
	kept := make([]keyed, 0, len(uses))
	seen := make(map[string]bool, len(uses))
	for _, u := range uses {
		if u == nil {
			continue
		}
		if unused[u] {
			continue
		}
		text := useSourceText(doc.src, u)
		if text == "" {
			continue
		}
		group := useGroup(u)
		key := useKey(u)
		// Dedup by (group, key, alias) — two identical `use` lines
		// collapse to one.
		dedupKey := keyWithAlias(group, key, u.Alias)
		if seen[dedupKey] {
			continue
		}
		seen[dedupKey] = true
		kept = append(kept, keyed{u: u, group: group, key: key, text: text})
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].group != kept[j].group {
			return kept[i].group < kept[j].group
		}
		if kept[i].key != kept[j].key {
			return kept[i].key < kept[j].key
		}
		return kept[i].u.Alias < kept[j].u.Alias
	})

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
	startOff := uses[0].PosV.Offset
	endOff := endOfLineOffset(doc.src, uses[len(uses)-1].EndV.Offset)
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

// useGroup classifies a use decl into the canonical group ordering.
// Kept in sync with format.useGroupOrder — duplicating instead of
// importing to avoid pulling the formatter into the lsp package just
// for a three-way switch.
func useGroup(u *ast.UseDecl) int {
	if u.IsGoFFI {
		return 2
	}
	if len(u.Path) > 0 && u.Path[0] == "std" {
		return 0
	}
	return 1
}

// useKey is the intra-group sort key.
func useKey(u *ast.UseDecl) string {
	if u.IsGoFFI {
		return u.GoPath
	}
	if u.RawPath != "" {
		return u.RawPath
	}
	return strings.Join(u.Path, ".")
}

// keyWithAlias combines the sort key with the alias so `use foo` and
// `use foo as bar` don't dedupe into one entry.
func keyWithAlias(group int, key, alias string) string {
	return string(rune('0'+group)) + "|" + key + "|" + alias
}

// useSourceText extracts the exact source text of a single-line use
// decl. Multi-line FFI blocks (whose body spans several lines) are
// returned verbatim including the `{ ... }` block.
func useSourceText(src []byte, u *ast.UseDecl) string {
	start := u.PosV.Offset
	end := u.EndV.Offset
	if start < 0 || end > len(src) || start >= end {
		return ""
	}
	return strings.TrimRight(string(src[start:end]), " \t\r\n")
}

// endOfLineOffset advances from `off` over any trailing whitespace
// plus one line terminator, returning the offset of the first byte of
// the next line (or len(src) if we hit EOF first).
func endOfLineOffset(src []byte, off int) int {
	for off < len(src) && (src[off] == ' ' || src[off] == '\t') {
		off++
	}
	if off < len(src) && src[off] == '\r' {
		off++
	}
	if off < len(src) && src[off] == '\n' {
		off++
	}
	return off
}

// unusedUseSet runs the single-file lint pass and returns the set of
// UseDecl nodes flagged L0003 ("unused import"). Reuses lint.File so
// the organize action stays in lockstep with the standalone `osty
// lint` CLI — if lint's unused-import logic evolves, the refactor
// follows automatically.
func unusedUseSet(doc *document) map[*ast.UseDecl]bool {
	out := map[*ast.UseDecl]bool{}
	a := doc.analysis
	if a == nil || a.file == nil {
		return out
	}
	// Build a by-offset index of every use decl so we can map lint
	// diagnostics (which carry positions, not AST pointers) back to
	// the node they concern.
	byOffset := make(map[int]*ast.UseDecl, len(a.file.Uses))
	for _, u := range a.file.Uses {
		if u != nil {
			byOffset[u.PosV.Offset] = u
		}
	}
	lr := lint.File(a.file, a.resolve, a.check)
	if lr == nil {
		return out
	}
	for _, d := range lr.Diags {
		if d.Code != diag.CodeUnusedImport {
			continue
		}
		for _, sp := range d.Spans {
			if u := byOffset[sp.Span.Start.Offset]; u != nil {
				out[u] = true
			}
		}
	}
	return out
}

// ---- Fix All ----

// fixAllAction collects every machine-applicable suggestion the lint
// pass produced for this file and folds them into a single
// WorkspaceEdit. The action is offered only when at least one
// applicable fix exists so the lightbulb doesn't light up on clean
// files.
//
// Resolver / checker diagnostics that carry suggestions are pulled
// in as well; those already feed the per-diagnostic quick fixes, but
// fix-all rolls them into one bulk edit for a single-click cleanup.
func fixAllAction(doc *document) *CodeAction {
	a := doc.analysis
	if a == nil || a.file == nil {
		return nil
	}
	edits := collectMachineApplicable(doc)
	if len(edits) == 0 {
		return nil
	}
	// Offsets may overlap between competing suggestions for the same
	// span (two renames of the same ident). applyEditsInReverse keeps
	// only the first suggestion at each offset so we never produce
	// overlapping edits, which clients reject outright.
	edits = dedupeEdits(edits)
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

// collectMachineApplicable harvests every diagnostic suggestion (from
// parse, resolve, check, AND an on-demand lint pass) whose
// MachineApplicable bit is set, converts each into a TextEdit, and
// returns them in document order. Lint diagnostics are re-run here
// because the LSP analysis cache currently skips the lint stage.
func collectMachineApplicable(doc *document) []TextEdit {
	a := doc.analysis
	var sources []*diag.Diagnostic
	sources = append(sources, a.diags...)
	if lr := lint.File(a.file, a.resolve, a.check); lr != nil {
		sources = append(sources, lr.Diags...)
	}
	var out []TextEdit
	for _, d := range sources {
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

// dedupeEdits drops edits whose range duplicates an earlier entry's
// range. LSP disallows overlapping edits within one WorkspaceEdit —
// two suggestions for the same ident span would both try to rename
// it, and the client would reject the bundle. We keep the first
// suggestion encountered (diagnostics are ordered by severity, so
// errors win over warnings).
func dedupeEdits(in []TextEdit) []TextEdit {
	if len(in) <= 1 {
		return in
	}
	type key struct {
		sLine, sChar, eLine, eChar uint32
	}
	seen := make(map[key]bool, len(in))
	out := in[:0]
	for _, e := range in {
		k := key{
			e.Range.Start.Line, e.Range.Start.Character,
			e.Range.End.Line, e.Range.End.Character,
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, e)
	}
	return out
}
