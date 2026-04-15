package main

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/parser"
)

// TestDiscoverTests_ShapesAndPrefixes covers the two axes the
// `osty test` discovery contract cares about: signature shape (no
// params, no generics, no return type) and name prefix (`test` /
// `bench`). Every fn declared inside the source is classified; the
// table records exactly which entries should survive discovery.
func TestDiscoverTests_ShapesAndPrefixes(t *testing.T) {
	src := `fn testBasic() {
    let _ = 1
}

fn testWithParam(x: Int) {
    let _ = x
}

fn testWithReturn() -> Int {
    42
}

fn testWithGenerics<T>() {
    let _ = 0
}

fn benchBasic() {
    let _ = 1
}

fn notATest() {
    let _ = 1
}

fn test() {
    let _ = 1
}
`
	file, diags := parser.ParseDiagnostics([]byte(src))
	for _, d := range diags {
		if d.Severity.String() == "error" {
			t.Fatalf("unexpected parse error: %s", d.Message)
		}
	}
	entries := discoverTests(file, "inline.osty")

	want := map[string]testKind{
		"testBasic": kindTest,
		"benchBasic": kindBench,
		"test":       kindTest,
	}
	if len(entries) != len(want) {
		t.Fatalf("expected %d discovered entries, got %d: %+v", len(want), len(entries), entries)
	}
	for _, e := range entries {
		wantKind, ok := want[e.name]
		if !ok {
			t.Errorf("unexpected discovery: %s (%s)", e.name, e.kind.label())
			continue
		}
		if e.kind != wantKind {
			t.Errorf("%s: kind = %s, want %s", e.name, e.kind.label(), wantKind.label())
		}
		if e.path != "inline.osty" {
			t.Errorf("%s: path = %q, want %q", e.name, e.path, "inline.osty")
		}
		if e.line == 0 {
			t.Errorf("%s: line = 0 (position unset)", e.name)
		}
	}
}

// TestClassifyTestName spot-checks the prefix rules in isolation so
// regressions show up at the classification layer rather than only via
// the AST-driven discoverTests path.
func TestClassifyTestName(t *testing.T) {
	cases := []struct {
		name   string
		wantOK bool
		want   testKind
	}{
		{"test", true, kindTest},
		{"testFoo", true, kindTest},
		{"testing", true, kindTest}, // any `test`-prefixed name qualifies
		{"bench", true, kindBench},
		{"benchParse", true, kindBench},
		{"Test", false, 0}, // case-sensitive per spec
		{"main", false, 0},
		{"_test", false, 0},
	}
	for _, c := range cases {
		kind, ok := classifyTestName(c.name)
		if ok != c.wantOK {
			t.Errorf("classifyTestName(%q): ok = %v, want %v", c.name, ok, c.wantOK)
			continue
		}
		if ok && kind != c.want {
			t.Errorf("classifyTestName(%q): kind = %s, want %s", c.name, kind.label(), c.want.label())
		}
	}
}

// TestIsDiscoverableSignature ensures every non-conforming shape is
// rejected. Paired with TestDiscoverTests_ShapesAndPrefixes so a
// future author who loosens the signature predicate can see which
// shape they're letting in.
func TestIsDiscoverableSignature(t *testing.T) {
	src := `fn plain() {
    let _ = 1
}

fn withParam(x: Int) {
    let _ = x
}

fn withReturn() -> Int {
    42
}

fn withGeneric<T>() {
    let _ = 1
}
`
	file, diags := parser.ParseDiagnostics([]byte(src))
	for _, d := range diags {
		if d.Severity.String() == "error" {
			t.Fatalf("unexpected parse error: %s", d.Message)
		}
	}
	want := map[string]bool{
		"plain":       true,
		"withParam":   false,
		"withReturn":  false,
		"withGeneric": false,
	}
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FnDecl)
		if !ok {
			continue
		}
		got := isDiscoverableSignature(fn)
		if want[fn.Name] != got {
			t.Errorf("isDiscoverableSignature(%s) = %v, want %v", fn.Name, got, want[fn.Name])
		}
	}
}
