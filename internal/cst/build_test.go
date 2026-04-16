package cst_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/cst"
	"github.com/osty/osty/internal/selfhost"
)

// TestBuildRoundTripCorpus is the Phase 4 round-trip: the Green tree produced
// by BuildFromParsed, when walked and concatenated, reconstructs the
// normalized source byte-for-byte for every corpus file.
func TestBuildRoundTripCorpus(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, rel := range corpus {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				t.Skipf("corpus missing: %v", err)
				return
			}
			src := cst.Normalize(raw)
			toks, _, _ := selfhost.Lex(src)
			trivias := cst.Extract(src, toks)
			file, _ := selfhost.Parse(src)
			if file == nil {
				t.Skip("parser returned nil file")
				return
			}
			tree := cst.BuildFromParsed(src, file, toks, trivias)
			got := emitTreeBytes(tree)
			if string(got) != string(src) {
				diff := firstDiff(src, got)
				t.Fatalf("%s: round-trip mismatch at byte %d\nwant: %q\n got: %q",
					rel, diff, preview(src, diff), preview(got, diff))
			}
		})
	}
}

// TestBuildTopLevelStructuring checks that entities get wrapped in their
// intended Green kind.
func TestBuildTopLevelStructuring(t *testing.T) {
	const source = `pub fn main() {
    let x = 1
}
`
	src := cst.Normalize([]byte(source))
	toks, _, _ := selfhost.Lex(src)
	trivias := cst.Extract(src, toks)
	file, _ := selfhost.Parse(src)
	tree := cst.BuildFromParsed(src, file, toks, trivias)

	var found bool
	tree.Root().Walk(func(r cst.Red) bool {
		if r.Kind() == cst.GkFnDecl {
			found = true
			text := string(src[r.Offset():r.End()])
			if !strings.Contains(text, "fn main") {
				t.Errorf("GkFnDecl text range does not cover 'fn main': %q", text)
			}
		}
		return true
	})
	if !found {
		t.Fatal("no GkFnDecl node found in tree for `pub fn main() {...}`")
	}
}

// TestBuildUseDeclStructuring verifies use-decls get their own structured
// node.
func TestBuildUseDeclStructuring(t *testing.T) {
	const source = `use std.io as io
use std.strings as strings

pub fn main() {}
`
	src := cst.Normalize([]byte(source))
	toks, _, _ := selfhost.Lex(src)
	trivias := cst.Extract(src, toks)
	file, _ := selfhost.Parse(src)
	tree := cst.BuildFromParsed(src, file, toks, trivias)

	useCount := 0
	tree.Root().Walk(func(r cst.Red) bool {
		if r.Kind() == cst.GkUseDecl {
			useCount++
		}
		return true
	})
	if useCount != 2 {
		t.Fatalf("expected 2 GkUseDecl nodes, found %d", useCount)
	}
}

// TestBuildLeadingTriviaAttachment sanity-checks that a leading comment is
// attached to the next real token, not dropped.
func TestBuildLeadingTriviaAttachment(t *testing.T) {
	const source = `// top comment
pub fn main() {}
`
	src := cst.Normalize([]byte(source))
	toks, _, _ := selfhost.Lex(src)
	trivias := cst.Extract(src, toks)
	file, _ := selfhost.Parse(src)
	tree := cst.BuildFromParsed(src, file, toks, trivias)

	first, ok := tree.Root().FirstToken()
	if !ok {
		t.Fatal("expected at least one real token")
	}
	tok := first.Token()
	sawComment := false
	for _, triID := range tok.LeadingTrivia {
		if tree.Arena.TriviaAt(triID).Kind == cst.TriviaLineComment {
			sawComment = true
			break
		}
	}
	if !sawComment {
		t.Fatal("first token's leading trivia has no TriviaLineComment")
	}
}

// TestBuildTrailingCommentInline checks that a same-line comment after a
// real (non-NEWLINE) token ends up as trailing trivia on that token. This is
// the central case the split policy exists to enable; pre-policy, the
// comment was buried in the next line's leading.
func TestBuildTrailingCommentInline(t *testing.T) {
	const source = `let x = 1 // hi
let y = 2
`
	tree := buildTreeFromSource(t, source)
	tokens := collectRealTokens(tree)

	oneTok, ok := findTokenByText(tokens, "1")
	if !ok {
		t.Fatal("expected a `1` token in the tree")
	}
	letIdx := findNthTokenByText(tokens, "let", 2)
	if letIdx < 0 {
		t.Fatal("expected a second `let` token in the tree")
	}
	secondLet := tokens[letIdx]

	gotTexts := triviaTexts(tree, oneTok.TrailingTrivia)
	gotKinds := triviaKinds(tree, oneTok.TrailingTrivia)
	if !containsKind(gotKinds, cst.TriviaLineComment) {
		t.Errorf("`1` trailing should contain the line comment; got kinds=%v texts=%q", gotKinds, gotTexts)
	}

	secondLeadKinds := triviaKinds(tree, secondLet.LeadingTrivia)
	if containsKind(secondLeadKinds, cst.TriviaLineComment) {
		t.Errorf("second `let` leading must not carry the inline comment; got kinds=%v", secondLeadKinds)
	}
}

// TestBuildTrailingNewlineTokenCarriesNoTrailing verifies the NEWLINE-token
// rule: trivia that sits after a NEWLINE token never becomes its trailing;
// it flows to the next token's leading. This keeps "the line that just
// ended" (the NEWLINE text alone) cleanly separated from "what's on the
// next line" (trivia in the following token's leading).
func TestBuildTrailingNewlineTokenCarriesNoTrailing(t *testing.T) {
	const source = `fn a() {}

fn b() {}
`
	tree := buildTreeFromSource(t, source)

	// Blank-line extra newline must land on the second fn's leading, never
	// on the NEWLINE token's trailing.
	var sawBlankLineOnFn bool
	var newlineTokensTrailed int
	tree.Root().Walk(func(r cst.Red) bool {
		if !r.IsToken() || r.Kind() != cst.GkToken {
			return true
		}
		tok := r.Token()
		if tok.Text == "\n" {
			if len(tok.TrailingTrivia) > 0 {
				newlineTokensTrailed++
			}
		}
		if tok.Text == "fn" && r.Offset() > 0 {
			if containsKind(triviaKinds(tree, tok.LeadingTrivia), cst.TriviaNewline) {
				sawBlankLineOnFn = true
			}
		}
		return true
	})

	if newlineTokensTrailed != 0 {
		t.Errorf("NEWLINE tokens must not carry trailing trivia; %d did", newlineTokensTrailed)
	}
	if !sawBlankLineOnFn {
		t.Error("second `fn` leading should carry the blank-line TriviaNewline")
	}
}

// TestBuildDocCommentAttachesToNext verifies the doc-comment carveout: `///`
// always stays with the following declaration, even if the previous real
// token would otherwise accumulate trailing trivia.
func TestBuildDocCommentAttachesToNext(t *testing.T) {
	const source = `let x = 1
/// doc
fn f() {}
`
	tree := buildTreeFromSource(t, source)
	tokens := collectRealTokens(tree)

	fnIdx := findNthTokenByText(tokens, "fn", 1)
	if fnIdx < 0 {
		t.Fatal("expected an `fn` token")
	}
	fnTok := tokens[fnIdx]

	fnLeadKinds := triviaKinds(tree, fnTok.LeadingTrivia)
	if !containsKind(fnLeadKinds, cst.TriviaDocComment) {
		t.Errorf("`fn` leading should own the doc comment; got kinds=%v", fnLeadKinds)
	}

	// No real token's trailing should swallow the doc comment.
	tree.Root().Walk(func(r cst.Red) bool {
		if !r.IsToken() || r.Kind() != cst.GkToken {
			return true
		}
		tok := r.Token()
		if containsKind(triviaKinds(tree, tok.TrailingTrivia), cst.TriviaDocComment) {
			t.Errorf("token %q trailing contains doc comment; doc must always lead the next decl", tok.Text)
		}
		return true
	})
}

// TestBuildTailTriviaAfterNewline verifies that trivia following a trailing
// NEWLINE token becomes file-tail trivia (parked under a GkEndOfFile leaf)
// rather than being attached as trailing of the NEWLINE. Round-trip must
// still reproduce the source byte-for-byte.
func TestBuildTailTriviaAfterNewline(t *testing.T) {
	const source = "fn f() {}\n// tail\n"
	tree := buildTreeFromSource(t, source)

	var newlineTrailedLineComment bool
	var eofLeafCarriesTail bool
	tree.Root().Walk(func(r cst.Red) bool {
		if !r.IsToken() {
			return true
		}
		switch r.Kind() {
		case cst.GkToken:
			tok := r.Token()
			if tok.Text == "\n" && containsKind(triviaKinds(tree, tok.TrailingTrivia), cst.TriviaLineComment) {
				newlineTrailedLineComment = true
			}
		case cst.GkEndOfFile:
			tok := r.Token()
			if containsKind(triviaKinds(tree, tok.LeadingTrivia), cst.TriviaLineComment) {
				eofLeafCarriesTail = true
			}
		}
		return true
	})
	if newlineTrailedLineComment {
		t.Error("NEWLINE token must not carry a trailing line-comment; it belongs to file tail")
	}
	if !eofLeafCarriesTail {
		t.Error("expected GkEndOfFile leaf to carry the tail `// tail` as leading trivia")
	}

	if got := string(emitTreeBytes(tree)); got != source {
		t.Fatalf("round-trip mismatch:\nwant: %q\n got: %q", source, got)
	}
}

// TestBuildEndOfFileIsLeaf verifies the structural properties of GkEndOfFile:
// zero-width, leaf, and not flagged as an error (the previous GkErrorMissing
// hack would have misreported this node as an error node).
func TestBuildEndOfFileIsLeaf(t *testing.T) {
	if !cst.GkEndOfFile.IsLeaf() {
		t.Error("GkEndOfFile must be a leaf kind")
	}
	if cst.GkEndOfFile.IsError() {
		t.Error("GkEndOfFile must not be classified as an error kind")
	}
}

// --- test helpers ---

func buildTreeFromSource(t *testing.T, source string) *cst.Tree {
	t.Helper()
	tree, _ := selfhost.ParseCST([]byte(source))
	if tree == nil {
		t.Fatal("ParseCST returned nil tree")
	}
	return tree
}

func collectRealTokens(tree *cst.Tree) []cst.GreenToken {
	var out []cst.GreenToken
	tree.Root().Walk(func(r cst.Red) bool {
		if r.IsToken() && r.Kind() == cst.GkToken {
			out = append(out, r.Token())
		}
		return true
	})
	return out
}

func findTokenByText(tokens []cst.GreenToken, text string) (cst.GreenToken, bool) {
	for _, tok := range tokens {
		if tok.Text == text {
			return tok, true
		}
	}
	return cst.GreenToken{}, false
}

func findNthTokenByText(tokens []cst.GreenToken, text string, n int) int {
	seen := 0
	for i, tok := range tokens {
		if tok.Text == text {
			seen++
			if seen == n {
				return i
			}
		}
	}
	return -1
}

func triviaTexts(tree *cst.Tree, ids []int) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		tri := tree.Arena.TriviaAt(id)
		lo, hi := tri.Offset, tri.Offset+tri.Length
		if lo < 0 || hi > len(tree.Source) {
			out = append(out, "<oob>")
			continue
		}
		out = append(out, string(tree.Source[lo:hi]))
	}
	return out
}

func triviaKinds(tree *cst.Tree, ids []int) []cst.TriviaKind {
	out := make([]cst.TriviaKind, 0, len(ids))
	for _, id := range ids {
		out = append(out, tree.Arena.TriviaAt(id).Kind)
	}
	return out
}

func containsKind(kinds []cst.TriviaKind, target cst.TriviaKind) bool {
	for _, k := range kinds {
		if k == target {
			return true
		}
	}
	return false
}

// emitTreeBytes walks the tree in pre-order and concatenates, for each token
// leaf: leading trivia + text + trailing trivia. Result must equal
// tree.Source. Trailing trivia is the addition over the pre-split-policy
// implementation; omitting it here would break round-trip as soon as any
// token carries same-line trailing trivia.
func emitTreeBytes(tree *cst.Tree) []byte {
	src := tree.Source
	out := make([]byte, 0, len(src))
	emitRun := func(indices []int) {
		for _, triID := range indices {
			tri := tree.Arena.TriviaAt(triID)
			lo, hi := tri.Offset, tri.Offset+tri.Length
			if lo < 0 {
				lo = 0
			}
			if hi > len(src) {
				hi = len(src)
			}
			if lo < hi {
				out = append(out, src[lo:hi]...)
			}
		}
	}
	tree.Root().Walk(func(r cst.Red) bool {
		if !r.IsToken() {
			return true
		}
		tok := r.Token()
		emitRun(tok.LeadingTrivia)
		out = append(out, tok.Text...)
		emitRun(tok.TrailingTrivia)
		return true
	})
	return out
}
