package selfhost

import (
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

func TestDedupeDiagnosticsKeepsDistinctDiagnosticsAtSamePosition(t *testing.T) {
	span := diag.Span{
		Start: token.Pos{Offset: 4, Line: 1, Column: 5},
		End:   token.Pos{Offset: 5, Line: 1, Column: 6},
	}
	first := diag.New(diag.Error, "first parser error").Code("E0001").Primary(span, "").Build()
	second := diag.New(diag.Error, "second parser error").Code("E0002").Primary(span, "").Build()

	got := dedupeDiagnostics([]*diag.Diagnostic{first, second})
	if len(got) != 2 {
		t.Fatalf("dedupeDiagnostics len = %d, want 2: %#v", len(got), got)
	}
}

func TestDedupeDiagnosticsDropsOnlyIdenticalDiagnostics(t *testing.T) {
	span := diag.Span{
		Start: token.Pos{Offset: 4, Line: 1, Column: 5},
		End:   token.Pos{Offset: 5, Line: 1, Column: 6},
	}
	first := diag.New(diag.Error, "same parser error").
		Code("E0001").
		Primary(span, "").
		Hint("same hint").
		Note("same note").
		Build()
	second := diag.New(diag.Error, "same parser error").
		Code("E0001").
		Primary(span, "").
		Hint("same hint").
		Note("same note").
		Build()

	got := dedupeDiagnostics([]*diag.Diagnostic{first, second})
	if len(got) != 1 {
		t.Fatalf("dedupeDiagnostics len = %d, want 1: %#v", len(got), got)
	}
}

func TestParserDiagnosticLiftsCodeHintNoteAndSpan(t *testing.T) {
	src := []byte("fn main() {\n    let x = :\n}\n")
	run := Run(src)
	diags := run.Diagnostics()
	if len(diags) == 0 {
		t.Fatal("Diagnostics len = 0, want parser diagnostic")
	}
	d := diags[0]
	if d.Code != "E0204" {
		t.Fatalf("diagnostic code = %q, want E0204: %#v", d.Code, d)
	}
	if d.Message != "unexpected : in expression" {
		t.Fatalf("diagnostic message = %q, want stable parser-core message", d.Message)
	}
	if d.Hint == "" {
		t.Fatalf("diagnostic hint is empty: %#v", d)
	}
	if got := d.PrimaryPos(); got.Line != 2 || got.Column != 13 {
		t.Fatalf("diagnostic primary pos = %s, want 2:13", got)
	}
	if len(d.Spans) == 0 || !d.Spans[0].Primary || d.Spans[0].Span.End.Offset <= d.Spans[0].Span.Start.Offset {
		t.Fatalf("diagnostic span is not a positive token span: %#v", d.Spans)
	}
}

func TestParserDiagnosticLiftsNotes(t *testing.T) {
	src := []byte("fn main() {\n    if true {\n    }\n    else {\n    }\n}\n")
	run := Run(src)
	for _, d := range run.Diagnostics() {
		if d.Code != "E0105" {
			continue
		}
		if len(d.Notes) == 0 {
			t.Fatalf("E0105 notes are empty: %#v", d)
		}
		if d.Hint == "" {
			t.Fatalf("E0105 hint is empty: %#v", d)
		}
		return
	}
	t.Fatalf("missing E0105 diagnostic: %#v", run.Diagnostics())
}
