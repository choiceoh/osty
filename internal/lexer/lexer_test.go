package lexer

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/token"
)

// expectCode lexes src and asserts that `want` appears among the emitted
// diagnostic codes. Parser-level diagnostics are ignored — `Lexer.Errors()`
// surfaces only lex-stage codes, which is what we want for a focused lex
// assertion.
func expectCode(t *testing.T, src, want string) {
	t.Helper()
	l := New([]byte(src))
	_ = l.Lex()
	for _, d := range l.Errors() {
		if d.Code == want {
			return
		}
	}
	var got []string
	for _, d := range l.Errors() {
		got = append(got, d.Code+":"+d.Message)
	}
	t.Fatalf("expected %s, got [%s]", want, strings.Join(got, "; "))
}

// expectNoLexErrors asserts zero lex diagnostics for src.
func expectNoLexErrors(t *testing.T, src string) {
	t.Helper()
	l := New([]byte(src))
	_ = l.Lex()
	errs := l.Errors()
	if len(errs) == 0 {
		return
	}
	var got []string
	for _, d := range errs {
		got = append(got, d.Code+":"+d.Message)
	}
	t.Fatalf("expected no lex errors, got %d: [%s]", len(errs), strings.Join(got, "; "))
}

func TestLexUnterminatedString(t *testing.T) {
	expectCode(t, "let s = \"hello", "E0001")
	expectCode(t, "let s = \"hello\n", "E0001")
}

func TestLexUppercaseBasePrefix(t *testing.T) {
	expectCode(t, "let n = 0X1F", "E0002")
	expectCode(t, "let n = 0B101", "E0002")
	expectCode(t, "let n = 0O77", "E0002")
}

func TestLexUnknownEscape(t *testing.T) {
	// Unknown single-character escape.
	expectCode(t, `let s = "bad\q"`, "E0003")
	// Surrogate code point in \u{...}.
	expectCode(t, `let c = '\u{D800}'`, "E0003")
	// Out-of-range scalar.
	expectCode(t, `let c = '\u{110000}'`, "E0003")
}

func TestLexUnterminatedBlockComment(t *testing.T) {
	expectCode(t, "/* never closes", "E0004")
}

func TestLexIllegalCharacter(t *testing.T) {
	expectCode(t, "let x = 1 ⚡ 2", "E0005")
}

func TestLexBadTripleIndent(t *testing.T) {
	// Opening `"""` must be followed by a newline.
	expectCode(t, `let s = """oops"""`, "E0006")
}

func TestLexFatArrowRemoved(t *testing.T) {
	expectCode(t, "fn f() -> Int { match 0 { 0 => 1, _ => 2 } }", "E0007")
}

func TestLexBadNumericSeparatorTrailing(t *testing.T) {
	expectCode(t, "let a = 1_", "E0008")
	expectCode(t, "let a = 0xFF_", "E0008")
	expectCode(t, "let a = 0b1010_", "E0008")
	expectCode(t, "let a = 0o7_", "E0008")
}

func TestLexBadNumericSeparatorAfterPrefix(t *testing.T) {
	expectCode(t, "let a = 0x_FF", "E0008")
	expectCode(t, "let a = 0b_1010", "E0008")
	expectCode(t, "let a = 0o_777", "E0008")
}

func TestLexBadNumericSeparatorConsecutive(t *testing.T) {
	expectCode(t, "let a = 1__000", "E0008")
	expectCode(t, "let a = 0xAB__CD", "E0008")
}

func TestLexBadNumericSeparatorAroundFloatPunct(t *testing.T) {
	// `_` adjacent to `.` or `e` inside a numeric literal is invalid per
	// §1.6.1. Note: `1.5e_2` is NOT a numeric literal with bad separator —
	// the lexer splits it as FLOAT(`1.5`) + IDENT(`e_2`), so no E0008
	// is expected. We only assert the in-literal cases.
	expectCode(t, "let a = 1_.5", "E0008")
	expectCode(t, "let a = 1.5_e2", "E0008")
}

func TestLexValidNumericSeparatorsAccepted(t *testing.T) {
	// Must not regress: every permitted placement stays clean.
	for _, ok := range []string{
		"let a = 1_000",
		"let a = 1_000_000",
		"let a = 0xFF_FF",
		"let a = 0xDEAD_BEEF",
		"let a = 0b1010_1010",
		"let a = 0o7_7_7",
		"let a = 1_000.5",
		"let a = 1.5e1_0",
	} {
		expectNoLexErrors(t, ok)
	}
}

func TestLexEmptyCharOrByte(t *testing.T) {
	expectCode(t, "let a = ''", "E0009")
	expectCode(t, "let a = b''", "E0009")
}

func TestLexInterpolationNestingOk(t *testing.T) {
	// Nested braces inside an interpolation must not falsely close the string.
	expectNoLexErrors(t, `let s = "a {f({g: 1})} b"`)
}

// A string interpolation `"...{expr}..."` may contain nested string literals
// inside the expression. The outer scanner must recognize that an opening
// `{` enters expression context where a bare `"` starts a *new* string, not
// the outer close. Regression test for the original bug: the inner `"` was
// mis-read as the outer terminator, emitting E0001.
func TestLexInterpolationNestedStringOk(t *testing.T) {
	expectNoLexErrors(t, `let s = "a.{f(x, ".")}"`)
	expectNoLexErrors(t, `let s = "got.{std.strings.join(xs, ".")}"`)
}

// A `\"` inside the interpolation expression is treated as a 2-unit
// passthrough by the outer scanner — it must not let the `"` be confused
// with the outer string's close. The *outer* string's span must therefore
// cover the full `"a.{f(x, \", \")}"` range even though the inner
// tokenization of `\"` is an error (backslash is illegal in expression
// context). This locks in the outer-boundary invariant separately from
// the inner-diagnostic outcome.
func TestLexInterpolationEscapedQuoteSpansOuter(t *testing.T) {
	src := `let s = "a.{f(x, \", \")}"`
	l := New([]byte(src))
	toks := l.Lex()
	var str *token.Token
	for i := range toks {
		if toks[i].Kind == token.STRING {
			str = &toks[i]
			break
		}
	}
	if str == nil {
		t.Fatalf("no STRING token produced for %q", src)
	}
	// src is ASCII, so byte length equals rune-based column count.
	wantStart := 9
	wantEnd := len(src) + 1
	if str.Pos.Column != wantStart {
		t.Fatalf("outer STRING start column = %d; want %d", str.Pos.Column, wantStart)
	}
	if str.End.Column != wantEnd {
		t.Fatalf("outer STRING end column = %d; want %d (source of length %d)",
			str.End.Column, wantEnd, len(src))
	}
}

// A nested string that itself contains `}` must not close the outer
// interpolation — the `}` is inside the nested string's content, so it
// is invisible to the interpolation's brace tracking.
func TestLexInterpolationNestedStringWithBraceOk(t *testing.T) {
	expectNoLexErrors(t, `let s = "a.{f(x, "}")}"`)
}

// Same nested-string rule applies to triple-quoted strings. Triple strings
// require a leading newline per §1.6.3; content indent is stripped relative
// to the closing `"""` indent, so every line (content + close) uses a
// matching 4-space indent here.
func TestLexTripleInterpolationNestedStringOk(t *testing.T) {
	expectNoLexErrors(t, "let s = \"\"\"\n    { f(x, \".\") }\n    \"\"\"")
}

func TestLexTripleStringStripsIndent(t *testing.T) {
	src := "let s = \"\"\"\n    line1\n    line2\n    \"\"\""
	l := New([]byte(src))
	toks := l.Lex()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	var got *token.Token
	for i := range toks {
		if toks[i].Kind == token.STRING && toks[i].Triple {
			got = &toks[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("no triple STRING token; tokens=%v", toks)
	}
	if len(got.Parts) != 1 || got.Parts[0].Kind != token.PartText {
		t.Fatalf("expected one PartText, got parts=%+v", got.Parts)
	}
	// Per §1.6.3: common indent stripped, trailing newline before the
	// closing `"""` removed.
	want := "line1\nline2"
	if got.Parts[0].Text != want {
		t.Fatalf("triple body = %q; want %q", got.Parts[0].Text, want)
	}
}

func TestLexTripleStringDecodesEscapesAfterIndentNormalize(t *testing.T) {
	src := "let s = \"\"\"\n    line\\nnext\\{ok\\}\n    \"\"\""
	l := New([]byte(src))
	toks := l.Lex()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	var got *token.Token
	for i := range toks {
		if toks[i].Kind == token.STRING && toks[i].Triple {
			got = &toks[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("no triple STRING token; tokens=%v", toks)
	}
	if len(got.Parts) != 1 || got.Parts[0].Kind != token.PartText {
		t.Fatalf("expected one PartText, got parts=%+v", got.Parts)
	}
	want := "line\nnext{ok}"
	if got.Parts[0].Text != want {
		t.Fatalf("triple body = %q; want %q", got.Parts[0].Text, want)
	}
}

func TestLexCommentsAndErrorsAvailableWithoutLexCall(t *testing.T) {
	src := "// note\nlet s = \"bad\\q\"\n"
	l := New([]byte(src))
	if got := len(l.Comments()); got != 1 {
		t.Fatalf("Comments() count = %d; want 1", got)
	}
	errs := l.Errors()
	if len(errs) != 1 {
		t.Fatalf("Errors() count = %d; want 1", len(errs))
	}
	if errs[0].Code != "E0003" {
		t.Fatalf("Errors()[0].Code = %q; want E0003", errs[0].Code)
	}
}

func TestLexShebangAndBomIgnored(t *testing.T) {
	src := "\ufeff#!/usr/bin/env osty\nfn main() {}\n"
	l := New([]byte(src))
	toks := l.Lex()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	// First non-newline token should be `fn`.
	var first token.Kind
	for _, tk := range toks {
		if tk.Kind != token.NEWLINE {
			first = tk.Kind
			break
		}
	}
	if first != token.FN {
		t.Fatalf("first significant token = %s; want fn", first)
	}
}

func TestLexUTF8ColumnIsRunesBased(t *testing.T) {
	// `길` is one rune (3 bytes UTF-8); `🦀` is one scalar (4 bytes UTF-8,
	// surrogate-pair in UTF-16). Column counts must be in Unicode code
	// points, not bytes.
	src := `let s = "길🦀"`
	l := New([]byte(src))
	toks := l.Lex()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	var str *token.Token
	for i := range toks {
		if toks[i].Kind == token.STRING {
			str = &toks[i]
			break
		}
	}
	if str == nil {
		t.Fatalf("no STRING token produced")
	}
	// The opening `"` is at column 9 (after `let s = `).
	// The closing `"` should be at column 9 + 1 (open) + 2 (two scalars) + 1 = 13.
	// Content runes: 길, 🦀 → 2 runes regardless of byte length.
	if str.Pos.Column != 9 {
		t.Fatalf("STRING start column = %d; want 9", str.Pos.Column)
	}
	if str.End.Column != 13 {
		t.Fatalf("STRING end column = %d; want 13 (rune-based, not byte-based)", str.End.Column)
	}
}

func TestLexTokensSpansAreMonotonic(t *testing.T) {
	src := `let x = 42 + f("hi")` + "\n"
	l := New([]byte(src))
	toks := l.Lex()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	for i := 1; i < len(toks); i++ {
		prev, curr := toks[i-1], toks[i]
		if curr.Pos.Offset < prev.End.Offset {
			t.Fatalf("token %d (%s) starts at offset %d, before previous token %s ended at %d",
				i, curr.Kind, curr.Pos.Offset, prev.Kind, prev.End.Offset)
		}
	}
}
