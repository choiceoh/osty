package lexer

import (
	"testing"

	"github.com/osty/osty/internal/token"
)

// kinds extracts the token kinds from a token slice, omitting any trailing
// NEWLINEs (inserted at EOF by the terminator-insertion rule) and the EOF
// sentinel itself.
func kinds(ts []token.Token) []token.Kind {
	out := make([]token.Kind, 0, len(ts))
	for _, t := range ts {
		if t.Kind == token.EOF {
			break
		}
		out = append(out, t.Kind)
	}
	for len(out) > 0 && out[len(out)-1] == token.NEWLINE {
		out = out[:len(out)-1]
	}
	return out
}

func lexStr(s string) []token.Token {
	return New([]byte(s)).Lex()
}

func TestLexIdentifiersAndKeywords(t *testing.T) {
	toks := lexStr("fn add let mut pub foo _bar _")
	got := kinds(toks)
	want := []token.Kind{
		token.FN, token.IDENT, token.LET, token.MUT, token.PUB, token.IDENT, token.IDENT, token.UNDERSCORE,
	}
	if !kindsEq(got, want) {
		t.Fatalf("kinds = %v; want %v", got, want)
	}
	if toks[1].Value != "add" {
		t.Fatalf("ident value = %q; want %q", toks[1].Value, "add")
	}
}

func TestLexNumbers(t *testing.T) {
	toks := lexStr("42 1_000_000 0xFF 0b1010 0o777 3.14 2.5e-3 1.0e10")
	want := []token.Kind{
		token.INT, token.INT, token.INT, token.INT, token.INT,
		token.FLOAT, token.FLOAT, token.FLOAT,
	}
	got := kinds(toks)
	if !kindsEq(got, want) {
		t.Fatalf("kinds = %v; want %v", got, want)
	}
}

func TestLexOperators(t *testing.T) {
	toks := lexStr("+ - * / % == != < > <= >= && || ! & | ^ ~ << >> = += -= *= /= %= &= |= ^= <<= >>= ? ?. ?? .. ..= -> <- . :: _ @")
	want := []token.Kind{
		token.PLUS, token.MINUS, token.STAR, token.SLASH, token.PERCENT,
		token.EQ, token.NEQ, token.LT, token.GT, token.LEQ, token.GEQ,
		token.AND, token.OR, token.NOT,
		token.BITAND, token.BITOR, token.BITXOR, token.BITNOT, token.SHL, token.SHR,
		token.ASSIGN,
		token.PLUSEQ, token.MINUSEQ, token.STAREQ, token.SLASHEQ, token.PERCENTEQ,
		token.BITANDEQ, token.BITOREQ, token.BITXOREQ, token.SHLEQ, token.SHREQ,
		token.QUESTION, token.QDOT, token.QQ, token.DOTDOT, token.DOTDOTEQ,
		token.ARROW, token.CHANARROW, token.DOT, token.COLONCOLON, token.UNDERSCORE, token.AT,
	}
	got := kinds(toks)
	if !kindsEq(got, want) {
		t.Fatalf("kinds = %v; want %v", got, want)
	}
}

func TestLexSimpleString(t *testing.T) {
	toks := lexStr(`"hello, world"`)
	if len(toks) < 1 || toks[0].Kind != token.STRING {
		t.Fatalf("expected STRING first, got %v", toks)
	}
	parts := toks[0].Parts
	if len(parts) != 1 || parts[0].Kind != token.PartText || parts[0].Text != "hello, world" {
		t.Fatalf("parts = %+v; want single text part", parts)
	}
}

func TestLexStringEscapes(t *testing.T) {
	toks := lexStr(`"a\nb\tc\"d\\e"`)
	if toks[0].Kind != token.STRING {
		t.Fatalf("kind = %v; want STRING", toks[0].Kind)
	}
	got := toks[0].Parts[0].Text
	want := "a\nb\tc\"d\\e"
	if got != want {
		t.Fatalf("text = %q; want %q", got, want)
	}
}

// TestLexStringUTF8 verifies that multi-byte runes in `"..."` strings
// round-trip into StringPart text without losing trailing bytes.
func TestLexStringUTF8(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`"a — b"`, "a — b"},                       // em-dash (3-byte UTF-8)
		{`"α β γ"`, "α β γ"},                       // 2-byte runes
		{`"한국어"`, "한국어"},                           // 3-byte runes
		{`"😀 hi"`, "😀 hi"},                         // 4-byte rune
		{"\"a\u00A0b\u2014c\"", "a\u00A0b\u2014c"}, // NBSP + em-dash
	}
	for _, c := range cases {
		toks := lexStr(c.src)
		if toks[0].Kind != token.STRING {
			t.Fatalf("kind = %v; want STRING for %q", toks[0].Kind, c.src)
		}
		if len(toks[0].Parts) != 1 || toks[0].Parts[0].Kind != token.PartText {
			t.Fatalf("parts = %+v for %q", toks[0].Parts, c.src)
		}
		if got := toks[0].Parts[0].Text; got != c.want {
			t.Errorf("src=%q text=%q; want %q", c.src, got, c.want)
		}
	}
}

// TestLexStringUTF8WithInterpolation: multi-byte runes flanking an
// interpolation segment must survive into the surrounding text parts.
func TestLexStringUTF8WithInterpolation(t *testing.T) {
	toks := lexStr(`"{a} — shape {b}"`)
	if toks[0].Kind != token.STRING {
		t.Fatalf("kind = %v; want STRING", toks[0].Kind)
	}
	// Expect: Expr(a), Text(" — shape "), Expr(b)
	parts := toks[0].Parts
	if len(parts) != 3 {
		t.Fatalf("parts len = %d; want 3: %+v", len(parts), parts)
	}
	if got := parts[1].Text; got != " — shape " {
		t.Errorf("middle text = %q; want %q", got, " — shape ")
	}
}

func TestLexTripleStringUTF8(t *testing.T) {
	src := "\"\"\"\n    한국어 — ok\n    \"\"\""
	toks := lexStr(src)
	if toks[0].Kind != token.STRING {
		t.Fatalf("kind = %v; want STRING", toks[0].Kind)
	}
	if len(toks[0].Parts) == 0 || toks[0].Parts[0].Kind != token.PartText {
		t.Fatalf("parts = %+v", toks[0].Parts)
	}
	if got, want := toks[0].Parts[0].Text, "한국어 — ok"; got != want {
		t.Errorf("text = %q; want %q", got, want)
	}
}

func TestLexRawStringUTF8(t *testing.T) {
	toks := lexStr(`r"한글 — raw"`)
	if toks[0].Kind != token.RAWSTRING {
		t.Fatalf("kind = %v; want RAWSTRING", toks[0].Kind)
	}
	if got, want := toks[0].Parts[0].Text, "한글 — raw"; got != want {
		t.Errorf("text = %q; want %q", got, want)
	}
}

// TestCommentsNotDuplicated guards against a bug where
// nextTokenSuppressesTerm — a peek-only helper — called
// skipSpacesAndComments without saving and restoring l.comments. Any
// block comment that sat between the token that triggered a peek and
// the next token ended up in the comments slice twice.
func TestCommentsNotDuplicated(t *testing.T) {
	src := "fn f() {\n    let x = 1\n    /* block */\n    x\n}\n"
	l := New([]byte(src))
	_ = l.Lex()
	blocks := 0
	for _, c := range l.Comments() {
		if c.Kind == token.CommentBlock {
			blocks++
		}
	}
	if blocks != 1 {
		t.Errorf("block comment count = %d; want 1", blocks)
	}
}

// TestLexIsDeterministic: two independent lexers over identical source
// must emit identical tokens and comments. Catches any stray global /
// cross-run state leak; the source includes doc comments which exercise
// the peek-and-restore path in nextTokenSuppressesTerm.
func TestLexIsDeterministic(t *testing.T) {
	src := `/// doc one
fn a() {}

/// doc two
fn b() {}

/// doc three
fn c() {}
`
	// Lex twice from independent lexers over identical input. Any
	// non-determinism in snapshot/restore surfaces as a mismatch
	// between the two comment lists or token kinds.
	l1 := New([]byte(src))
	toks1 := l1.Lex()
	l2 := New([]byte(src))
	toks2 := l2.Lex()

	if len(toks1) != len(toks2) {
		t.Fatalf("token counts differ: %d vs %d", len(toks1), len(toks2))
	}
	for i := range toks1 {
		if toks1[i].Kind != toks2[i].Kind {
			t.Errorf("token[%d] kind differs: %v vs %v", i, toks1[i].Kind, toks2[i].Kind)
		}
	}

	c1, c2 := l1.Comments(), l2.Comments()
	if len(c1) != len(c2) {
		t.Fatalf("comment counts differ: %d vs %d", len(c1), len(c2))
	}
	want := 3
	if len(c1) != want {
		t.Errorf("got %d doc comments; want %d", len(c1), want)
	}
	for i, c := range c1 {
		if c.Kind != token.CommentDoc {
			t.Errorf("comment[%d] kind = %v; want CommentDoc", i, c.Kind)
		}
	}
}

func TestLexCharUTF8(t *testing.T) {
	toks := lexStr(`'한' '—' '😀'`)
	want := []rune{'한', '—', '😀'}
	for i, r := range want {
		if toks[i].Kind != token.CHAR {
			t.Fatalf("kind[%d] = %v; want CHAR", i, toks[i].Kind)
		}
		got := []rune(toks[i].Value)
		if len(got) != 1 || got[0] != r {
			t.Errorf("char[%d] value = %q (%U); want %q (%U)", i, toks[i].Value, got, string(r), r)
		}
	}
}

func TestLexStringInterpolation(t *testing.T) {
	toks := lexStr(`"hi, {name}!"`)
	if toks[0].Kind != token.STRING {
		t.Fatalf("kind = %v; want STRING", toks[0].Kind)
	}
	parts := toks[0].Parts
	if len(parts) != 3 {
		t.Fatalf("parts = %+v; want 3", parts)
	}
	if parts[0].Kind != token.PartText || parts[0].Text != "hi, " {
		t.Fatalf("parts[0] = %+v", parts[0])
	}
	if parts[1].Kind != token.PartExpr {
		t.Fatalf("parts[1] kind = %v; want PartExpr", parts[1].Kind)
	}
	if len(parts[1].Expr) != 1 || parts[1].Expr[0].Kind != token.IDENT || parts[1].Expr[0].Value != "name" {
		t.Fatalf("parts[1] expr = %+v", parts[1].Expr)
	}
	if parts[2].Text != "!" {
		t.Fatalf("parts[2] text = %q", parts[2].Text)
	}
}

func TestLexStringInterpolationCall(t *testing.T) {
	toks := lexStr(`"items: {xs.join(\", \")}"`)
	if toks[0].Kind != token.STRING {
		t.Fatalf("kind = %v; want STRING", toks[0].Kind)
	}
	// The interpolation expression tokens should be: xs . join ( "..." , "..." )
	// Because we embedded an escaped string in the outer string, the inner
	// tokens should include STRING literals.
	parts := toks[0].Parts
	if len(parts) < 2 || parts[1].Kind != token.PartExpr {
		t.Fatalf("no expression part: %+v", parts)
	}
}

func TestLexRawString(t *testing.T) {
	toks := lexStr(`r"\d+\.\d+"`)
	if toks[0].Kind != token.RAWSTRING {
		t.Fatalf("kind = %v; want RAWSTRING", toks[0].Kind)
	}
	if got, want := toks[0].Parts[0].Text, `\d+\.\d+`; got != want {
		t.Fatalf("raw text = %q; want %q", got, want)
	}
}

func TestLexChar(t *testing.T) {
	toks := lexStr(`'A' '\n' '\u{1F600}'`)
	for i, t_ := range []token.Token{toks[0], toks[1], toks[2]} {
		if t_.Kind != token.CHAR {
			t.Fatalf("kind[%d] = %v; want CHAR", i, t_.Kind)
		}
	}
	if toks[0].Value != "A" {
		t.Fatalf("char A value = %q", toks[0].Value)
	}
	if toks[1].Value != "\n" {
		t.Fatalf("char \\n value = %q", toks[1].Value)
	}
}

func TestLexByte(t *testing.T) {
	toks := lexStr(`b'A'`)
	if toks[0].Kind != token.BYTE {
		t.Fatalf("kind = %v; want BYTE", toks[0].Kind)
	}
	if toks[0].Value != "A" {
		t.Fatalf("byte value = %q", toks[0].Value)
	}
}

func TestLexComments(t *testing.T) {
	src := `// line comment
42 /* block */ 7
/// doc comment
fn foo`
	toks := lexStr(src)
	got := kinds(toks)
	want := []token.Kind{
		token.INT, token.INT, token.NEWLINE, token.FN, token.IDENT,
	}
	if !kindsEq(got, want) {
		t.Fatalf("kinds = %v; want %v", got, want)
	}
}

func TestLexCommentTextAndLeadingDoc(t *testing.T) {
	src := "// line\n/* block */\n/// doc\nfn foo"
	l := New([]byte(src))
	toks := l.Lex()
	comments := l.Comments()
	if len(comments) != 3 {
		t.Fatalf("comments len = %d; want 3: %+v", len(comments), comments)
	}
	if comments[0].Kind != token.CommentLine || comments[0].Text != " line" {
		t.Fatalf("line comment = %+v; want text without // delimiter", comments[0])
	}
	if comments[1].Kind != token.CommentBlock || comments[1].Text != " block " {
		t.Fatalf("block comment = %+v; want text without /* */ delimiters", comments[1])
	}
	if comments[2].Kind != token.CommentDoc || comments[2].Text != " doc" {
		t.Fatalf("doc comment = %+v; want text without /// delimiter", comments[2])
	}
	for _, tok := range toks {
		if tok.Kind == token.FN {
			if tok.LeadingDoc != "doc" {
				t.Fatalf("fn leading doc = %q; want %q", tok.LeadingDoc, "doc")
			}
			return
		}
	}
	t.Fatal("no fn token found")
}

func TestLexNewlines(t *testing.T) {
	// Newline after `let x = 1` terminates the statement. After `=`, it
	// doesn't (binary operator expecting rhs). After `,`, it doesn't.
	src := `let x = 1
let y = 2
let z =
    3
fn f(
    a: Int,
    b: Int,
) { }`
	toks := lexStr(src)
	// Expect NEWLINEs only after `1` and `2` — and after `}`.
	var nlCount int
	for _, t_ := range toks {
		if t_.Kind == token.NEWLINE {
			nlCount++
		}
	}
	// Expected NEWLINEs after: `1`, `2`, `3`, and `}`.
	if nlCount != 4 {
		t.Fatalf("newline count = %d; want 4 (got %v)", nlCount, toks)
	}
}

func TestLexRangeVsDot(t *testing.T) {
	toks := lexStr(`0..10 0..=10 x.field`)
	got := kinds(toks)
	want := []token.Kind{
		token.INT, token.DOTDOT, token.INT,
		token.INT, token.DOTDOTEQ, token.INT,
		token.IDENT, token.DOT, token.IDENT,
	}
	if !kindsEq(got, want) {
		t.Fatalf("kinds = %v; want %v", got, want)
	}
}

func TestLexShebang(t *testing.T) {
	src := `#!/usr/bin/env osty
println("hi")
`
	toks := lexStr(src)
	got := kinds(toks)
	want := []token.Kind{
		token.IDENT, token.LPAREN, token.STRING, token.RPAREN,
	}
	if !kindsEq(got, want) {
		t.Fatalf("kinds = %v; want %v", got, want)
	}
}

// TestLexTripleStringInterpPosition is a regression test: the embedded
// {name} interpolation should report a position on line 4 of the source,
// not line 1 of a synthetic sub-buffer.
func TestLexTripleStringInterpPosition(t *testing.T) {
	src := "fn f() {\n    let s = \"\"\"\n        first line\n        name is {name}\n        \"\"\"\n}\n"
	toks := lexStr(src)
	// Find the STRING token.
	var sTok token.Token
	for _, t_ := range toks {
		if t_.Kind == token.STRING {
			sTok = t_
			break
		}
	}
	if sTok.Kind != token.STRING {
		t.Fatal("no STRING token found")
	}
	// Expect at least one PartExpr whose interpolation tokens have
	// line >= 4 (the line containing `{name}` in the source).
	found := false
	for _, p := range sTok.Parts {
		if p.Kind == token.PartExpr {
			for _, tok := range p.Expr {
				if tok.Pos.Line >= 4 {
					found = true
					break
				}
			}
		}
	}
	if !found {
		t.Fatalf("interpolation token has wrong line: want >=4, got tokens %+v", sTok.Parts)
	}
}

// TestLexInterpWithInnerString ensures an interpolated expression containing
// a nested string literal with a `}` in it lexes correctly without the
// outer `}` being terminated early.
func TestLexInterpWithInnerString(t *testing.T) {
	src := `"start: {foo("}")} end"`
	toks := lexStr(src)
	if toks[0].Kind != token.STRING {
		t.Fatalf("kind = %v", toks[0].Kind)
	}
	parts := toks[0].Parts
	// Expect: PartText "start: ", PartExpr [foo ( "}" )], PartText " end".
	if len(parts) != 3 {
		t.Fatalf("parts = %d; want 3 (got %+v)", len(parts), parts)
	}
	if parts[2].Text != " end" {
		t.Fatalf("tail = %q; want %q", parts[2].Text, " end")
	}
}

func TestLexTripleString(t *testing.T) {
	src := "let sql = \"\"\"\n    SELECT *\n    FROM users\n    WHERE id = {id}\n    \"\"\"\n"
	toks := lexStr(src)
	// Expect: LET IDENT ASSIGN STRING NEWLINE.
	got := kinds(toks)
	want := []token.Kind{token.LET, token.IDENT, token.ASSIGN, token.STRING}
	if !kindsEq(got, want) {
		t.Fatalf("kinds = %v; want %v", got, want)
	}
	parts := toks[3].Parts
	if len(parts) < 2 {
		t.Fatalf("triple string should have interpolation; parts = %+v", parts)
	}
}

// TestLexBOMStripped: a leading UTF-8 BOM (EF BB BF) is stripped so the
// next token is the real first token of the source. §1.1 does not
// mention BOMs but Windows editors occasionally insert them, and the
// file would otherwise be rejected as illegal bytes.
func TestLexBOMStripped(t *testing.T) {
	src := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`fn main() {}`)...)
	l := New(src)
	toks := l.Lex()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors after BOM: %v", errs)
	}
	if toks[0].Kind != token.FN {
		t.Fatalf("first token = %v; want FN (BOM should be stripped)", toks[0].Kind)
	}
	// Line/column of `fn` should still be 1:1, not 1:4 — offset counts
	// post-strip, so the user-facing position matches what the editor shows.
	if toks[0].Pos.Line != 1 || toks[0].Pos.Column != 1 {
		t.Errorf("fn pos = %v; want 1:1", toks[0].Pos)
	}
}

// TestLexBOMOnlyLeading: a U+FEFF inside a string literal is preserved
// (the stripping rule only fires for the three-byte prefix at offset 0).
func TestLexBOMOnlyLeading(t *testing.T) {
	src := "\"a\uFEFFb\""
	toks := lexStr(src)
	if toks[0].Kind != token.STRING {
		t.Fatalf("kind = %v; want STRING", toks[0].Kind)
	}
	if got, want := toks[0].Parts[0].Text, "a\uFEFFb"; got != want {
		t.Errorf("text = %q; want %q", got, want)
	}
}

// TestLexShebangUTF8: a shebang line that contains multi-byte runes in
// a trailing comment must not desynchronize column counting — the
// following token should still be at column 1 of line 2.
func TestLexShebangUTF8(t *testing.T) {
	src := "#!/usr/bin/env osty — 한글\nfn main() {}\n"
	l := New([]byte(src))
	toks := l.Lex()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	if toks[0].Kind != token.FN {
		t.Fatalf("first token = %v; want FN", toks[0].Kind)
	}
	if toks[0].Pos.Line != 2 || toks[0].Pos.Column != 1 {
		t.Errorf("fn pos = %v; want 2:1", toks[0].Pos)
	}
}

// TestLexEmptyCharLiteral: `”` is rejected with a dedicated
// "empty char literal" diagnostic rather than the confusing
// "expected closing '" path that used to swallow the closing quote
// as the char's value.
func TestLexEmptyCharLiteral(t *testing.T) {
	l := New([]byte(`''`))
	toks := l.Lex()
	errs := l.Errors()
	if len(errs) == 0 {
		t.Fatalf("expected a lex error for `''`, got none; tokens=%v", toks)
	}
	if toks[0].Kind != token.CHAR {
		t.Fatalf("kind = %v; want CHAR (recovery token)", toks[0].Kind)
	}
	found := false
	for _, e := range errs {
		if e.Message == "empty char literal" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no `empty char literal` diagnostic; got %v", errs)
	}
}

// TestLexEmptyByteLiteral: `b”` is rejected analogously to `”`.
func TestLexEmptyByteLiteral(t *testing.T) {
	l := New([]byte(`b''`))
	toks := l.Lex()
	errs := l.Errors()
	if len(errs) == 0 {
		t.Fatalf("expected a lex error for `b''`, got none; tokens=%v", toks)
	}
	if toks[0].Kind != token.BYTE {
		t.Fatalf("kind = %v; want BYTE (recovery token)", toks[0].Kind)
	}
	found := false
	for _, e := range errs {
		if e.Message == "empty byte literal" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no `empty byte literal` diagnostic; got %v", errs)
	}
}

// TestLexUnterminatedCharAtEOL: `'a` followed by a newline reports an
// unterminated-char diagnostic instead of swallowing the newline as
// the char's value and desynchronizing line numbers.
func TestLexUnterminatedCharAtEOL(t *testing.T) {
	src := "'\nlet d = 1\n"
	l := New([]byte(src))
	_ = l.Lex()
	errs := l.Errors()
	if len(errs) == 0 {
		t.Fatalf("expected a lex error for unterminated char, got none")
	}
	found := false
	for _, e := range errs {
		if e.Message == "unterminated char literal" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no `unterminated char literal` diagnostic; got %v", errs)
	}
}

// TestLexFatArrowRejected verifies that `=>` is reported as a lex error
// (§1.7 / OSTY_GRAMMAR_v0.3 O7: "`=>` is not a token. Any occurrence of
// `=>` in source is a lex error"). The lexer consumes both bytes and
// emits a single ILLEGAL token so recovery continues with the next real
// token rather than emitting a spurious `>`.
func TestLexFatArrowRejected(t *testing.T) {
	l := New([]byte(`match x { 0 => 1 }`))
	toks := l.Lex()
	errs := l.Errors()
	if len(errs) == 0 {
		t.Fatalf("expected a lex error for `=>`, got none; tokens=%v", toks)
	}
	var illegal *token.Token
	for i := range toks {
		if toks[i].Kind == token.ILLEGAL {
			illegal = &toks[i]
			break
		}
	}
	if illegal == nil {
		t.Fatalf("expected an ILLEGAL token for `=>`; got %v", toks)
	}
	if illegal.Value != "=>" {
		t.Errorf("illegal value = %q; want %q", illegal.Value, "=>")
	}
	// No stray `>` token should follow the ILLEGAL — both bytes were consumed.
	for i, tk := range toks {
		if tk.Kind == token.ILLEGAL && i+1 < len(toks) && toks[i+1].Kind == token.GT {
			t.Errorf("spurious GT after ILLEGAL `=>`: %v", toks)
		}
	}
}

func kindsEq(a, b []token.Kind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
