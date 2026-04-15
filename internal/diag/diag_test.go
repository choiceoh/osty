package diag

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/token"
)

func TestSeverityString(t *testing.T) {
	cases := []struct {
		s    Severity
		want string
	}{
		{Error, "error"},
		{Warning, "warning"},
		{Note, "note"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("Severity(%d).String() = %q; want %q", c.s, got, c.want)
		}
	}
}

func TestBuilder(t *testing.T) {
	span := Span{
		Start: token.Pos{Offset: 5, Line: 1, Column: 6},
		End:   token.Pos{Offset: 10, Line: 1, Column: 11},
	}
	d := New(Error, "expected `}`, got `else`").
		Code("E0105").
		Primary(span, "else here").
		Note("the if block was already closed").
		Hint("move else to the same line as `}`").
		Build()
	if d.Severity != Error {
		t.Errorf("severity = %v", d.Severity)
	}
	if d.Code != "E0105" {
		t.Errorf("code = %q", d.Code)
	}
	if len(d.Spans) != 1 || !d.Spans[0].Primary {
		t.Errorf("spans = %+v", d.Spans)
	}
	if len(d.Notes) != 1 || d.Hint == "" {
		t.Errorf("notes/hint missing: %+v", d)
	}
}

func TestPrimaryPos(t *testing.T) {
	d := New(Error, "x").
		Secondary(Span{Start: token.Pos{Line: 1}}, "first").
		Primary(Span{Start: token.Pos{Line: 5, Column: 3}}, "main").
		Build()
	got := d.PrimaryPos()
	if got.Line != 5 || got.Column != 3 {
		t.Errorf("PrimaryPos = %+v; want line=5 col=3", got)
	}
}

func TestFormatBasic(t *testing.T) {
	src := []byte("fn f() {\n    1 + 2\n}\n")
	d := New(Error, "headline").
		Code("E0123").
		Primary(Span{
			Start: token.Pos{Offset: 13, Line: 2, Column: 5},
			End:   token.Pos{Offset: 14, Line: 2, Column: 6},
		}, "label here").
		Note("explanatory note").
		Hint("try this fix").
		Build()
	f := &Formatter{Filename: "t.osty", Source: src, Color: false}
	out := f.Format(d)

	for _, want := range []string{
		"error[E0123]: headline",
		"--> t.osty:2:5",
		"|     1 + 2",
		"^ label here",
		"= note: explanatory note",
		"= help: try this fix",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestFormatNoSource(t *testing.T) {
	// Without Source, the snippet is omitted but headline+location remain.
	d := New(Error, "no snippet").
		Primary(Span{Start: token.Pos{Line: 3, Column: 7}}, "").
		Build()
	f := &Formatter{Filename: "t.osty", Source: nil, Color: false}
	out := f.Format(d)
	if !strings.Contains(out, "--> t.osty:3:7") {
		t.Errorf("missing location header: %s", out)
	}
	if strings.Contains(out, "|") {
		t.Errorf("snippet pipe should be absent without Source: %s", out)
	}
}

func TestFormatColorEscapes(t *testing.T) {
	d := New(Error, "x").
		Code("E0001").
		Primary(Span{Start: token.Pos{Line: 1, Column: 1}, End: token.Pos{Line: 1, Column: 2}}, "").
		Build()
	f := &Formatter{Source: []byte("x\n"), Color: true}
	out := f.Format(d)
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("expected ANSI escapes when Color=true; got:\n%s", out)
	}
	f.Color = false
	out2 := f.Format(d)
	if strings.Contains(out2, "\x1b[") {
		t.Errorf("expected no escapes when Color=false; got:\n%s", out2)
	}
}

func TestFormatAll(t *testing.T) {
	d1 := New(Error, "first").Primary(Span{Start: token.Pos{Line: 1}}, "").Build()
	d2 := New(Warning, "second").Primary(Span{Start: token.Pos{Line: 2}}, "").Build()
	f := &Formatter{Color: false}
	out := f.FormatAll([]*Diagnostic{d1, d2})
	if !strings.Contains(out, "error: first") || !strings.Contains(out, "warning: second") {
		t.Errorf("missing both diagnostics: %s", out)
	}
}

func TestErrorInterface(t *testing.T) {
	// Diagnostic implements error.
	var _ error = (*Diagnostic)(nil)
	d := New(Error, "boom").
		Code("E0001").
		Primary(Span{Start: token.Pos{Line: 5, Column: 3}}, "").
		Build()
	got := d.Error()
	for _, want := range []string{"E0001", "5:3", "boom"} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() = %q; missing %q", got, want)
		}
	}
}
