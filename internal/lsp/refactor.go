package lsp

import (
	"sort"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
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
	uses := a.file.Uses
	// Bail out if any meaningful trivia sits between consecutive use
	// decls — line comments, doc comments, or annotations that the
	// AST doesn't attach to the UseDecl itself. Rewriting the block
	// would silently relocate or delete those bytes. The user can
	// still delete unused imports via the per-diagnostic quick fix,
	// so organizing is just unavailable, not broken.
	if hasTriviaBetweenUses(doc.src, uses) {
		return nil
	}
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
	if u.IsFFI() {
		return 2
	}
	if len(u.Path) > 0 && u.Path[0] == "std" {
		return 0
	}
	return 1
}

// useKey is the intra-group sort key.
func useKey(u *ast.UseDecl) string {
	if u.IsFFI() {
		return u.FFIPath()
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
// plus exactly one line terminator, returning the offset of the first
// byte of the next line (or len(src) if we hit EOF first). Trailing
// blank lines the user inserted between the last `use` and the next
// decl are preserved — we stop after consuming the first newline.
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

// hasTriviaBetweenUses reports whether non-whitespace bytes appear
// between the end of one UseDecl's line and the start of the next.
// Osty's parser strips comments before handing the token stream to
// the AST builder, so a `// ...` line between two `use` decls has no
// AST node — it only shows up when we scan the raw source. Finding
// ANY non-whitespace / non-`use` byte in that gap tells us there's
// trivia we shouldn't stomp.
//
// The scan runs from each use's end-of-line to the next use's
// PosV.Offset. If it sees anything other than ASCII whitespace or a
// second `use` keyword, we treat the block as too risky to rewrite.
func hasTriviaBetweenUses(src []byte, uses []*ast.UseDecl) bool {
	for i := 0; i+1 < len(uses); i++ {
		if uses[i] == nil || uses[i+1] == nil {
			continue
		}
		gapStart := uses[i].EndV.Offset
		gapEnd := uses[i+1].PosV.Offset
		if gapStart < 0 || gapEnd > len(src) || gapStart >= gapEnd {
			continue
		}
		for off := gapStart; off < gapEnd; off++ {
			b := src[off]
			if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
				continue
			}
			// Any other byte (comment start `/`, `#` annotation,
			// stray text) means we can't safely rewrite.
			return true
		}
	}
	return false
}

// unusedUseSet returns the set of UseDecl nodes flagged L0003
// ("unused import") by the analysis pipeline's lint pass. Uses the
// cached result on doc.analysis so organizing imports costs one diag
// scan, not another full lint walk.
func unusedUseSet(doc *document) map[*ast.UseDecl]bool {
	out := map[*ast.UseDecl]bool{}
	a := doc.analysis
	if a == nil || a.file == nil || a.lint == nil {
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
	for _, d := range a.lint.Diags {
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

// fixAllAction collects every machine-applicable suggestion the
// analysis pipeline produced for this file and folds them into a
// single WorkspaceEdit. The action is offered only when at least one
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

// collectMachineApplicable harvests every diagnostic suggestion on
// the cached analysis whose MachineApplicable bit is set, converts
// each into a TextEdit, and returns them in the order they appeared.
// The cache already contains parse + resolve + check + lint diags
// (see analyzeSingleFile / analysisForFileInPackage), so this is a
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
	if len(in) <= 1 {
		return in
	}
	// Remember original index so the stable-sort preserves source
	// priority for equal-range edits.
	type indexed struct {
		e   TextEdit
		idx int
	}
	tagged := make([]indexed, len(in))
	for i, e := range in {
		tagged[i] = indexed{e: e, idx: i}
	}
	sort.SliceStable(tagged, func(i, j int) bool {
		a, b := tagged[i].e.Range, tagged[j].e.Range
		if a.Start.Line != b.Start.Line {
			return a.Start.Line < b.Start.Line
		}
		if a.Start.Character != b.Start.Character {
			return a.Start.Character < b.Start.Character
		}
		// Same start: the earlier-sourced edit wins the tie so the
		// stable order puts it first.
		return tagged[i].idx < tagged[j].idx
	})
	out := make([]TextEdit, 0, len(tagged))
	var lastStart, lastEnd Position
	var have bool
	for _, t := range tagged {
		start := t.e.Range.Start
		end := t.e.Range.End
		if have {
			// Strict overlap: this edit begins before the previous
			// edit ended. Drop it.
			if posBefore(start, lastEnd) {
				continue
			}
			// Edge-on case: start == lastEnd. This is a valid
			// adjacency for replacements, but two inserts at the
			// exact same point (start == end == lastStart ==
			// lastEnd) would both rewrite "at the caret" and the
			// client has no way to pick an order. Collapse to the
			// first.
			if posEqual(start, lastEnd) && posEqual(start, end) &&
				posEqual(lastStart, lastEnd) {
				continue
			}
		}
		out = append(out, t.e)
		lastStart = start
		lastEnd = end
		have = true
	}
	return out
}

// posBefore reports whether a is strictly before b.
func posBefore(a, b Position) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Character < b.Character
}

// posEqual reports whether two LSP positions are identical.
func posEqual(a, b Position) bool {
	return a.Line == b.Line && a.Character == b.Character
}
