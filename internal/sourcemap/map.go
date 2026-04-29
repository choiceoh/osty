package sourcemap

import (
	"bytes"
	"sort"
	"sync"
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

	originalExactOnce sync.Once
	originalExact     map[spanKey]*Entry
}

type spanKey struct {
	start int
	end   int
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

// Clone returns a copy of the map metadata. Entry values are immutable, so a
// shallow entry copy is enough to keep callers from mutating this map's slice.
func (m *Map) Clone() *Map {
	if m == nil || len(m.entries) == 0 {
		return nil
	}
	return &Map{entries: append([]Entry(nil), m.entries...)}
}

// Compose chains this map with next. The returned map projects this map's
// generated spans through this map's original spans and then through next,
// e.g. canonical source -> transformed source -> on-disk source.
func (m *Map) Compose(next *Map) *Map {
	if m == nil || len(m.entries) == 0 {
		return nil
	}
	if next == nil || len(next.entries) == 0 {
		return m.Clone()
	}
	out := &Map{entries: make([]Entry, 0, len(m.entries))}
	for _, entry := range m.entries {
		original := entry.Original
		if remapped, ok := next.RemapSpanProjected(entry.Original); ok {
			original = remapped
		}
		out.entries = append(out.entries, Entry{
			Kind:      entry.Kind,
			Generated: entry.Generated,
			Original:  original,
		})
	}
	return out
}

// RemapSpan projects a canonical/generated span back onto the original source.
func (m *Map) RemapSpan(span diag.Span) (diag.Span, bool) {
	entry := m.entryForGenerated(span.Start.Offset, span.End.Offset)
	if entry == nil {
		return diag.Span{}, false
	}
	return entry.Original, true
}

// RemapSpanProjected projects a generated span back onto the original source
// while preserving sub-span offsets when a map entry has enough information.
func (m *Map) RemapSpanProjected(span diag.Span) (diag.Span, bool) {
	entry := m.entryForGenerated(span.Start.Offset, span.End.Offset)
	if entry != nil {
		return projectSpan(entry, span), true
	}
	start, ok := m.RemapPos(span.Start)
	if !ok {
		return diag.Span{}, false
	}
	end, ok := m.RemapPos(span.End)
	if !ok || end.Offset < start.Offset {
		end = start
	}
	return diag.Span{Start: start, End: end}, true
}

// RemapPos projects a generated position back onto the original source.
func (m *Map) RemapPos(pos token.Pos) (token.Pos, bool) {
	entry := m.entryForGeneratedPoint(pos.Offset)
	if entry == nil {
		return token.Pos{}, false
	}
	return projectPos(entry.Generated, entry.Original, pos.Offset), true
}

// GeneratedSpanForOriginal projects an original AST/source span into the
// canonical/generated source.
func (m *Map) GeneratedSpanForOriginal(span diag.Span) (diag.Span, bool) {
	if entry := m.exactOriginalEntry(span.Start.Offset, span.End.Offset); entry != nil {
		return entry.Generated, true
	}
	entry := m.entryForOriginal(span.Start.Offset, span.End.Offset)
	if entry == nil {
		return diag.Span{}, false
	}
	return entry.Generated, true
}

func (m *Map) exactOriginalEntry(start, end int) *Entry {
	if m == nil {
		return nil
	}
	if end < start {
		end = start
	}
	m.originalExactOnce.Do(func() {
		if len(m.entries) == 0 {
			return
		}
		cache := make(map[spanKey]*Entry, len(m.entries))
		for i := range m.entries {
			entry := &m.entries[i]
			os := entry.Original.Start.Offset
			oe := entry.Original.End.Offset
			if oe < os {
				oe = os
			}
			key := spanKey{start: os, end: oe}
			if prev := cache[key]; prev != nil {
				prevGenerated := prev.Generated.End.Offset - prev.Generated.Start.Offset
				nextGenerated := entry.Generated.End.Offset - entry.Generated.Start.Offset
				if nextGenerated <= prevGenerated {
					continue
				}
			}
			cache[key] = entry
		}
		m.originalExact = cache
	})
	return m.originalExact[spanKey{start: start, end: end}]
}

// RemapDiagnostic returns a deep-cloned diagnostic whose spans and structured
// suggestions are projected back into the original source wherever possible.
func (m *Map) RemapDiagnostic(in *diag.Diagnostic) *diag.Diagnostic {
	return m.remapDiagnostic(in, m.RemapSpan)
}

// RemapDiagnosticProjected is RemapDiagnostic with sub-span projection.
func (m *Map) RemapDiagnosticProjected(in *diag.Diagnostic) *diag.Diagnostic {
	return m.remapDiagnostic(in, m.RemapSpanProjected)
}

func (m *Map) remapDiagnostic(in *diag.Diagnostic, remap func(diag.Span) (diag.Span, bool)) *diag.Diagnostic {
	if in == nil {
		return nil
	}
	out := *in
	if len(in.Spans) > 0 {
		out.Spans = make([]diag.LabeledSpan, len(in.Spans))
		for i, sp := range in.Spans {
			out.Spans[i] = sp
			if remapped, ok := remap(sp.Span); ok {
				out.Spans[i].Span = remapped
			}
		}
	}
	if len(in.Suggestions) > 0 {
		out.Suggestions = make([]diag.Suggestion, len(in.Suggestions))
		for i, sg := range in.Suggestions {
			out.Suggestions[i] = sg
			if remapped, ok := remap(sg.Span); ok {
				out.Suggestions[i].Span = remapped
			}
			if sg.CopyFrom != nil {
				copied := *sg.CopyFrom
				if remapped, ok := remap(copied); ok {
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
	return m.remapDiagnostics(in, m.RemapDiagnostic)
}

// RemapDiagnosticsProjected clones and remaps diagnostics using sub-span
// projection.
func (m *Map) RemapDiagnosticsProjected(in []*diag.Diagnostic) []*diag.Diagnostic {
	return m.remapDiagnostics(in, m.RemapDiagnosticProjected)
}

func (m *Map) remapDiagnostics(in []*diag.Diagnostic, remap func(*diag.Diagnostic) *diag.Diagnostic) []*diag.Diagnostic {
	if len(in) == 0 {
		return nil
	}
	out := make([]*diag.Diagnostic, 0, len(in))
	for _, d := range in {
		out = append(out, remap(d))
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

func (m *Map) entryForGeneratedPoint(offset int) *Entry {
	if m == nil {
		return nil
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
		if offset < es || offset > ee {
			continue
		}
		size := ee - es
		if offset == ee && size > 0 && best != nil {
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

func projectSpan(entry *Entry, span diag.Span) diag.Span {
	if entry == nil {
		return diag.Span{}
	}
	start := projectPos(entry.Generated, entry.Original, span.Start.Offset)
	end := projectPos(entry.Generated, entry.Original, span.End.Offset)
	if end.Offset < start.Offset {
		end = start
	}
	return diag.Span{Start: start, End: end}
}

func projectPos(generated, original diag.Span, offset int) token.Pos {
	gs := generated.Start.Offset
	ge := generated.End.Offset
	if ge < gs {
		ge = gs
	}
	os := original.Start.Offset
	oe := original.End.Offset
	if oe < os {
		oe = os
	}
	if offset <= gs || ge == gs {
		return original.Start
	}
	if offset >= ge {
		return original.End
	}
	gLen := ge - gs
	oLen := oe - os
	if oLen == 0 {
		return original.Start
	}
	projected := os + ((offset-gs)*oLen)/gLen
	if projected < os {
		projected = os
	}
	if projected > oe {
		projected = oe
	}
	return interpolatePos(original, projected)
}

func interpolatePos(span diag.Span, offset int) token.Pos {
	if offset <= span.Start.Offset {
		return span.Start
	}
	if offset >= span.End.Offset {
		return span.End
	}
	if span.Start.Line == span.End.Line && span.Start.Line > 0 {
		return token.Pos{
			Offset: offset,
			Line:   span.Start.Line,
			Column: span.Start.Column + (offset - span.Start.Offset),
		}
	}
	return token.Pos{Offset: offset, Line: span.Start.Line, Column: span.Start.Column}
}

// FromSourceTransform builds a best-effort map from transformed/generated source
// back to the original bytes supplied to a loader transform. It records exact
// unchanged line segments and a coarse segment for each edited region, which is
// enough to keep most parser/checker diagnostics anchored to the user's file
// after keyword and helper-syntax rewrites.
func FromSourceTransform(original, generated []byte) *Map {
	if bytes.Equal(original, generated) {
		return nil
	}
	builder := NewBuilder()
	origLines := sourceLineRanges(original)
	genLines := sourceLineRanges(generated)
	origStarts := computeLineStarts(original)

	n := len(genLines)
	for i := 0; i < n; i++ {
		if i < len(origLines) {
			addLineTransformMap(builder, original, generated, origStarts, origLines[i], genLines[i])
			continue
		}
		eof := len(original)
		addTransformSegment(builder, generated, original, origStarts, genLines[i].start, genLines[i].next, eof, eof)
	}
	sm := builder.Build(generated)
	if sm == nil || sm.Empty() {
		return nil
	}
	return sm
}

type sourceLineRange struct {
	start int
	end   int
	next  int
}

func sourceLineRanges(src []byte) []sourceLineRange {
	if len(src) == 0 {
		return nil
	}
	var out []sourceLineRange
	start := 0
	for i, b := range src {
		if b != '\n' {
			continue
		}
		end := i
		if end > start && src[end-1] == '\r' {
			end--
		}
		out = append(out, sourceLineRange{start: start, end: end, next: i + 1})
		start = i + 1
	}
	if start < len(src) {
		out = append(out, sourceLineRange{start: start, end: len(src), next: len(src)})
	}
	return out
}

func addLineTransformMap(builder *Builder, original, generated []byte, origStarts []int, orig, gen sourceLineRange) {
	origText := original[orig.start:orig.end]
	genText := generated[gen.start:gen.end]
	prefix := commonPrefixBytes(origText, genText)
	suffix := commonSuffixBytes(origText[prefix:], genText[prefix:])

	addTransformSegment(builder, generated, original, origStarts, gen.start, gen.start+prefix, orig.start, orig.start+prefix)
	addTransformSegment(builder, generated, original, origStarts, gen.start+prefix, gen.end-suffix, orig.start+prefix, orig.end-suffix)
	addTransformSegment(builder, generated, original, origStarts, gen.end-suffix, gen.end, orig.end-suffix, orig.end)
	if gen.next > gen.end {
		addTransformSegment(builder, generated, original, origStarts, gen.end, gen.next, orig.end, orig.next)
	}
}

func addTransformSegment(builder *Builder, generated, original []byte, origStarts []int, genStart, genEnd, origStart, origEnd int) {
	if builder == nil || genEnd <= genStart {
		return
	}
	builder.Add("source-transform", genStart, genEnd, diag.Span{
		Start: posForOffset(original, origStarts, origStart),
		End:   posForOffset(original, origStarts, origEnd),
	})
}

func commonPrefixBytes(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func commonSuffixBytes(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[len(a)-1-i] != b[len(b)-1-i] {
			return i
		}
	}
	return n
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
