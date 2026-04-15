package parser

import (
	"testing"
	"unicode/utf8"
)

// FuzzParse ensures the parser never panics on arbitrary UTF-8 input and
// always terminates. Error output is ignored — we only care that the
// parser handles malformed input gracefully.
func FuzzParse(f *testing.F) {
	seeds := []string{
		// Happy-path snippets.
		"fn main() {}",
		"let x = 5\nlet y = x + 1",
		`fn f() -> Int { if cond { 1 } else { 2 } }`,
		`struct Point { x: Int, y: Int }`,
		`enum Shape { Circle(Float), Empty }`,
		`pub interface Writer { fn write(self, data: Bytes) -> Result<Int, Error> }`,
		`"hi, {name}!"`,
		`for i in 0..10 { println("{i}") }`,
		`match n { 0..=9 -> "s", _ -> "b" }`,
		`let sql = """\n  SELECT *\n  FROM t\n  """`,
		`/// doc\npub fn foo() {}`,
		`fn f() { ch <- value }`,
		// Known-reject inputs to exercise error paths.
		`fn broken( {`,
		`fn f() { a < b < c }`,        // non-assoc
		`fn f() { foo::bar() }`,       // missing turbofish
		`#[inline] pub fn f() {}`,     // unknown annotation
		`fn f() { 0X1F }`,             // uppercase base
		`use a/b.c`,                   // mixed use path
		"fn f() {}\n}\n",              // stray closing brace
		`fn f(t: Int = compute()) {}`, // non-literal default
		`"unterminated`,               // unterminated string
		`r"\d+`,                       // unterminated raw string
		`0b`,                          // incomplete base literal
		`match x { }`,                 // empty match
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		if !utf8.ValidString(src) {
			t.Skip()
		}
		if len(src) > 4096 {
			t.Skip()
		}
		// Parse must not panic. Errors are fine.
		_, _ = Parse([]byte(src))
	})
}
