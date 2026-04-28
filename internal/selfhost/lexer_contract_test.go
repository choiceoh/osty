package selfhost

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/token"
)

func TestLexAdapterCopiesCanonicalFacts(t *testing.T) {
	src := "\ufeff" + strings.Join([]string{
		"#!/usr/bin/env osty",
		"/// doc one",
		"/// doc two",
		"pub fn main() {",
		"    // line comment",
		"    /* block */",
		`    let ch = '\x41'`,
		`    let b = b'\x42'`,
		`    let s = "hi\n{ch}\t"`,
		`    let raw = r"\n"`,
		`    let expr = "inner {'\x43'}"`,
		`    let tri = """`,
		`        left {ch}`,
		`        right`,
		`        """`,
		"}",
	}, "\r\n") + "\r\n"

	lexed, facts, rt := canonicalLexFacts(src)
	if strings.Contains(lexed.source, "\r") {
		t.Fatalf("normalized source still contains CR: %q", lexed.source)
	}
	if lexed.stream.bomStripped != 1 {
		t.Fatalf("bomStripped = %d; want 1", lexed.stream.bomStripped)
	}
	if lexed.stream.shebangs != 1 {
		t.Fatalf("shebangs = %d; want 1", lexed.stream.shebangs)
	}

	toks, diags, comments := Lex([]byte(src))
	if len(diags) != 0 {
		t.Fatalf("unexpected lex diagnostics: %+v", diags)
	}
	assertTokensMatchCanonicalFacts(t, toks, lexed.stream, facts, rt)
	assertCommentsMatchCanonicalFacts(t, comments, facts, rt)

	pub := firstTokenKind(t, toks, token.PUB)
	if pub.LeadingDoc != "doc one\ndoc two" {
		t.Fatalf("pub leading doc = %q; want joined doc lines", pub.LeadingDoc)
	}
	if got, want := pub.Pos.Offset, strings.Index(lexed.source, "pub"); got != want {
		t.Fatalf("pub offset = %d; want normalized-source byte offset %d", got, want)
	}
}

func TestLexResultStableTokenIDs(t *testing.T) {
	src := `/// doc
fn main() {
    let s = "hi {name}"
    let raw = r"raw"
}
`

	lexed, facts, _ := canonicalLexFacts(src)
	result := ostyLex(src)
	if got, want := ostyLexResultTokenCount(result), frontLexTokenCount(lexed.stream); got != want {
		t.Fatalf("rich token count = %d; want %d", got, want)
	}

	seen := map[int]bool{}
	for i := 0; i < frontLexTokenCount(lexed.stream); i++ {
		front := frontLexTokenAt(lexed.stream, i)
		rich := ostyLexResultTokenAt(result, i)
		if front.id <= 0 {
			t.Fatalf("front token %d has non-stable id %d", i, front.id)
		}
		if seen[front.id] {
			t.Fatalf("duplicate token id %d at token %d", front.id, i)
		}
		seen[front.id] = true
		if rich.id != front.id {
			t.Fatalf("rich token %d id = %d; want front token id %d", i, rich.id, front.id)
		}
	}

	str := firstFrontTokenKind(t, lexed.stream, FrontTokenKind(&FrontTokenKind_FrontString{}))
	parts := canonicalPartsForOwnerID(facts.stringParts, str.id)
	if len(parts) == 0 {
		t.Fatalf("missing string parts for stable owner id %d", str.id)
	}
	for _, part := range parts {
		if part.ownerTokenID != str.id {
			t.Fatalf("part ownerTokenID = %d; want %d", part.ownerTokenID, str.id)
		}
	}
}

func TestLexAdapterDiagnosticsCopyCanonicalFacts(t *testing.T) {
	src := strings.Join([]string{
		`let s = "bad\q"`,
		"let n = 0Xff",
		`let c = '\u{D800}'`,
		"let x = 1 => 2",
	}, "\n")

	_, facts, rt := canonicalLexFacts(src)
	_, diags, _ := Lex([]byte(src))
	if len(diags) != len(facts.errors) {
		t.Fatalf("diagnostic count = %d; want %d (%+v)", len(diags), len(facts.errors), diags)
	}
	for i, want := range facts.errors {
		got := diags[i]
		if got.Code != want.diagCode {
			t.Fatalf("diag %d code = %q; want %q", i, got.Code, want.diagCode)
		}
		if got.Message != want.message {
			t.Fatalf("diag %d message = %q; want %q", i, got.Message, want.message)
		}
		if got.Hint != want.hint {
			t.Fatalf("diag %d hint = %q; want %q", i, got.Hint, want.hint)
		}
		if len(got.Spans) == 0 {
			t.Fatalf("diag %d has no spans: %+v", i, got)
		}
		wantStart := token.Pos{Offset: rt.byteOffset(want.startOffset), Line: want.startLine, Column: want.startCol}
		wantEnd := token.Pos{Offset: rt.byteOffset(want.endOffset), Line: want.endLine, Column: want.endCol}
		if got.Spans[0].Span.Start != wantStart || got.Spans[0].Span.End != wantEnd {
			t.Fatalf("diag %d span = [%+v,%+v); want [%+v,%+v)",
				i, got.Spans[0].Span.Start, got.Spans[0].Span.End, wantStart, wantEnd)
		}
	}
}

func canonicalLexFacts(src string) (*OstyLexedSource, *OstyLexFacts, runeTable) {
	lexed := ostyLexSource(src)
	rt := newRuneTable(lexed.source)
	return lexed, ostyLexFactsFromStream(lexed.source, lexed.stream), rt
}

func assertTokensMatchCanonicalFacts(t *testing.T, toks []token.Token, stream *FrontLexStream, facts *OstyLexFacts, rt runeTable) {
	t.Helper()
	if got, want := len(toks), frontLexTokenCount(stream); got != want {
		t.Fatalf("token count = %d; want %d", got, want)
	}
	if got, want := len(facts.tokenTexts), frontLexTokenCount(stream); got != want {
		t.Fatalf("canonical token text count = %d; want %d", got, want)
	}
	if got, want := len(facts.interpolationTokenTexts), frontInterpolationTokenCount(stream); got != want {
		t.Fatalf("canonical interpolation token text count = %d; want %d", got, want)
	}
	for i := range toks {
		front := frontLexTokenAt(stream, i)
		if toks[i].Kind != mapTokenKind(front.kind) {
			t.Fatalf("token %d kind = %s; want %s", i, toks[i].Kind, mapTokenKind(front.kind))
		}
		if toks[i].Value != facts.tokenTexts[i] {
			t.Fatalf("token %d value = %q; want canonical text %q", i, toks[i].Value, facts.tokenTexts[i])
		}
		if toks[i].Pos != rt.pos(front.start) || toks[i].End != rt.pos(front.end) {
			t.Fatalf("token %d span = [%+v,%+v); want [%+v,%+v)",
				i, toks[i].Pos, toks[i].End, rt.pos(front.start), rt.pos(front.end))
		}
		if toks[i].LeadingDoc != adapterStringAt(facts.leadingDocs, i) {
			t.Fatalf("token %d leading doc = %q; want %q", i, toks[i].LeadingDoc, adapterStringAt(facts.leadingDocs, i))
		}
		if toks[i].Triple != front.triple {
			t.Fatalf("token %d triple = %v; want %v", i, toks[i].Triple, front.triple)
		}
		assertTokenPartsMatchCanonicalFacts(t, toks[i], i, stream, facts, rt)
	}
}

func assertTokenPartsMatchCanonicalFacts(t *testing.T, tok token.Token, owner int, stream *FrontLexStream, facts *OstyLexFacts, rt runeTable) {
	t.Helper()
	front := frontLexTokenAt(stream, owner)
	wantParts := canonicalPartsForOwnerID(facts.stringParts, front.id)
	if len(tok.Parts) != len(wantParts) {
		t.Fatalf("token %d parts = %d; want %d (%+v)", owner, len(tok.Parts), len(wantParts), tok.Parts)
	}
	for i, want := range wantParts {
		got := tok.Parts[i]
		if int(got.Kind) != want.kindCode {
			t.Fatalf("token %d part %d kind = %d; want %d", owner, i, got.Kind, want.kindCode)
		}
		if got.Text != want.text {
			t.Fatalf("token %d part %d text = %q; want %q", owner, i, got.Text, want.text)
		}
		if want.kindCode != int(token.PartExpr) {
			continue
		}
		if len(got.Expr) != want.exprTokenCount {
			t.Fatalf("token %d part %d expr token count = %d; want %d", owner, i, len(got.Expr), want.exprTokenCount)
		}
		for j, exprTok := range got.Expr {
			front := frontInterpolationTokenAt(stream, want.exprTokenStart+j).token
			wantValue := adapterStringAt(facts.interpolationTokenTexts, want.exprTokenStart+j)
			if exprTok.Kind != mapTokenKind(front.kind) || exprTok.Value != wantValue ||
				exprTok.Pos != rt.pos(front.start) || exprTok.End != rt.pos(front.end) {
				t.Fatalf("token %d part %d expr %d = (%s,%q,%+v,%+v); want (%s,%q,%+v,%+v)",
					owner, i, j, exprTok.Kind, exprTok.Value, exprTok.Pos, exprTok.End,
					mapTokenKind(front.kind), wantValue, rt.pos(front.start), rt.pos(front.end))
			}
		}
	}
}

func canonicalPartsForOwnerID(parts []*OstyLexStringPart, ownerID int) []*OstyLexStringPart {
	var out []*OstyLexStringPart
	for _, part := range parts {
		if part.ownerTokenID == ownerID {
			out = append(out, part)
		}
	}
	return out
}

func firstFrontTokenKind(t *testing.T, stream *FrontLexStream, kind FrontTokenKind) *FrontLexToken {
	t.Helper()
	for i := 0; i < frontLexTokenCount(stream); i++ {
		tok := frontLexTokenAt(stream, i)
		if ostyEqual(tok.kind, kind) {
			return tok
		}
	}
	t.Fatalf("missing front token kind %s", frontTokenKindName(kind))
	return nil
}

func assertCommentsMatchCanonicalFacts(t *testing.T, comments []token.Comment, facts *OstyLexFacts, rt runeTable) {
	t.Helper()
	if len(comments) != len(facts.comments) {
		t.Fatalf("comment count = %d; want %d", len(comments), len(facts.comments))
	}
	for i, want := range facts.comments {
		got := comments[i]
		wantPos := token.Pos{Offset: rt.byteOffset(want.startOffset), Line: want.startLine, Column: want.startCol}
		if got.Kind != token.CommentKind(want.kindCode) || got.Text != want.text || got.Pos != wantPos || got.EndLine != want.endLine {
			t.Fatalf("comment %d = %+v; want kind=%d text=%q pos=%+v endLine=%d",
				i, got, want.kindCode, want.text, wantPos, want.endLine)
		}
	}
}

func firstTokenKind(t *testing.T, toks []token.Token, kind token.Kind) token.Token {
	t.Helper()
	for _, tok := range toks {
		if tok.Kind == kind {
			return tok
		}
	}
	t.Fatalf("missing token kind %s in %v", kind, toks)
	return token.Token{}
}
