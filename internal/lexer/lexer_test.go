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

func firstTokenOfKind(t *testing.T, toks []token.Token, kind token.Kind) *token.Token {
	t.Helper()
	for i := range toks {
		if toks[i].Kind == kind {
			return &toks[i]
		}
	}
	t.Fatalf("missing %s token in %v", kind, toks)
	return nil
}

func firstDiagnosticWithCode(t *testing.T, diags []*Error, code string) *Error {
	t.Helper()
	for _, d := range diags {
		if d.Code == code {
			return d
		}
	}
	t.Fatalf("missing diagnostic %s in %+v", code, diags)
	return nil
}

func sourceLineColAt(src string, offset int) (int, int) {
	line, col := 1, 1
	for idx, r := range src {
		if idx >= offset {
			break
		}
		if r == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
	}
	return line, col
}

func TestLexUnterminatedString(t *testing.T) {
	expectCode(t, "let s = \"hello", "E0001")
	expectCode(t, "let s = \"hello\n", "E0001")
}

func TestLexDiagnosticContracts(t *testing.T) {
	tests := []struct {
		name        string
		src         string
		code        string
		message     string
		hint        string
		startNeedle string
		spanWidth   int
	}{
		{
			name:        "unterminated string",
			src:         `let s = "abc`,
			code:        "E0001",
			message:     "unterminated string literal",
			startNeedle: `"`,
			spanWidth:   len(`"abc`),
		},
		{
			name:        "uppercase base prefix",
			src:         "let n = 0X1",
			code:        "E0002",
			message:     "uppercase base prefix is not allowed",
			hint:        "use lowercase base prefixes: `0x`, `0b`, or `0o`",
			startNeedle: "0X",
			spanWidth:   len("0X"),
		},
		{
			name:        "unknown escape",
			src:         `let s = "bad\q"`,
			code:        "E0003",
			message:     "unknown escape sequence",
			startNeedle: `\q`,
			spanWidth:   len(`\q`),
		},
		{
			name:        "unterminated block comment",
			src:         "/* never closes",
			code:        "E0004",
			message:     "unterminated block comment",
			startNeedle: "/*",
			spanWidth:   len("/* never closes"),
		},
		{
			name:        "illegal character",
			src:         "let x = ⚡",
			code:        "E0005",
			message:     "illegal character",
			startNeedle: "⚡",
			spanWidth:   len("⚡"),
		},
		{
			name:        "bad triple shape",
			src:         `let s = """oops"""`,
			code:        "E0006",
			message:     "invalid triple-quoted string",
			startNeedle: `"""`,
			spanWidth:   len(`"""oops"""`),
		},
		{
			name:        "fat arrow removed",
			src:         "let x = 0 => 1",
			code:        "E0007",
			message:     "`=>` is not valid Osty syntax; use `->`",
			startNeedle: "=>",
			spanWidth:   len("=>"),
		},
		{
			name:        "bad numeric separator",
			src:         "let a = 1_",
			code:        "E0008",
			message:     "numeric separator `_` must appear between two digits",
			hint:        "place `_` only between two digits",
			startNeedle: "1_",
			spanWidth:   len("1_"),
		},
		{
			name:        "empty char",
			src:         "let a = ''",
			code:        "E0009",
			message:     "empty char literal",
			startNeedle: "''",
			spanWidth:   len("''"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := New([]byte(tt.src))
			_ = l.Lex()
			d := firstDiagnosticWithCode(t, l.Errors(), tt.code)
			if d.Message != tt.message {
				t.Fatalf("message = %q; want %q", d.Message, tt.message)
			}
			if d.Hint != tt.hint {
				t.Fatalf("hint = %q; want %q", d.Hint, tt.hint)
			}
			if len(d.Spans) == 0 {
				t.Fatalf("missing span: %+v", d)
			}
			start := strings.Index(tt.src, tt.startNeedle)
			if start < 0 {
				t.Fatalf("test bug: missing start needle %q", tt.startNeedle)
			}
			end := start + tt.spanWidth
			got := d.Spans[0].Span
			if got.Start.Offset != start || got.End.Offset != end {
				t.Fatalf("span offsets = [%d,%d); want [%d,%d)", got.Start.Offset, got.End.Offset, start, end)
			}
			startLine, startCol := sourceLineColAt(tt.src, start)
			endLine, endCol := sourceLineColAt(tt.src, end)
			if got.Start.Line != startLine || got.Start.Column != startCol ||
				got.End.Line != endLine || got.End.Column != endCol {
				t.Fatalf("span line/col = [%d:%d,%d:%d); want [%d:%d,%d:%d)",
					got.Start.Line, got.Start.Column, got.End.Line, got.End.Column,
					startLine, startCol, endLine, endCol)
			}
		})
	}
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
	src := "let x = 0 => 1"
	l := New([]byte(src))
	toks := l.Lex()
	var fat *token.Token
	for i := range toks {
		if toks[i].Kind == token.ILLEGAL && toks[i].Value == "=>" {
			fat = &toks[i]
			break
		}
	}
	if fat == nil {
		t.Fatalf("missing single ILLEGAL(`=>`) token in %v", toks)
	}
	if fat.End.Offset-fat.Pos.Offset != len("=>") {
		t.Fatalf("fat-arrow token span = [%d,%d), want width 2", fat.Pos.Offset, fat.End.Offset)
	}
	var saw bool
	for _, d := range l.Errors() {
		if d.Code == "E0007" {
			saw = true
			if len(d.Spans) == 0 || d.Spans[0].Span.Start.Offset != fat.Pos.Offset || d.Spans[0].Span.End.Offset != fat.End.Offset {
				t.Fatalf("E0007 span offsets = [%d,%d), want token span [%d,%d) (spans=%+v)",
					d.Spans[0].Span.Start.Offset, d.Spans[0].Span.End.Offset, fat.Pos.Offset, fat.End.Offset, d.Spans)
			}
		}
	}
	if !saw {
		t.Fatalf("expected E0007, got %v", l.Errors())
	}
}

func TestLexFatArrowIgnoredInStringAndComment(t *testing.T) {
	expectNoLexErrors(t, "let s = \"=>\"\n// =>\n")
}

func TestLexStringPartsAlreadyDecodedBySelfhost(t *testing.T) {
	src := `let s = "line\n\u{1F600}\{ok\}\""`
	l := New([]byte(src))
	toks := l.Lex()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	got := firstTokenOfKind(t, toks, token.STRING)
	if len(got.Parts) != 1 || got.Parts[0].Kind != token.PartText {
		t.Fatalf("expected one PartText, got parts=%+v", got.Parts)
	}
	want := "line\n😀{ok}\""
	if got.Parts[0].Text != want {
		t.Fatalf("string part text = %q; want %q", got.Parts[0].Text, want)
	}
}

func TestLexHexEscapesDecodedBySelfhost(t *testing.T) {
	src := `let s = "\x41"
let c = '\x42'
let b = b'\x43'`
	l := New([]byte(src))
	toks := l.Lex()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	str := firstTokenOfKind(t, toks, token.STRING)
	if len(str.Parts) != 1 || str.Parts[0].Text != "A" {
		t.Fatalf("string parts = %+v; want decoded A", str.Parts)
	}
	ch := firstTokenOfKind(t, toks, token.CHAR)
	if ch.Value != "B" {
		t.Fatalf("char value = %q; want B", ch.Value)
	}
	bt := firstTokenOfKind(t, toks, token.BYTE)
	if bt.Value != "C" {
		t.Fatalf("byte value = %q; want C", bt.Value)
	}
}

func TestLexInvalidUnicodeScalarsDecodeReplacement(t *testing.T) {
	src := `let surrogate = '\u{D800}'
let tooBig = '\u{110000}'`
	l := New([]byte(src))
	toks := l.Lex()
	if got := len(l.Errors()); got != 2 {
		t.Fatalf("lex error count = %d; want 2 (%+v)", got, l.Errors())
	}
	var values []string
	for _, tk := range toks {
		if tk.Kind == token.CHAR {
			values = append(values, tk.Value)
		}
	}
	if len(values) != 2 || values[0] != "\uFFFD" || values[1] != "\uFFFD" {
		t.Fatalf("char values = %q; want two replacement characters", values)
	}
}

func TestLexRawStringPartsStayRaw(t *testing.T) {
	src := `let s = r"\n\u{41}"`
	l := New([]byte(src))
	toks := l.Lex()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	got := firstTokenOfKind(t, toks, token.RAWSTRING)
	if len(got.Parts) != 1 || got.Parts[0].Kind != token.PartText {
		t.Fatalf("expected one PartText, got parts=%+v", got.Parts)
	}
	want := `\n\u{41}`
	if got.Parts[0].Text != want {
		t.Fatalf("raw string part text = %q; want %q", got.Parts[0].Text, want)
	}
}

func TestLexInterpolatedStringTextPartsAlreadyDecoded(t *testing.T) {
	src := `let s = "a\n{foo}\t"`
	l := New([]byte(src))
	toks := l.Lex()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	got := firstTokenOfKind(t, toks, token.STRING)
	if len(got.Parts) != 3 {
		t.Fatalf("part count = %d; want 3 (parts=%+v)", len(got.Parts), got.Parts)
	}
	if got.Parts[0].Kind != token.PartText || got.Parts[0].Text != "a\n" {
		t.Fatalf("first part = %+v; want decoded text %q", got.Parts[0], "a\n")
	}
	if got.Parts[1].Kind != token.PartExpr || len(got.Parts[1].Expr) == 0 {
		t.Fatalf("middle part = %+v; want non-empty interpolation expr", got.Parts[1])
	}
	if got.Parts[2].Kind != token.PartText || got.Parts[2].Text != "\t" {
		t.Fatalf("last part = %+v; want decoded tab", got.Parts[2])
	}
}

func TestLexCharAndByteValuesDecodedBySelfhost(t *testing.T) {
	src := `let c = '\u{1F600}'
let b = b'\u{41}'`
	l := New([]byte(src))
	toks := l.Lex()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	ch := firstTokenOfKind(t, toks, token.CHAR)
	if ch.Value != "😀" {
		t.Fatalf("char value = %q; want %q", ch.Value, "😀")
	}
	bt := firstTokenOfKind(t, toks, token.BYTE)
	if bt.Value != "A" {
		t.Fatalf("byte value = %q; want A", bt.Value)
	}
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

func TestLexBadNumericSeparatorInInterpolation(t *testing.T) {
	expectCode(t, `let s = "bad {1_}"`, "E0008")
	expectCode(t, `let s = "bad {0x_FF}"`, "E0008")
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

func TestLexAsQuestionSingleToken(t *testing.T) {
	src := "fn f(err: Error) { let cast = err as? FsError }\n"
	l := New([]byte(src))
	toks := l.Lex()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	var saw bool
	for _, tk := range toks {
		if tk.Kind == token.ASQUESTION {
			saw = true
			if tk.Value != "as?" {
				t.Fatalf("ASQUESTION token value = %q; want %q", tk.Value, "as?")
			}
		}
		if tk.Kind == token.IDENT && tk.Value == "as" {
			t.Fatalf("lexer split `as?` into IDENT(`as`) in %v", toks)
		}
	}
	if !saw {
		t.Fatalf("missing ASQUESTION token in %v", toks)
	}
}

func TestLexLoopLabelSingleToken(t *testing.T) {
	src := "fn f() { 'outer: for { continue 'outer } }\n"
	l := New([]byte(src))
	toks := l.Lex()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	var labels []token.Token
	for _, tk := range toks {
		if tk.Kind == token.LABEL {
			labels = append(labels, tk)
		}
	}
	if got, want := len(labels), 2; got != want {
		t.Fatalf("label token count = %d, want %d in %v", got, want, toks)
	}
	for _, tk := range labels {
		if tk.Value != "'outer" {
			t.Fatalf("LABEL token value = %q; want %q", tk.Value, "'outer")
		}
	}
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

func TestLexInterpolationNestedStringEscapeDiagnostics(t *testing.T) {
	expectNoLexErrors(t, `let s = "outer {f("ok\n")}"`)
	expectCode(t, `let s = "outer {f("bad\q")}"`, "E0003")
}

func TestLexInterpolationBackslashQuoteIsNotOuterEscape(t *testing.T) {
	expectCode(t, `let s = "outer {f(\"x\")}"`, "E0001")
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

func TestLexInterpolationNestedBlockCommentWithBraceOk(t *testing.T) {
	expectNoLexErrors(t, `let s = "a {f(/* } */ 1)} b"`)
}

func TestLexInterpolationRawCharByteWithBraceOk(t *testing.T) {
	expectNoLexErrors(t, `let s = "a {f(r"}", '}', b'}')} b"`)
}

func TestLexUnterminatedInterpolationSpan(t *testing.T) {
	src := `let s = "a {1`
	l := New([]byte(src))
	_ = l.Lex()
	wantStart := strings.IndexByte(src, '{')
	if wantStart < 0 {
		t.Fatal("test source missing interpolation start")
	}
	for _, d := range l.Errors() {
		if d.Code == "E0001" && d.Message == "unterminated interpolation in string" {
			if len(d.Spans) == 0 {
				t.Fatalf("unterminated interpolation diagnostic has no spans: %+v", d)
			}
			got := d.Spans[0].Span
			if got.Start.Offset != wantStart || got.End.Offset != len(src) {
				t.Fatalf("unterminated interpolation span = [%d,%d), want [%d,%d)",
					got.Start.Offset, got.End.Offset, wantStart, len(src))
			}
			return
		}
	}
	t.Fatalf("missing unterminated interpolation diagnostic; got %+v", l.Errors())
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

func TestLexTripleStringUsesClosingIndent(t *testing.T) {
	src := "let s = \"\"\"\n      keeps two spaces\n    \"\"\""
	l := New([]byte(src))
	toks := l.Lex()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	got := firstTokenOfKind(t, toks, token.STRING)
	if len(got.Parts) != 1 || got.Parts[0].Kind != token.PartText {
		t.Fatalf("expected one PartText, got parts=%+v", got.Parts)
	}
	want := "  keeps two spaces"
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
