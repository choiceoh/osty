package selfhost

import (
	"testing"

	"github.com/osty/osty/internal/ast"
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

func TestParseCSTUsesSelfhostArenaEntitySpans(t *testing.T) {
	src := []byte("use std::{fs, io as io}\nfn main() {\n    let items = [1]\n    let count = len(items)\n}\n")
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
	if got := countCSTKind(tree, cst.GkUseDecl); got != 2 {
		t.Fatalf("CST grouped use decl count = %d, want 2", got)
	}
}

func TestParseCSTSemanticPublicAndLosslessSurfacesStayAligned(t *testing.T) {
	src := []byte("use std.fs as fs\n// keep this trivia\n/// docs for main\nfn main() {\n    println(fs.open)\n}\n")
	normalized := cst.Normalize(src)
	run := Run(src)
	if diags := run.Diagnostics(); len(diags) != 0 {
		t.Fatalf("Run diagnostics = %#v, want none", diags)
	}
	semantic := run.semanticAstFile()
	if semantic == nil || semantic.arena == nil {
		t.Fatal("semantic AstFile is nil")
	}
	public := LowerPublicFileFromRun(run)
	if public == nil {
		t.Fatal("public AST is nil")
	}
	if got, want := len(public.Uses), 1; got != want {
		t.Fatalf("public use count = %d, want %d", got, want)
	}
	fn, ok := public.Decls[0].(*ast.FnDecl)
	if !ok {
		t.Fatalf("public decl[0] = %T, want *ast.FnDecl", public.Decls[0])
	}
	if fn.DocComment == "" {
		t.Fatalf("public fn doc comment is empty")
	}

	tree, diags := ParseCSTFromRun(run, normalized)
	if len(diags) != 0 {
		t.Fatalf("ParseCSTFromRun diagnostics = %#v, want none", diags)
	}
	if got := string(emitCSTBytesForSelfhostTest(tree)); got != string(normalized) {
		t.Fatalf("CST round-trip mismatch:\nwant: %q\n got: %q", string(normalized), got)
	}
	if got := countCSTKind(tree, cst.GkUseDecl); got != len(public.Uses) {
		t.Fatalf("CST use decl count = %d, public AST uses = %d", got, len(public.Uses))
	}
	if got := countCSTKind(tree, cst.GkFnDecl); got != len(public.Decls) {
		t.Fatalf("CST fn decl count = %d, public AST decls = %d", got, len(public.Decls))
	}
	if got := countCSTTriviaKind(tree, cst.TriviaDocComment); got == 0 {
		t.Fatal("CST doc-comment trivia count = 0, want doc trivia preserved")
	}
	if got := countCSTTriviaKind(tree, cst.TriviaLineComment); got == 0 {
		t.Fatal("CST line-comment trivia count = 0, want comment trivia preserved")
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

func countCSTKind(tree *cst.Tree, kind cst.GreenKind) int {
	var count int
	tree.Root().Walk(func(r cst.Red) bool {
		if r.Kind() == kind {
			count++
		}
		return true
	})
	return count
}

func countCSTTriviaKind(tree *cst.Tree, kind cst.TriviaKind) int {
	var count int
	tree.Root().Walk(func(r cst.Red) bool {
		if !r.IsToken() {
			return true
		}
		tok := r.Token()
		for _, id := range tok.LeadingTrivia {
			if tree.Arena.TriviaAt(id).Kind == kind {
				count++
			}
		}
		for _, id := range tok.TrailingTrivia {
			if tree.Arena.TriviaAt(id).Kind == kind {
				count++
			}
		}
		return true
	})
	return count
}
