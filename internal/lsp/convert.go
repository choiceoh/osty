package lsp

import (
	"net/url"
	"strings"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
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
	return &lineIndex{src: src, lines: selfhost.LSPLineStarts(src)}
}

// ostyToLSP converts an Osty position (1-based line, 1-based rune
// column) into the LSP representation (0-based line, 0-based UTF-16
// code-unit character). Positions past EOF clamp to EOF; positions
// inside invalid UTF-8 degrade to the best-effort character count.
func (li *lineIndex) ostyToLSP(p token.Pos) Position {
	pos := selfhost.LSPOstyPositionToLSP(li.src, li.lines, p.Line, p.Offset)
	return Position{Line: pos.Line, Character: pos.Character}
}

// lspToOsty converts an LSP position back into an Osty position. The
// returned Offset/Line/Column are consistent with what the lexer
// would have assigned when reading at that point in the source.
func (li *lineIndex) lspToOsty(p Position) token.Pos {
	pos := selfhost.LSPLSPPositionToOsty(li.src, li.lines, p.Line, p.Character)
	return token.Pos{Offset: pos.Offset, Line: pos.Line, Column: pos.Column}
}

// offsetToLSP converts a byte offset into a 0-based LSP Position.
// Runs a binary search on the cached line starts then walks the
// line prefix in runes to count UTF-16 code units.
func (li *lineIndex) offsetToLSP(off int) Position {
	pos := selfhost.LSPOffsetToPosition(li.src, li.lines, off)
	return Position{Line: pos.Line, Character: pos.Character}
}

// rangeFromOffsets builds an LSP Range from two byte offsets.
func (li *lineIndex) rangeFromOffsets(start, end int) Range {
	rng := selfhost.LSPRangeFromOffsets(li.src, li.lines, start, end)
	return Range{
		Start: Position{Line: rng.Start.Line, Character: rng.Start.Character},
		End:   Position{Line: rng.End.Line, Character: rng.End.Character},
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
	return selfhost.LSPPathToURI(path)
}

// ostyRange converts a diagnostic span into an LSP Range. If the end
// position is missing (parser emitted a point-diagnostic without an
// explicit End) we fall back to a single-rune range starting at
// Start, which renders nicely in most editors.
func (li *lineIndex) ostyRange(s diag.Span) Range {
	rng := selfhost.LSPRangeFromOstySpan(li.src, li.lines, s.Start.Line, s.Start.Offset, s.End.Line, s.End.Offset)
	return Range{
		Start: Position{Line: rng.Start.Line, Character: rng.Start.Character},
		End:   Position{Line: rng.End.Line, Character: rng.End.Character},
	}
}

// utf16UnitsInPrefix counts the number of UTF-16 code units needed
// to encode the given UTF-8 slice. A rune < 0x10000 occupies one
// code unit; anything above (astral / supplementary plane) requires
// two (a surrogate pair). Invalid UTF-8 bytes are counted as one
// unit each so a corrupted source still yields a finite answer.
func utf16UnitsInPrefix(p []byte) uint32 {
	return selfhost.LSPUTF16UnitsInPrefix(p)
}
