package cst

import "testing"

// TestClassifyTriviaInline exercises each classifier branch directly. This
// test lives in package cst (not cst_test) so it can call the unexported
// classifyTrivia helper. Corpus-driven tests that import selfhost live in
// trivia_corpus_test.go as package cst_test.
func TestClassifyTriviaInline(t *testing.T) {
	cases := []struct {
		name         string
		src          string
		atFileStart  bool
		wantKind     TriviaKind
		wantConsumed int
	}{
		{"single space", " a", false, TriviaWhitespace, 1},
		{"tab run", "\t\t\t", false, TriviaWhitespace, 3},
		{"mixed whitespace", " \t ", false, TriviaWhitespace, 3},
		{"single newline", "\n", false, TriviaNewline, 1},
		{"newline run", "\n\n\n", false, TriviaNewline, 3},
		{"line comment", "// hello\n", false, TriviaLineComment, 8},
		{"line comment at eof", "// tail", false, TriviaLineComment, 7},
		{"doc comment", "/// hi\nx", false, TriviaDocComment, 6},
		{"quadruple-slash is line", "//// commented-out doc\n", false, TriviaLineComment, 22},
		{"block comment", "/* body */ x", false, TriviaBlockComment, 10},
		{"multi-line block", "/* one\n two\n */x", false, TriviaBlockComment, 15},
		{"unterminated block", "/* uh oh", false, TriviaUnterminatedBlockComment, 8},
		{"bom only at start", "\uFEFFabc", true, TriviaBom, 3},
		{"bom midfile is not trivia", "\uFEFFabc", false, 0, 0},
		{"shebang", "#!/usr/bin/env osty\n", true, TriviaShebang, 19},
		{"shebang midfile is not trivia", "#! ignored", false, 0, 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			kind, consumed := classifyTrivia([]byte(tc.src), 0, tc.atFileStart)
			if kind != tc.wantKind || consumed != tc.wantConsumed {
				t.Fatalf("classifyTrivia(%q, atFileStart=%v) = (%v, %d); want (%v, %d)",
					tc.src, tc.atFileStart, kind, consumed, tc.wantKind, tc.wantConsumed)
			}
		})
	}
}
