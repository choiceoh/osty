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

// BuildFromEntitySpans lifts top-level syntax spans plus a token stream into a
// Green tree wrapped in a Red Tree. The resulting tree carries:
//
//  1. Structural Green nodes for the file and every top-level entity
//     (GkFile, GkFnDecl, GkStructDecl, …). Nested declarations inside
//     struct/enum bodies are NOT yet broken out — that is a follow-up phase.
//  2. Flat token runs inside each top-level entity: expressions and
//     statements are not yet structured. The public Red API does not depend
//     on that structuring, so consumers can migrate when the native Green
//     parser (blocked on the self-host generator) lands.
//  3. Trivia attached as leading and trailing runs on tokens. Runs between
//     tokens split at the first newline or doc comment — see
//     pairTriviaToTokens for the exact rule. Tail trivia after the last
//     token sits under the root via a zero-width GkErrorMissing sentinel.
//
// Byte coverage: every source byte is reachable from the tree via either a
// token's text or a trivia record. TestBuildRoundTrip enforces this.
//
// This builder is the adapter path while toolchain/lossless_lex.osty and a
// native Green parser remain blocked on the self-host generator. A native
// parser can replace this call without changing Red consumers.
func BuildFromEntitySpans(src []byte, entities []EntitySpan, toks []token.Token, trivias []Trivia) *Tree {
	b := NewBuilder(nil)
	arena := b.Arena()

	triviaIDs := make([]int, len(trivias))
	for i, tr := range trivias {
		triviaIDs[i] = arena.AddTrivia(tr)
	}
	leading, trailing, tailTrivia := pairTriviaToTokens(toks, trivias)
	entities = append([]EntitySpan(nil), entities...)
	sort.SliceStable(entities, func(i, j int) bool {
		if entities[i].Start == entities[j].Start {
			return entities[i].End < entities[j].End
		}
		return entities[i].Start < entities[j].Start
	})

	b.StartNode(GkFile)
	tokIdx := 0
	for _, ent := range entities {
		startOff := ent.Start
		endOff := ent.End

		// Orphan tokens before the entity attach to the file root.
		for tokIdx < len(toks) && !isEOF(toks[tokIdx]) && toks[tokIdx].Pos.Offset < startOff {
			emitToken(b, toks[tokIdx], src, leading[tokIdx], trailing[tokIdx], triviaIDs)
			tokIdx++
		}
		b.StartNode(ent.Kind)
		for tokIdx < len(toks) && !isEOF(toks[tokIdx]) && toks[tokIdx].Pos.Offset < endOff {
			emitToken(b, toks[tokIdx], src, leading[tokIdx], trailing[tokIdx], triviaIDs)
			tokIdx++
		}
		b.FinishNode()
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
