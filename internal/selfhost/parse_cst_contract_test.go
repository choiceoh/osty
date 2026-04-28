package selfhost

import (
	"testing"

	"github.com/osty/osty/internal/cst"
)

func TestParseCSTLosslessOnMalformedSource(t *testing.T) {
	src := []byte("fn main() {\r\n    let x = : // nope\r\n}\r\n")
	normalized := cst.Normalize(src)
	tree, diags := ParseCST(src)
	if tree == nil {
		t.Fatal("ParseCST returned nil tree")
	}
	if len(diags) == 0 {
		t.Fatal("ParseCST returned no diagnostics for malformed source")
	}
	if got := string(emitCSTBytesForSelfhostTest(tree)); got != string(normalized) {
		t.Fatalf("ParseCST round-trip mismatch:\nwant: %q\n got: %q", string(normalized), got)
	}
}

func TestParseCSTUsesExplicitPublicASTCompatibilityAdapter(t *testing.T) {
	src := []byte("fn main() {\n    let items = [1]\n    let count = len(items)\n}\n")
	ResetAstbridgeLowerCount()
	tree, diags := ParseCST(src)
	if len(diags) != 0 {
		t.Fatalf("ParseCST diagnostics = %#v, want none", diags)
	}
	if tree == nil {
		t.Fatal("ParseCST returned nil tree")
	}
	if got := AstbridgeLowerCount(); got != 0 {
		t.Fatalf("ParseCST FrontendRun.File count = %d, want 0", got)
	}
}

func emitCSTBytesForSelfhostTest(tree *cst.Tree) []byte {
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
