package sourcemap

import (
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

func TestGeneratedSpanForOriginalPrefersLargestExactGeneratedSpan(t *testing.T) {
	original := diag.Span{
		Start: token.Pos{Offset: 10, Line: 1, Column: 11},
		End:   token.Pos{Offset: 20, Line: 1, Column: 21},
	}
	want := diag.Span{
		Start: token.Pos{Offset: 100, Line: 5, Column: 1},
		End:   token.Pos{Offset: 112, Line: 5, Column: 13},
	}
	sm := &Map{
		entries: []Entry{
			{
				Generated: diag.Span{
					Start: token.Pos{Offset: 100, Line: 5, Column: 1},
					End:   token.Pos{Offset: 108, Line: 5, Column: 9},
				},
				Original: original,
			},
			{
				Generated: want,
				Original:  original,
			},
		},
	}

	got, ok := sm.GeneratedSpanForOriginal(original)
	if !ok {
		t.Fatal("GeneratedSpanForOriginal() = false, want exact match")
	}
	if got != want {
		t.Fatalf("generated span = %#v, want %#v", got, want)
	}
}
