package cst

import (
	"sort"

	"github.com/osty/osty/internal/token"
)

// EntitySpan is the top-level syntax envelope a caller wants represented as a
// structural Green node. Start and End are byte offsets in the normalized
// source; End is exclusive.
type EntitySpan struct {
	Start int
	End   int
	Kind  GreenKind
}

// SyntaxSpan is the nested form of EntitySpan. It describes one source-backed
// syntax envelope and any already-known child envelopes. Start and End are
// byte offsets in the normalized source; End is exclusive.
//
// This is the compatibility bridge between the current semantic parser arena
// and the long-term parser-owned Green event stream. Once the parser emits
// StartNode/Token/FinishNode events directly, callers can bypass SyntaxSpan
// and feed GreenBuilder without changing Red consumers.
type SyntaxSpan struct {
	Start    int
	End      int
	Kind     GreenKind
	Children []SyntaxSpan
}

// BuildFromEntitySpans lifts flat top-level syntax spans plus a token stream
// into a Green tree wrapped in a Red Tree. It is kept for older adapter
// callers; new parser-facing code should prefer BuildFromSyntaxSpans.
//
// The resulting tree carries:
//
//  1. Structural Green nodes for the file and every supplied entity.
//  2. Flat token runs inside each entity. Use BuildFromSyntaxSpans when nested
//     expression/statement/type structure is available.
//  3. Trivia attached as leading and trailing runs on tokens. Runs between
//     tokens split at the first newline or doc comment — see
//     pairTriviaToTokens for the exact rule. Tail trivia after the last
//     token sits under the root via a zero-width GkErrorMissing sentinel.
//
// Byte coverage: every source byte is reachable from the tree via either a
// token's text or a trivia record. TestBuildRoundTrip enforces this.
//
// A native parser can replace this adapter call without changing Red consumers.
func BuildFromEntitySpans(src []byte, entities []EntitySpan, toks []token.Token, trivias []Trivia) *Tree {
	spans := make([]SyntaxSpan, 0, len(entities))
	for _, ent := range entities {
		spans = append(spans, SyntaxSpan{Start: ent.Start, End: ent.End, Kind: ent.Kind})
	}
	return BuildFromSyntaxSpans(src, spans, toks, trivias)
}

// BuildFromSyntaxSpans lifts a nested syntax span tree plus a token stream into
// a Green tree wrapped in a Red Tree. It preserves the same byte-coverage and
// trivia-attachment contract as BuildFromEntitySpans, but keeps child syntax
// nodes nested instead of flattening every top-level entity into raw tokens.
func BuildFromSyntaxSpans(src []byte, spans []SyntaxSpan, toks []token.Token, trivias []Trivia) *Tree {
	b := NewBuilder(nil)
	arena := b.Arena()

	triviaIDs := make([]int, len(trivias))
	for i, tr := range trivias {
		triviaIDs[i] = arena.AddTrivia(tr)
	}
	leading, trailing, tailTrivia := pairTriviaToTokens(toks, trivias)
	spans = sanitizeSyntaxChildren(spans, 0, len(src))

	b.StartNode(GkFile)
	tokIdx := 0
	for _, span := range spans {
		startOff := span.Start

		// Orphan tokens before the syntax node attach to the file root.
		for tokIdx < len(toks) && !isEOF(toks[tokIdx]) && toks[tokIdx].Pos.Offset < startOff {
			emitToken(b, toks[tokIdx], src, leading[tokIdx], trailing[tokIdx], triviaIDs)
			tokIdx++
		}
		emitSyntaxSpan(b, span, toks, src, leading, trailing, triviaIDs, &tokIdx)
	}
	for tokIdx < len(toks) {
		tk := toks[tokIdx]
		if isEOF(tk) {
			break
		}
		emitToken(b, tk, src, leading[tokIdx], trailing[tokIdx], triviaIDs)
		tokIdx++
	}

	if len(tailTrivia) > 0 {
		// File-tail trivia (e.g. final newline with no successor token).
		// Wrap it in a zero-width leaf under the file root so traversal
		// reaches it. A dedicated GkEndOfFile kind would read better and
		// is a future refinement.
		b.Token(GkErrorMissing, 0, "", 0, translateTriviaIDs(tailTrivia, triviaIDs), nil)
	}

	b.FinishNode() // GkFile
	_, root := b.Finish()
	return NewTreeFromSource(arena, root, src)
}

func emitSyntaxSpan(
	b *GreenBuilder,
	span SyntaxSpan,
	toks []token.Token,
	src []byte,
	leading, trailing [][]int,
	triviaIDs []int,
	tokIdx *int,
) {
	b.StartNode(span.Kind)
	children := sanitizeSyntaxChildren(span.Children, span.Start, span.End)
	for _, child := range children {
		for *tokIdx < len(toks) && !isEOF(toks[*tokIdx]) && toks[*tokIdx].Pos.Offset < child.Start {
			emitToken(b, toks[*tokIdx], src, leading[*tokIdx], trailing[*tokIdx], triviaIDs)
			*tokIdx = *tokIdx + 1
		}
		emitSyntaxSpan(b, child, toks, src, leading, trailing, triviaIDs, tokIdx)
	}
	for *tokIdx < len(toks) && !isEOF(toks[*tokIdx]) && toks[*tokIdx].Pos.Offset < span.End {
		emitToken(b, toks[*tokIdx], src, leading[*tokIdx], trailing[*tokIdx], triviaIDs)
		*tokIdx = *tokIdx + 1
	}
	b.FinishNode()
}

func sanitizeSyntaxChildren(children []SyntaxSpan, parentStart, parentEnd int) []SyntaxSpan {
	if parentEnd < parentStart {
		parentEnd = parentStart
	}
	out := make([]SyntaxSpan, 0, len(children))
	for _, child := range children {
		if child.Kind == GkNone {
			continue
		}
		if child.Start < parentStart || child.End > parentEnd || child.End < child.Start {
			continue
		}
		copied := child
		copied.Children = sanitizeSyntaxChildren(child.Children, child.Start, child.End)
		out = append(out, copied)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Start == out[j].Start {
			return out[i].End > out[j].End
		}
		return out[i].Start < out[j].Start
	})

	// Keep direct children non-overlapping. Nested structure belongs inside
	// the containing child; overlapping siblings make Red offsets ambiguous.
	filtered := out[:0]
	cursor := parentStart
	for _, child := range out {
		if child.Start < cursor {
			continue
		}
		filtered = append(filtered, child)
		if child.End > cursor {
			cursor = child.End
		}
	}
	return filtered
}

func isEOF(tk token.Token) bool { return tk.Kind == token.EOF }

// pairTriviaToTokens splits each trivia run into leading/trailing attachments.
//
// Osty emits a real token.NEWLINE token for each significant line terminator,
// so the line-ending `\n` is usually NOT trivia — it's its own token. Trivia
// only includes whitespace, comments, and the EXTRA newlines that make blank
// lines. The attachment rule reflects that structure:
//
//   - First non-EOF token: no previous token exists, so its entire preceding
//     trivia run is leading (handles shebang, BOM, file-leading comments).
//   - Previous token is NEWLINE: the run is on a logically new line, so the
//     whole run goes to the next token's leading (or to file tail if there
//     is no next token). A NEWLINE token never carries trailing trivia.
//   - Previous token is a normal token: the run stays on the same logical
//     line as the previous token (before the next NEWLINE token arrives),
//     with one carveout — if the run contains a TriviaDocComment, everything
//     from that doc comment onward flows forward to the next token's leading
//     so `///` always documents the following declaration.
func pairTriviaToTokens(toks []token.Token, trivias []Trivia) (leading, trailing [][]int, tailTrivia []int) {
	leading = make([][]int, len(toks))
	trailing = make([][]int, len(toks))

	lastNonEOF := -1
	for i, tk := range toks {
		if !isEOF(tk) {
			lastNonEOF = i
		}
	}

	triIdx := 0
	prevNonEOF := -1
	for i, tk := range toks {
		if isEOF(tk) {
			break
		}
		// Collect the run of trivia that ends at or before this token's start.
		runStart := triIdx
		for triIdx < len(trivias) && trivias[triIdx].Offset+trivias[triIdx].Length <= tk.Pos.Offset {
			triIdx++
		}
		runEnd := triIdx

		switch {
		case prevNonEOF < 0:
			// First real token.
			for k := runStart; k < runEnd; k++ {
				leading[i] = append(leading[i], k)
			}
		case toks[prevNonEOF].Kind == token.NEWLINE:
			// Previous token ended the line — the run is on the next line.
			for k := runStart; k < runEnd; k++ {
				leading[i] = append(leading[i], k)
			}
		default:
			// Same-line trailing for the previous token, except that a doc
			// comment in the run peels the remainder forward.
			docAt := findFirstDocComment(trivias, runStart, runEnd)
			if docAt < 0 {
				for k := runStart; k < runEnd; k++ {
					trailing[prevNonEOF] = append(trailing[prevNonEOF], k)
				}
			} else {
				for k := runStart; k < docAt; k++ {
					trailing[prevNonEOF] = append(trailing[prevNonEOF], k)
				}
				for k := docAt; k < runEnd; k++ {
					leading[i] = append(leading[i], k)
				}
			}
		}
		prevNonEOF = i
	}

	// Tail: trivia after the last non-EOF token.
	if lastNonEOF >= 0 && triIdx < len(trivias) {
		if toks[lastNonEOF].Kind == token.NEWLINE {
			// The last real token ended a line — everything after is tail.
			for k := triIdx; k < len(trivias); k++ {
				tailTrivia = append(tailTrivia, k)
			}
		} else {
			docAt := findFirstDocComment(trivias, triIdx, len(trivias))
			if docAt < 0 {
				for k := triIdx; k < len(trivias); k++ {
					trailing[lastNonEOF] = append(trailing[lastNonEOF], k)
				}
			} else {
				for k := triIdx; k < docAt; k++ {
					trailing[lastNonEOF] = append(trailing[lastNonEOF], k)
				}
				for k := docAt; k < len(trivias); k++ {
					tailTrivia = append(tailTrivia, k)
				}
			}
		}
	} else {
		// No non-EOF tokens: everything is tail.
		for k := triIdx; k < len(trivias); k++ {
			tailTrivia = append(tailTrivia, k)
		}
	}
	return leading, trailing, tailTrivia
}

// findFirstDocComment returns the index of the first TriviaDocComment in
// trivias[lo:hi), or -1 if none. Doc comments are the only in-run split point
// — all other same-line trivia stays trailing of the previous token because
// Osty's NEWLINE tokens, not TriviaNewline, mark line boundaries.
func findFirstDocComment(trivias []Trivia, lo, hi int) int {
	for k := lo; k < hi; k++ {
		if trivias[k].Kind == TriviaDocComment {
			return k
		}
	}
	return -1
}

func emitToken(b *GreenBuilder, tk token.Token, src []byte, leadingIdx, trailingIdx []int, triviaIDs []int) {
	width := tk.End.Offset - tk.Pos.Offset
	if width < 0 {
		width = 0
	}
	text := ""
	if tk.Pos.Offset >= 0 && tk.End.Offset <= len(src) && tk.Pos.Offset <= tk.End.Offset {
		text = string(src[tk.Pos.Offset:tk.End.Offset])
	}
	b.Token(GkToken, int(tk.Kind), text, width,
		translateTriviaIDs(leadingIdx, triviaIDs),
		translateTriviaIDs(trailingIdx, triviaIDs))
}

func translateTriviaIDs(indices []int, triviaIDs []int) []int {
	if len(indices) == 0 {
		return nil
	}
	out := make([]int, len(indices))
	for i, idx := range indices {
		out[i] = triviaIDs[idx]
	}
	return out
}
