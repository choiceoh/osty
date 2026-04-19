package parser

// v0.5 (G28) §5 — scoped / grouped imports.
//
// Source form:
//
//	use path::{a, b as c, d}
//	pub use path::{open, exists as has}
//
// is rewritten into one flat use statement per item before the
// self-hosted parser runs:
//
//	use path.a
//	use path.b as c
//	use path.d
//
// The transformation lives here (Go side, pre-parse) rather than in
// `toolchain/parser.osty` because the bootstrap-gen regen pipeline
// is temporarily blocked (see pub_use.go for the full reasoning).
// Once bootstrap-gen is restored, the Osty-side grammar can consume
// `::{ ... }` natively and this file can retire.
//
// v0.5 scope:
//   - `use PATH::{ ITEMS }` — comma-separated identifier list
//   - Per-item `a as x` rename
//   - `pub` prefix preserved verbatim
//   - Trailing comma tolerated
//
// Out of scope:
//   - Nested `::{ ... }` (would need hierarchical import trees)
//   - Star re-export (`::{*}`)
//   - Renames on the base path
//
// Byte-offset preservation: the rewrite expands a single line into
// multiple use statements joined by newlines, which shifts all
// offsets after the scoped-import site. Diagnostics produced after
// the expansion point use the new offsets; tooling that needs to
// map back should consult the Provenance step (`scoped_import_
// expansion`) attached to the parse result.

import (
	"bytes"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/token"
)

type scopedImportEdit struct {
	start int
	end   int
	text  []byte
	step  ProvenanceStep
}

type scopedUseSite struct {
	startOff, endOff int
	startPos, endPos token.Pos
	path             string
	items            []scopedItem
	isPub            bool // `pub use path::{...}` — preserved on each expansion
}

type scopedItem struct {
	name  string
	alias string
}

// expandScopedImports rewrites `use PATH::{ ITEMS }` into one flat
// use per item. Returns the rewritten source and provenance steps.
func expandScopedImports(src []byte) ([]byte, []ProvenanceStep) {
	toks, _, _ := selfhost.Lex(src)
	if len(toks) == 0 {
		return src, nil
	}
	var edits []scopedImportEdit

	i := 0
	for i < len(toks) {
		site, ok, next := findScopedUseSite(toks, i)
		i = next
		if !ok {
			continue
		}
		replacement := buildFlatUses(src, site)
		if replacement == nil {
			continue
		}
		edits = append(edits, scopedImportEdit{
			start: site.startOff,
			end:   site.endOff,
			text:  replacement,
			step: ProvenanceStep{
				Kind:        "scoped_import_expansion",
				SourceHabit: "scoped_use_braces",
				Span: diag.Span{
					Start: site.startPos,
					End:   site.endPos,
				},
				Detail: "expand `use path::{a, b, c}` into one `use` per item",
			},
		})
	}
	if len(edits) == 0 {
		return src, nil
	}

	// Apply edits in reverse so earlier offsets stay valid.
	out := append([]byte(nil), src...)
	steps := make([]ProvenanceStep, 0, len(edits))
	for j := len(edits) - 1; j >= 0; j-- {
		e := edits[j]
		if e.start < 0 || e.end < e.start || e.end > len(out) {
			continue
		}
		tail := append([]byte(nil), out[e.end:]...)
		out = append(append(out[:e.start], e.text...), tail...)
		steps = append(steps, e.step)
	}
	reverseProvenanceSteps(steps)
	return out, steps
}

// findScopedUseSite scans forward from `from` for a `[pub] use PATH
// ::{ ITEMS }` run. The `pub` prefix is preserved in the rewritten
// text; the rewrite only touches the `PATH::{...}` portion.
func findScopedUseSite(toks []token.Token, from int) (scopedUseSite, bool, int) {
	empty := scopedUseSite{}
	i := from
	// Skip forward to the next USE keyword.
	for i < len(toks) && toks[i].Kind != token.USE {
		i++
	}
	if i >= len(toks) {
		return empty, false, i
	}
	useTok := toks[i]
	// Detect `pub` immediately preceding `use` so the expansion keeps
	// visibility on every generated line. The PUB token may or may
	// not be in the stream as a dedicated kind — handle both.
	isPub := false
	if i > 0 {
		if toks[i-1].Kind == token.PUB {
			isPub = true
		}
	}
	i++
	// Path: IDENT ('.' IDENT)*.
	if i >= len(toks) || toks[i].Kind != token.IDENT {
		return empty, false, i
	}
	pathStart := i
	var pathParts []string
	pathParts = append(pathParts, toks[i].Value)
	i++
	for i+1 < len(toks) && toks[i].Kind == token.DOT && toks[i+1].Kind == token.IDENT {
		pathParts = append(pathParts, toks[i+1].Value)
		i += 2
	}
	// Expect `::` then `{`.
	if i+1 >= len(toks) || toks[i].Kind != token.COLONCOLON || toks[i+1].Kind != token.LBRACE {
		return empty, false, i
	}
	i += 2 // consume :: and {
	// Items until `}`.
	var items []scopedItem
	for i < len(toks) && toks[i].Kind != token.RBRACE {
		if toks[i].Kind == token.COMMA {
			i++
			continue
		}
		if toks[i].Kind != token.IDENT {
			return empty, false, i + 1
		}
		item := scopedItem{name: toks[i].Value}
		i++
		// Optional `as NAME`.
		if i+1 < len(toks) && toks[i].Kind == token.IDENT && toks[i].Value == "as" &&
			toks[i+1].Kind == token.IDENT {
			item.alias = toks[i+1].Value
			i += 2
		}
		items = append(items, item)
	}
	if i >= len(toks) || toks[i].Kind != token.RBRACE {
		return empty, false, i
	}
	endTok := toks[i]
	i++
	_ = pathStart
	return scopedUseSite{
		startOff: useTok.Pos.Offset,
		endOff:   endTok.End.Offset,
		startPos: useTok.Pos,
		endPos:   endTok.End,
		path:     joinDotted(pathParts),
		items:    items,
		isPub:    isPub,
	}, true, i
}

// buildFlatUses renders one `use PATH.name [as alias]` line per item,
// joined by newlines. The replacement is laid on top of the
// original scoped-import span and keeps the first line in place;
// additional items extend below with the same indentation as the
// original `use` token.
func buildFlatUses(src []byte, site scopedUseSite) []byte {
	if len(site.items) == 0 {
		return nil
	}
	indent := lineIndent(src, site.startOff)
	var buf bytes.Buffer
	for i, it := range site.items {
		if i > 0 {
			buf.WriteByte('\n')
			buf.WriteString(indent)
			// Keep `pub` visibility on every expansion. The first
			// line retains the existing `pub` that sat in the source
			// unchanged (we only replace starting at `use`).
			if site.isPub {
				buf.WriteString("pub ")
			}
		}
		buf.WriteString("use ")
		buf.WriteString(site.path)
		buf.WriteByte('.')
		buf.WriteString(it.name)
		if it.alias != "" {
			buf.WriteString(" as ")
			buf.WriteString(it.alias)
		}
	}
	return buf.Bytes()
}

// lineIndent returns the whitespace run at the start of the line
// containing offset.
func lineIndent(src []byte, offset int) string {
	start := offset
	for start > 0 && src[start-1] != '\n' {
		start--
	}
	end := start
	for end < len(src) && (src[end] == ' ' || src[end] == '\t') {
		end++
	}
	return string(src[start:end])
}

func joinDotted(parts []string) string {
	var b bytes.Buffer
	for i, p := range parts {
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(p)
	}
	return b.String()
}
