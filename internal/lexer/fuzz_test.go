package lexer

import (
	"testing"
	"unicode/utf8"

	"github.com/osty/osty/internal/token"
)

// FuzzLex ensures the lexer never panics, never infinite-loops, and always
// produces a terminating EOF token regardless of input. The fuzz corpus is
// seeded with the happy-path strings used in the unit tests.
func FuzzLex(f *testing.F) {
	seeds := []string{
		// Happy path.
		"fn add(a: Int, b: Int) -> Int { a + b }",
		`"hello, {name}!"`,
		"let xs = [1, 2, 3]",
		"0..=10",
		"\"\"\"\n    indent\n    \"\"\"",
		"match x { Some(n) if n > 0 -> n, _ -> 0 }",
		"r\"\\d+\\s*\"",
		"/// doc\nfn foo() {}",
		"|x, y| x + y",
		"use go \"net/http\" as http { fn Get(u: String) -> String }",
		// Error-path seeds.
		`"unterminated string`,
		`"bad escape \q"`,
		"0X1F",    // uppercase base
		"'ab'",    // multi-char char lit
		"\"hi, {", // unterminated interpolation
		"/* unterminated",
		"\"\"\"\nbad indent\n  \"\"\"", // bad triple-string
		"b'\\xFF'",                     // invalid byte escape
		"\uFEFF",                       // lone BOM
		"\x00\x01\x02",                 // binary noise
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		// Reject invalid UTF-8 inputs — Osty source must be UTF-8 per §1.1.
		if !utf8.ValidString(src) {
			t.Skip()
		}
		// A pathological 1 MiB input is out of scope for unit-level fuzz.
		if len(src) > 4096 {
			t.Skip()
		}
		// Run with a cap on total tokens as a cheap infinite-loop guard.
		l := New([]byte(src))
		const maxToks = 1 << 16
		count := 0
		for {
			tok := l.next()
			count++
			if tok.Kind == token.EOF {
				break
			}
			if count > maxToks {
				t.Fatalf("lexer produced >%d tokens for %d-byte input", maxToks, len(src))
			}
		}
	})
}
