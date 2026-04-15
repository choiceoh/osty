package lsp

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// lineIndex caches the byte offsets of every line start in a source
// document so that position conversions run in O(line-length) after
// a single O(n) build. The n+1-th entry (for out-of-range access) is
// len(src) so callers can safely compute `lines[p.Line]`.
type lineIndex struct {
	src   []byte
	lines []int // lines[i] = byte offset of line i+1's first byte
}

// newLineIndex scans the source once and records each line start.
// Accepts LF, CRLF, and CR line terminators for robustness, though
// Osty sources are expected to be LF-only.
func newLineIndex(src []byte) *lineIndex {
	lines := []int{0}
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\n':
			lines = append(lines, i+1)
		case '\r':
			// CR or CRLF. Skip an immediately-following LF so we
			// record a single line break for CRLF.
			if i+1 < len(src) && src[i+1] == '\n' {
				i++
			}
			lines = append(lines, i+1)
		}
	}
	return &lineIndex{src: src, lines: lines}
}

// ostyToLSP converts an Osty position (1-based line, 1-based rune
// column) into the LSP representation (0-based line, 0-based UTF-16
// code-unit character). Positions past EOF clamp to EOF; positions
// inside invalid UTF-8 degrade to the best-effort character count.
func (li *lineIndex) ostyToLSP(p token.Pos) Position {
	if p.Line <= 0 {
		return Position{}
	}
	// Clamp line to [1, len(lines)] so p at EOF (one past the last
	// newline) still returns a usable Position.
	line := p.Line
	if line > len(li.lines) {
		line = len(li.lines)
	}
	start := li.lines[line-1]
	end := p.Offset
	if end < start {
		// Defensive: Offset hasn't been set (zero Pos).
		end = start
	}
	if end > len(li.src) {
		end = len(li.src)
	}
	return Position{
		Line:      uint32(line - 1),
		Character: utf16UnitsInPrefix(li.src[start:end]),
	}
}

// lspToOsty converts an LSP position back into an Osty position. The
// returned Offset/Line/Column are consistent with what the lexer
// would have assigned when reading at that point in the source.
func (li *lineIndex) lspToOsty(p Position) token.Pos {
	line := int(p.Line) + 1
	if line > len(li.lines) {
		// Past EOF — clamp to the last line so callers get a
		// well-formed Pos instead of a zero value.
		line = len(li.lines)
	}
	if line < 1 {
		line = 1
	}
	start := li.lines[line-1]
	// Walk runes until we've consumed p.Character UTF-16 code units
	// or hit the end of the line / file.
	want := int(p.Character)
	col := 1
	off := start
	units := 0
	for off < len(li.src) {
		b := li.src[off]
		if b == '\n' || b == '\r' {
			break
		}
		r, sz := utf8.DecodeRune(li.src[off:])
		ru := 1
		if r >= 0x10000 {
			ru = 2 // surrogate pair
		}
		if units+ru > want {
			break
		}
		units += ru
		off += sz
		col++
	}
	return token.Pos{Offset: off, Line: line, Column: col}
}

// offsetToLSP converts a byte offset into a 0-based LSP Position.
// Runs a binary search on the cached line starts then walks the
// line prefix in runes to count UTF-16 code units.
func (li *lineIndex) offsetToLSP(off int) Position {
	if off < 0 {
		off = 0
	}
	if off > len(li.src) {
		off = len(li.src)
	}
	// Binary search: largest i with lines[i] <= off.
	lo, hi := 0, len(li.lines)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if li.lines[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	start := li.lines[lo]
	return Position{
		Line:      uint32(lo),
		Character: utf16UnitsInPrefix(li.src[start:off]),
	}
}

// rangeFromOffsets builds an LSP Range from two byte offsets.
func (li *lineIndex) rangeFromOffsets(start, end int) Range {
	return Range{
		Start: li.offsetToLSP(start),
		End:   li.offsetToLSP(end),
	}
}

// fileURIPath converts a `file://` URI to a local filesystem path.
// Returns ("", false) for non-file URIs (e.g. `inmemory:`, `untitled:`).
// Percent-decoding handles paths with spaces and Unicode characters.
func fileURIPath(uri string) (string, bool) {
	const prefix = "file://"
	if !strings.HasPrefix(uri, prefix) {
		return "", false
	}
	path := strings.TrimPrefix(uri, prefix)
	// On Windows the URI is `file:///C:/path/...` — strip the leading
	// slash so we get `C:/path/...`. On POSIX the leading slash IS the
	// path root and must be kept.
	if len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	decoded, err := url.PathUnescape(path)
	if err != nil {
		return "", false
	}
	return decoded, true
}

// pathToURI is the inverse of fileURIPath: wraps a local path back
// into the `file://` URI form the client uses. We skip
// percent-encoding because every major LSP client tolerates raw
// UTF-8 in file URIs.
func pathToURI(path string) string {
	if len(path) == 0 {
		return "file://"
	}
	if path[0] == '/' {
		return "file://" + path
	}
	// Windows: "C:/path" → "file:///C:/path"
	return "file:///" + path
}

// ostyRange converts a diagnostic span into an LSP Range. If the end
// position is missing (parser emitted a point-diagnostic without an
// explicit End) we fall back to a single-rune range starting at
// Start, which renders nicely in most editors.
func (li *lineIndex) ostyRange(s diag.Span) Range {
	start := li.ostyToLSP(s.Start)
	endPos := s.End
	if endPos.Line == 0 {
		endPos = s.Start
	}
	end := li.ostyToLSP(endPos)
	if end.Line == start.Line && end.Character <= start.Character {
		// Ensure a visible width: extend by one character so
		// editors highlight at least one glyph.
		end.Character = start.Character + 1
	}
	return Range{Start: start, End: end}
}

// utf16UnitsInPrefix counts the number of UTF-16 code units needed
// to encode the given UTF-8 slice. A rune < 0x10000 occupies one
// code unit; anything above (astral / supplementary plane) requires
// two (a surrogate pair). Invalid UTF-8 bytes are counted as one
// unit each so a corrupted source still yields a finite answer.
func utf16UnitsInPrefix(p []byte) uint32 {
	var n uint32
	for len(p) > 0 {
		r, sz := utf8.DecodeRune(p)
		if r == utf8.RuneError && sz == 1 {
			n++
			p = p[1:]
			continue
		}
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
		p = p[sz:]
	}
	return n
}

