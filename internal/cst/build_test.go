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

// emitTreeBytes walks the tree in pre-order and concatenates each token's
// leading trivia bytes followed by its text. Result must equal tree.Source.
func emitTreeBytes(tree *cst.Tree) []byte {
	src := tree.Source
	out := make([]byte, 0, len(src))
	tree.Root().Walk(func(r cst.Red) bool {
		if !r.IsToken() {
			return true
		}
		tok := r.Token()
		for _, triID := range tok.LeadingTrivia {
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
		out = append(out, tok.Text...)
		return true
	})
	return out
}
