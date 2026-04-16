package cst

import (
	"unicode/utf8"

	"github.com/osty/osty/internal/token"
)

// TriviaKind classifies a run of non-token source bytes.
//
// The enum mirrors `TriviaKind` in toolchain/lossless_lex.osty so the Go
// trivia extractor and the eventual Osty-native one agree on categories.
// When the self-host generator is able to ingest lossless_lex.osty we can
// swap the two implementations without changing downstream consumers.
type TriviaKind int

const (
	_                             TriviaKind = iota
	TriviaWhitespace                         // runs of ' ' or '\t'
	TriviaNewline                            // one or more '\n' (source is normalized)
	TriviaLineComment                        // `// ...` up to end-of-line (exclusive of '\n')
	TriviaBlockComment                       // `/* ... */`, possibly multi-line
	TriviaDocComment                         // `/// ...` up to end-of-line
	TriviaShebang                            // `#!...` on the first line only
	TriviaBom                                // U+FEFF at offset 0 only
	TriviaUnterminatedBlockComment           // `/* ...` with no closing `*/`
)

// String returns a short, stable label for snapshot and log output.
func (k TriviaKind) String() string {
	switch k {
	case TriviaWhitespace:
		return "whitespace"
	case TriviaNewline:
		return "newline"
	case TriviaLineComment:
		return "line-comment"
	case TriviaBlockComment:
		return "block-comment"
	case TriviaDocComment:
		return "doc-comment"
	case TriviaShebang:
		return "shebang"
	case TriviaBom:
		return "bom"
	case TriviaUnterminatedBlockComment:
		return "unterminated-block-comment"
	}
	return "unknown-trivia"
}

// Trivia describes one contiguous non-token span in the source.
//
// Byte-offset fields refer to the NORMALIZED source (CRLF / lone CR collapsed
// to LF). Use Normalize for anything that may have CRLF line endings.
type Trivia struct {
	Kind   TriviaKind
	Offset int // byte offset from start of normalized source
	Length int // byte length of the span
	Pos    token.Pos
	End    token.Pos
}

// Normalize collapses CRLF and lone CR to LF. Every byte offset returned by
// Extract refers to the normalized source, not the original.
//
// Passing a normalized slice through again is a no-op.
func Normalize(src []byte) []byte {
	hasCR := false
	for _, b := range src {
		if b == '\r' {
			hasCR = true
			break
		}
	}
	if !hasCR {
		return src
	}
	out := make([]byte, 0, len(src))
	for i := 0; i < len(src); i++ {
		b := src[i]
		if b == '\r' {
			if i+1 < len(src) && src[i+1] == '\n' {
				i++ // skip \r, let the \n fall through on next iter... but we're manual
			}
			out = append(out, '\n')
			continue
		}
		out = append(out, b)
	}
	return out
}

// Extract returns the trivia list that, combined with toks, covers every byte
// of src exactly once. src MUST already be normalized (see Normalize).
//
// Tokens are expected in source order with accurate Pos.Offset / End.Offset.
// The lexer in internal/selfhost produces them in this form.
//
// If a gap between tokens contains bytes that don't match any trivia shape
// (for example, an illegal byte at a token boundary), a TriviaWhitespace span
// of length 1 is emitted to preserve byte coverage. This matches the policy
// in toolchain/lossless_lex.osty:losslessScanTrivia so the two extractors
// agree on edge cases.
func Extract(src []byte, toks []token.Token) []Trivia {
	trivias := make([]Trivia, 0, 32)
	li := newLineIndex(src)
	idx := 0
	atFileStart := true

	emit := func(kind TriviaKind, start, length int) {
		if length <= 0 {
			return
		}
		start0 := start
		end0 := start + length
		if end0 > len(src) {
			end0 = len(src)
		}
		trivias = append(trivias, Trivia{
			Kind:   kind,
			Offset: start0,
			Length: end0 - start0,
			Pos:    li.locate(start0),
			End:    li.locate(end0),
		})
	}

	for _, tok := range toks {
		stopAt := tok.Pos.Offset
		if tok.Kind == token.EOF {
			stopAt = len(src)
		}
		if stopAt > len(src) {
			stopAt = len(src)
		}
		for idx < stopAt {
			kind, consumed := classifyTrivia(src, idx, atFileStart)
			if consumed <= 0 {
				// Unclassifiable single byte (shouldn't happen on valid
				// Osty source). Emit a whitespace span of length 1 to
				// maintain the byte-coverage invariant.
				emit(TriviaWhitespace, idx, 1)
				idx++
				atFileStart = false
				continue
			}
			emit(kind, idx, consumed)
			idx += consumed
			atFileStart = false
		}
		if tok.Kind == token.EOF {
			break
		}
		if tok.End.Offset > idx {
			idx = tok.End.Offset
		}
		atFileStart = false
	}

	// After the last non-EOF token, consume any remaining bytes as trivia.
	for idx < len(src) {
		kind, consumed := classifyTrivia(src, idx, atFileStart)
		if consumed <= 0 {
			emit(TriviaWhitespace, idx, 1)
			idx++
			atFileStart = false
			continue
		}
		emit(kind, idx, consumed)
		idx += consumed
		atFileStart = false
	}

	return trivias
}

// classifyTrivia inspects src[start:] and returns the trivia kind plus the
// number of bytes consumed. Callers are responsible for passing atFileStart
// only for the very first classification in the file.
//
// Returns 0 consumed when the leading byte is not a recognizable trivia
// starter — caller handles that case.
func classifyTrivia(src []byte, start int, atFileStart bool) (TriviaKind, int) {
	if start >= len(src) {
		return 0, 0
	}
	// BOM — only meaningful at file start.
	if atFileStart {
		if r, sz := utf8.DecodeRune(src[start:]); sz > 0 && r == 0xFEFF {
			return TriviaBom, sz
		}
	}
	b := src[start]
	// Shebang at file start.
	if atFileStart && b == '#' {
		if start+1 < len(src) && src[start+1] == '!' {
			end := start + 2
			for end < len(src) && src[end] != '\n' {
				end++
			}
			return TriviaShebang, end - start
		}
	}
	// Newline run.
	if b == '\n' {
		end := start
		for end < len(src) && src[end] == '\n' {
			end++
		}
		return TriviaNewline, end - start
	}
	// Whitespace run (spaces and tabs; newlines handled above).
	if b == ' ' || b == '\t' {
		end := start
		for end < len(src) {
			c := src[end]
			if c != ' ' && c != '\t' {
				break
			}
			end++
		}
		return TriviaWhitespace, end - start
	}
	// Comments.
	if b == '/' && start+1 < len(src) {
		next := src[start+1]
		if next == '/' {
			// Determine doc vs line.
			// `///` followed by anything other than another '/' is a doc comment.
			// `////` (or more) is a regular line comment per the lexer's spec.
			isDoc := false
			if start+2 < len(src) && src[start+2] == '/' && (start+3 >= len(src) || src[start+3] != '/') {
				isDoc = true
			}
			end := start + 2
			for end < len(src) && src[end] != '\n' {
				end++
			}
			if isDoc {
				return TriviaDocComment, end - start
			}
			return TriviaLineComment, end - start
		}
		if next == '*' {
			end := start + 2
			for end+1 < len(src) {
				if src[end] == '*' && src[end+1] == '/' {
					return TriviaBlockComment, (end + 2) - start
				}
				end++
			}
			// Unterminated: consume to EOF.
			return TriviaUnterminatedBlockComment, len(src) - start
		}
	}
	return 0, 0
}

// lineIndex mirrors toolchain/lossless_lex.osty:LosslessLineIndex. It records
// the start offset of every line so positions can be resolved in O(lineCount)
// without re-walking the source.
type lineIndex struct {
	starts []int
	total  int
}

func newLineIndex(src []byte) *lineIndex {
	starts := []int{0}
	for i, b := range src {
		if b == '\n' {
			starts = append(starts, i+1)
		}
	}
	return &lineIndex{starts: starts, total: len(src)}
}

// locate resolves a byte offset to a 1-based (line, column). Column is in
// bytes; the existing diagnostic renderer converts to runes when printing.
func (li *lineIndex) locate(offset int) token.Pos {
	if offset <= 0 {
		return token.Pos{Offset: 0, Line: 1, Column: 1}
	}
	if offset >= li.total {
		offset = li.total
	}
	// Binary search for the largest start <= offset.
	lo, hi := 0, len(li.starts)
	for lo < hi {
		mid := (lo + hi) / 2
		if li.starts[mid] <= offset {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	line := lo // 1-based: starts[0]=0 for line 1
	start := li.starts[line-1]
	return token.Pos{Offset: offset, Line: line, Column: offset - start + 1}
}
