package doctest

import (
	"testing"

	"github.com/osty/osty/internal/parser"
)

func TestExtractSingleBlock(t *testing.T) {
	src := []byte("/// Returns the first element.\n" +
		"///\n" +
		"/// ```osty\n" +
		"/// let xs = [1, 2, 3]\n" +
		"/// assertEq(xs.first(), Some(1))\n" +
		"/// ```\n" +
		"pub fn first<T>(xs: List<T>) -> T? { xs.first() }\n")
	file, _ := parser.ParseDiagnostics(src)
	if file == nil {
		t.Fatal("parse failed")
	}
	docs := Extract(file)
	if len(docs) != 1 {
		t.Fatalf("expected 1 doctest, got %d", len(docs))
	}
	got := docs[0]
	if got.Owner != "first" {
		t.Errorf("Owner: got %q, want first", got.Owner)
	}
	if got.OrdinalInOwner != 1 {
		t.Errorf("OrdinalInOwner: got %d, want 1", got.OrdinalInOwner)
	}
	want := "let xs = [1, 2, 3]\nassertEq(xs.first(), Some(1))"
	if got.Source != want {
		t.Errorf("Source: got %q, want %q", got.Source, want)
	}
}

func TestExtractMultipleBlocks(t *testing.T) {
	src := []byte("/// Adds two numbers.\n" +
		"///\n" +
		"/// ```osty\n" +
		"/// assertEq(add(1, 2), 3)\n" +
		"/// ```\n" +
		"///\n" +
		"/// Overflow is undefined:\n" +
		"/// ```osty\n" +
		"/// let _ = add(1, 2)\n" +
		"/// ```\n" +
		"pub fn add(a: Int, b: Int) -> Int { a + b }\n")
	file, _ := parser.ParseDiagnostics(src)
	docs := Extract(file)
	if len(docs) != 2 {
		t.Fatalf("expected 2 doctests, got %d", len(docs))
	}
	if docs[0].OrdinalInOwner != 1 || docs[1].OrdinalInOwner != 2 {
		t.Errorf("ordinals: got %d and %d, want 1 and 2",
			docs[0].OrdinalInOwner, docs[1].OrdinalInOwner)
	}
	if docs[0].Owner != "add" || docs[1].Owner != "add" {
		t.Errorf("owners: got %q and %q, want add twice", docs[0].Owner, docs[1].Owner)
	}
}

func TestExtractIgnoresNonOstyBlocks(t *testing.T) {
	src := []byte("/// Example output in shell form:\n" +
		"///\n" +
		"/// ```shell\n" +
		"/// $ osty run\n" +
		"/// ok\n" +
		"/// ```\n" +
		"///\n" +
		"/// Runnable form:\n" +
		"/// ```osty\n" +
		"/// assertEq(demo(), 7)\n" +
		"/// ```\n" +
		"pub fn demo() -> Int { 7 }\n")
	file, _ := parser.ParseDiagnostics(src)
	docs := Extract(file)
	if len(docs) != 1 {
		t.Fatalf("expected 1 doctest (osty only), got %d", len(docs))
	}
	if docs[0].Source != "assertEq(demo(), 7)" {
		t.Errorf("unexpected source: %q", docs[0].Source)
	}
}

func TestExtractIgnoresUntaggedFence(t *testing.T) {
	src := []byte("/// Example:\n" +
		"/// ```\n" +
		"/// this is just prose in a bare fence\n" +
		"/// ```\n" +
		"pub fn foo() -> Int { 1 }\n")
	file, _ := parser.ParseDiagnostics(src)
	docs := Extract(file)
	if len(docs) != 0 {
		t.Errorf("bare fence must not extract; got %d doctests", len(docs))
	}
}

func TestExtractCoversMultipleDecls(t *testing.T) {
	src := []byte("/// First.\n" +
		"/// ```osty\n" +
		"/// assertEq(a(), 1)\n" +
		"/// ```\n" +
		"pub fn a() -> Int { 1 }\n" +
		"\n" +
		"/// Second.\n" +
		"/// ```osty\n" +
		"/// assertEq(b(), 2)\n" +
		"/// ```\n" +
		"pub fn b() -> Int { 2 }\n")
	file, _ := parser.ParseDiagnostics(src)
	docs := Extract(file)
	if len(docs) != 2 {
		t.Fatalf("expected 2 doctests across 2 fns, got %d", len(docs))
	}
	owners := map[string]bool{}
	for _, d := range docs {
		owners[d.Owner] = true
	}
	if !owners["a"] || !owners["b"] {
		t.Errorf("expected owners a and b, got %v", owners)
	}
}

func TestExtractNoDocNoCrash(t *testing.T) {
	src := []byte("pub fn nodoc() -> Int { 42 }\n")
	file, _ := parser.ParseDiagnostics(src)
	docs := Extract(file)
	if len(docs) != 0 {
		t.Errorf("fn without doc yielded %d doctests", len(docs))
	}
}

// The self-hosted parser normalises doc comments by stripping
// the `///` prefix AND any leading whitespace on each line before
// attaching DocComment. As a result, indentation inside a fenced
// block is flattened during parse — code that relies on significant
// indentation (e.g. Python-style blocks) can't be doctested.
// Osty's block structure uses braces, so this flattening is lossy
// for code style but preserves semantics.
func TestExtractFlattensParserNormalizedIndent(t *testing.T) {
	src := []byte("/// Walks a tree.\n" +
		"/// ```osty\n" +
		"/// for node in nodes {\n" +
		"///     if node.active {\n" +
		"///         process(node)\n" +
		"///     }\n" +
		"/// }\n" +
		"/// ```\n" +
		"pub fn walk(nodes: List<Int>) { }\n")
	file, _ := parser.ParseDiagnostics(src)
	docs := Extract(file)
	if len(docs) != 1 {
		t.Fatalf("expected 1 doctest, got %d", len(docs))
	}
	// Parser-flattened — all leading spaces dropped. Test pins the
	// contract so a future parser change that preserves indent
	// trips this test and we notice.
	want := "for node in nodes {\nif node.active {\nprocess(node)\n}\n}"
	if docs[0].Source != want {
		t.Errorf("got:\n%s\nwant:\n%s", docs[0].Source, want)
	}
}

func TestExtractNilFile(t *testing.T) {
	if got := Extract(nil); got != nil {
		t.Errorf("Extract(nil) = %v, want nil", got)
	}
}
