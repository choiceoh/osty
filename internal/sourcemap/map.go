package sourcemap

import (
	"sort"
	"unicode/utf8"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// Entry links one generated canonical source span back to the original source
// span that produced it.
type Entry struct {
	Kind      string
	Generated diag.Span
	Original  diag.Span
}

// Map stores a coarse bidirectional mapping between canonical source emitted by
// the formatter and the original spans carried on the AST.
type Map struct {
	entries []Entry
}

// Empty reports whether the map carries any span mappings.
func (m *Map) Empty() bool {
	return m == nil || len(m.entries) == 0
}

// Entries returns a read-only view of the stored mappings.
func (m *Map) Entries() []Entry {
	if m == nil {
		return nil
	}
	return m.entries
}

// RemapSpan projects a canonical/generated span back onto the original source.
func (m *Map) RemapSpan(span diag.Span) (diag.Span, bool) {
	entry := m.entryForGenerated(span.Start.Offset, span.End.Offset)
	if entry == nil {
		return diag.Span{}, false
	}
	return entry.Original, true
}

// GeneratedSpanForOriginal projects an original AST/source span into the
// canonical/generated source.
func (m *Map) GeneratedSpanForOriginal(span diag.Span) (diag.Span, bool) {
	entry := m.entryForOriginal(span.Start.Offset, span.End.Offset)
	if entry == nil {
		return diag.Span{}, false
	}
	return entry.Generated, true
}

// RemapDiagnostic returns a deep-cloned diagnostic whose spans and structured
// suggestions are projected back into the original source wherever possible.
func (m *Map) RemapDiagnostic(in *diag.Diagnostic) *diag.Diagnostic {
	if in == nil {
		return nil
	}
	out := *in
	if len(in.Spans) > 0 {
		out.Spans = make([]diag.LabeledSpan, len(in.Spans))
		for i, sp := range in.Spans {
			out.Spans[i] = sp
			if remapped, ok := m.RemapSpan(sp.Span); ok {
				out.Spans[i].Span = remapped
			}
		}
	}
	if len(in.Suggestions) > 0 {
		out.Suggestions = make([]diag.Suggestion, len(in.Suggestions))
		for i, sg := range in.Suggestions {
			out.Suggestions[i] = sg
			if remapped, ok := m.RemapSpan(sg.Span); ok {
				out.Suggestions[i].Span = remapped
			}
			if sg.CopyFrom != nil {
				copied := *sg.CopyFrom
				if remapped, ok := m.RemapSpan(copied); ok {
					copied = remapped
				}
				out.Suggestions[i].CopyFrom = &copied
			}
		}
	}
	return &out
}

// RemapDiagnostics clones and remaps every diagnostic in the slice.
func (m *Map) RemapDiagnostics(in []*diag.Diagnostic) []*diag.Diagnostic {
	if len(in) == 0 {
		return nil
	}
	out := make([]*diag.Diagnostic, 0, len(in))
	for _, d := range in {
		out = append(out, m.RemapDiagnostic(d))
	}
	return out
}

func (m *Map) entryForGenerated(start, end int) *Entry {
	if m == nil {
		return nil
	}
	if end < start {
		end = start
	}
	var best *Entry
	bestSize := int(^uint(0) >> 1)
	for i := range m.entries {
		entry := &m.entries[i]
		es := entry.Generated.Start.Offset
		ee := entry.Generated.End.Offset
		if ee < es {
			ee = es
		}
		if start < es || end > ee {
			continue
		}
		size := ee - es
		if start == es && end == ee {
			if best == nil || best.Generated.Start.Offset != start || best.Generated.End.Offset != end || size < bestSize {
				best = entry
				bestSize = size
			}
			continue
		}
		if size < bestSize {
			best = entry
			bestSize = size
		}
	}
	return best
}

func (m *Map) entryForOriginal(start, end int) *Entry {
	if m == nil {
		return nil
	}
	if end < start {
		end = start
	}
	var best *Entry
	bestSize := int(^uint(0) >> 1)
	for i := range m.entries {
		entry := &m.entries[i]
		os := entry.Original.Start.Offset
		oe := entry.Original.End.Offset
		if oe < os {
			oe = os
		}
		if start < os || end > oe {
			continue
		}
		size := oe - os
		if start == os && end == oe {
			generatedSize := entry.Generated.End.Offset - entry.Generated.Start.Offset
			bestGeneratedSize := -1
			if best != nil {
				bestGeneratedSize = best.Generated.End.Offset - best.Generated.Start.Offset
			}
			if best == nil ||
				best.Original.Start.Offset != start ||
				best.Original.End.Offset != end ||
				generatedSize > bestGeneratedSize ||
				(generatedSize == bestGeneratedSize && size < bestSize) {
				best = entry
				bestSize = size
			}
			continue
		}
		if size < bestSize {
			best = entry
			bestSize = size
		}
	}
	return best
}

type rawEntry struct {
	kind          string
	generatedFrom int
	generatedTo   int
	original      diag.Span
}

// Builder accumulates mappings while canonical source is being emitted.
type Builder struct {
	entries []rawEntry
}

func NewBuilder() *Builder {
	return &Builder{}
}

func (b *Builder) Snapshot() int {
	if b == nil {
		return 0
	}
	return len(b.entries)
}

func (b *Builder) Restore(n int) {
	if b == nil {
		return
	}
	if n < 0 {
		n = 0
	}
	if n > len(b.entries) {
		n = len(b.entries)
	}
	b.entries = b.entries[:n]
}

// Add records one emitted canonical range and the original span that produced
// it.
func (b *Builder) Add(kind string, generatedStart, generatedEnd int, original diag.Span) {
	if b == nil {
		return
	}
	if original.Start.Line == 0 && original.Start.Offset == 0 && original.End.Offset == 0 {
		return
	}
	if generatedStart < 0 {
		generatedStart = 0
	}
	if generatedEnd < generatedStart {
		generatedEnd = generatedStart
	}
	if original.End.Offset < original.Start.Offset {
		original.End = original.Start
	}
	b.entries = append(b.entries, rawEntry{
		kind:          kind,
		generatedFrom: generatedStart,
		generatedTo:   generatedEnd,
		original:      original,
	})
}

// Build finalizes the map for the given generated canonical source.
func (b *Builder) Build(generated []byte) *Map {
	if b == nil || len(b.entries) == 0 {
		return nil
	}
	lineStarts := computeLineStarts(generated)
	out := &Map{
		entries: make([]Entry, 0, len(b.entries)),
	}
	for _, entry := range b.entries {
		out.entries = append(out.entries, Entry{
			Kind: entry.kind,
			Generated: diag.Span{
				Start: posForOffset(generated, lineStarts, entry.generatedFrom),
				End:   posForOffset(generated, lineStarts, entry.generatedTo),
			},
			Original: entry.original,
		})
	}
	return out
}

func computeLineStarts(src []byte) []int {
	lines := []int{0}
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\n':
			lines = append(lines, i+1)
		case '\r':
			if i+1 < len(src) && src[i+1] == '\n' {
				i++
			}
			lines = append(lines, i+1)
		}
	}
	return lines
}

func posForOffset(src []byte, lineStarts []int, off int) token.Pos {
	if off < 0 {
		off = 0
	}
	if off > len(src) {
		off = len(src)
	}
	line := sort.Search(len(lineStarts), func(i int) bool {
		return lineStarts[i] > off
	}) - 1
	if line < 0 {
		line = 0
	}
	lineStart := lineStarts[line]
	column := 1 + utf8.RuneCount(src[lineStart:off])
	return token.Pos{
		Offset: off,
		Line:   line + 1,
		Column: column,
	}
}
