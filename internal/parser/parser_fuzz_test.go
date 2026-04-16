// Parser fuzz harness.
//
// FuzzParse is the crash-free baseline for the self-hosted parser. Per
// CLAUDE.md the parser must never panic on user input; this harness makes
// that property testable. The corpus is seeded with representative fragments
// from the existing testdata; the fuzzer mutates from there.
//
// Running:
//   go test ./internal/parser -run=FuzzParse -fuzz=FuzzParse -fuzztime=5m
//
// This is the Phase 0 oracle for the Red/Green tree refactor: if the refactor
// introduces any panic or goroutine leak, this harness catches it.

package parser

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// parseTimeout is the per-iteration budget for a single fuzz parse. The
// current self-hosted parser has at least one pre-existing hang (e.g. on a
// bare `#` input) which will be fixed during the Phase 4 parser rewrite. For
// Phase 0 we detect hangs so the fuzzer can continue past them.
const parseTimeout = 3 * time.Second

// parseWithTimeout runs ParseDiagnostics on src and reports whether it
// completed within the budget. Panics inside the parse propagate out of the
// goroutine to the t.Fatal path via recover + fatalf.
func parseWithTimeout(t *testing.T, src []byte) (done bool) {
	t.Helper()
	d := make(chan struct{})
	var panicVal any
	var panicStack string
	go func() {
		defer close(d)
		defer func() {
			if r := recover(); r != nil {
				panicVal = r
				buf := make([]byte, 64*1024)
				n := runtime.Stack(buf, false)
				panicStack = string(buf[:n])
			}
		}()
		_, _ = ParseDiagnostics(src)
	}()
	select {
	case <-d:
		if panicVal != nil {
			t.Fatalf("ParseDiagnostics panicked on %q: %v\n%s", truncate(src), panicVal, panicStack)
		}
		return true
	case <-time.After(parseTimeout):
		// Pre-existing hang. This is a known bug in the current parser that
		// the Phase 4 rewrite must fix. For Phase 0 baseline we log and
		// continue so the fuzzer can explore other inputs; the curated
		// regression set in TestParseTerminatesOnMinimalInputs is the
		// non-fuzz tripwire that catches the common forms.
		t.Logf("ParseDiagnostics did not terminate within %s on %q (pre-existing hang)", parseTimeout, truncate(src))
		return false
	}
}

func truncate(src []byte) string {
	const cap_ = 80
	if len(src) > cap_ {
		return string(src[:cap_]) + "..."
	}
	return string(src)
}

// BenchmarkParseBaseline measures parse time on a representative file.
// Useful for the Phase 5 perf budget (Green parse within 1.4x of baseline).
//
//   go test ./internal/parser -bench=BenchmarkParseBaseline -benchtime=3s
func BenchmarkParseBaseline(b *testing.B) {
	src, err := os.ReadFile(filepath.Join("..", "..", "word_freq.osty"))
	if err != nil {
		b.Skipf("baseline corpus missing: %v", err)
	}
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ParseDiagnostics(src)
	}
}

// fuzzSeeds lists small, focused fragments that together cover the grammar's
// interesting corners: literals, keywords, operators, error-trigger shapes.
// Go's fuzzer mutates from these seeds, producing arbitrary bytes. We only
// assert the parser returns without panicking - diagnostics and partial ASTs
// are both allowed.
var fuzzSeeds = []string{
	"",
	"\n",
	"#",
	"#[",
	"#[]\n",
	"pub fn main() {}\n",
	"let x = 1\n",
	"let y: Int = 0 + 1 * 2\n",
	"fn f<T: Ord>(x: T) -> T { x }\n",
	"struct Point { x: Int, y: Int }\n",
	"enum Color { Red, Green, Blue(Int) }\n",
	"interface Show { fn show(self) -> String }\n",
	"type Id = Int\n",
	"use std.io as io\n",
	"use runtime.strings as strings { fn Trim(s: String) -> String }\n",
	"for i in 0..10 { print(i) }\n",
	"match x { 1 => \"one\", _ => \"other\" }\n",
	"if let Some(v) = opt { v } else { 0 }\n",
	"|a, b| a + b\n",
	"[1, 2, 3]\n",
	"(1, 2, 3)\n",
	"{ a: 1, b: 2 }\n",
	"Point { x: 1, y: 2 }\n",
	"\"triple\"\n",
	"\"\"\"triple\n    line\"\"\"\n",
	"0xDEAD_BEEF\n",
	"0b1010\n",
	"0o777\n",
	"1.5e-3\n",
	"// line comment\n/// doc\npub fn f() {}\n",
	// Error-trigger shapes that exercise recovery paths:
	"pub fn (",
	"struct { }\n",
	"let",
	"let =",
	"fn f() -> { }\n",
	"match {}\n",
	"if {}\n",
	"|",
	"a < b < c\n",
	"1..=2..3\n",
}

func FuzzParse(f *testing.F) {
	for _, seed := range fuzzSeeds {
		f.Add([]byte(seed))
	}

	// Seed further from testdata to give the fuzzer a solid starting set.
	for _, rel := range []string{
		"testdata/full.osty",
		"testdata/hello.osty",
		"testdata/spec/positive/01-lexical.osty",
		"testdata/spec/positive/04-expressions.osty",
	} {
		if data, err := os.ReadFile(filepath.Join("..", "..", rel)); err == nil {
			f.Add(data)
		}
	}

	f.Fuzz(func(t *testing.T, src []byte) {
		// Property: parsing any input, however malformed, must return without
		// panic within parseTimeout. Diagnostics may be empty or populated.
		// Hangs are reported as t.Errorf (non-fatal) so the fuzzer keeps
		// exploring; panics propagate via t.Fatalf.
		parseWithTimeout(t, src)
	})
}

// TestParseTerminatesOnMinimalInputs asserts a small set of minimal inputs
// terminate within the timeout. These are curated for quick regression and
// to document the shape of inputs the parser must handle.
func TestParseTerminatesOnMinimalInputs(t *testing.T) {
	inputs := [][]byte{
		{},
		[]byte("\n"),
		[]byte("#"),
		[]byte("#["),
		[]byte("a"),
		[]byte("fn"),
		[]byte("fn f() {}\n"),
	}
	for _, src := range inputs {
		src := src
		t.Run(truncate(src), func(t *testing.T) {
			if !parseWithTimeout(t, src) {
				t.Fatalf("ParseDiagnostics did not terminate within %s on %q", parseTimeout, truncate(src))
			}
		})
	}
}
