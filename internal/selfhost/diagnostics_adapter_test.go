package selfhost

import (
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

func TestDedupeDiagnosticsSmallSlice(t *testing.T) {
	dupA := diag.New(diag.Error, "first").Primary(diag.Span{
		Start: token.Pos{Offset: 3, Line: 1, Column: 4},
		End:   token.Pos{Offset: 4, Line: 1, Column: 5},
	}, "").Build()
	dupB := diag.New(diag.Warning, "second").Primary(diag.Span{
		Start: token.Pos{Offset: 3, Line: 1, Column: 4},
		End:   token.Pos{Offset: 6, Line: 1, Column: 7},
	}, "").Build()
	other := diag.New(diag.Error, "other").Primary(diag.Span{
		Start: token.Pos{Offset: 9, Line: 2, Column: 1},
		End:   token.Pos{Offset: 10, Line: 2, Column: 2},
	}, "").Build()

	got := dedupeDiagnostics([]*diag.Diagnostic{dupA, nil, dupB, other})
	if len(got) != 2 {
		t.Fatalf("len(dedupeDiagnostics(...)) = %d, want 2", len(got))
	}
	if got[0] != dupA {
		t.Fatalf("first diagnostic = %#v, want first duplicate representative", got[0])
	}
	if got[1] != other {
		t.Fatalf("second diagnostic = %#v, want distinct diagnostic", got[1])
	}
}

func TestDedupeDiagnosticsSingleNil(t *testing.T) {
	got := dedupeDiagnostics([]*diag.Diagnostic{nil})
	if len(got) != 0 {
		t.Fatalf("len(dedupeDiagnostics([nil])) = %d, want 0", len(got))
	}
}
