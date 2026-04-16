package parser

import (
	"testing"

	"github.com/osty/osty/internal/cst"
)

// TestParseCSTFacade is a smoke test for the parser.ParseCST facade. It
// verifies that the lifted tree is non-nil, carries the input source, and
// that a Green kind from the cst package (here GkFile) is reachable — i.e.
// the facade genuinely exposes the structured CST rather than returning
// some placeholder.
func TestParseCSTFacade(t *testing.T) {
	const source = "pub fn main() {\n    let x = 1\n}\n"
	tree, diags := ParseCST([]byte(source))
	if len(diags) > 0 {
		t.Fatalf("ParseCST returned %d diagnostics; first: %v", len(diags), diags[0])
	}
	if tree == nil {
		t.Fatal("ParseCST returned nil tree")
	}
	if string(tree.Source) != source {
		t.Fatalf("tree.Source mismatch: got %q", tree.Source)
	}

	var sawFile, sawFnDecl bool
	tree.Root().Walk(func(r cst.Red) bool {
		switch r.Kind() {
		case cst.GkFile:
			sawFile = true
		case cst.GkFnDecl:
			sawFnDecl = true
		}
		return true
	})
	if !sawFile {
		t.Error("expected a GkFile node at the tree root")
	}
	if !sawFnDecl {
		t.Error("expected a GkFnDecl node in the tree")
	}
}
