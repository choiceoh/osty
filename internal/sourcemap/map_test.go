package sourcemap

import (
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/spanid"
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

func TestFromSourceTransformMapsEditedKeywordBackToOriginal(t *testing.T) {
	original := []byte("func main() {\n    return 1\n}\n")
	generated := []byte("fn main() {\n    return 1\n}\n")
	sm := FromSourceTransform(original, generated)
	if sm == nil {
		t.Fatal("FromSourceTransform returned nil")
	}

	got, ok := sm.RemapSpanProjected(diag.Span{
		Start: token.Pos{Offset: 0, Line: 1, Column: 1},
		End:   token.Pos{Offset: 2, Line: 1, Column: 3},
	})
	if !ok {
		t.Fatal("RemapSpan returned false")
	}
	if got.Start.Offset != 0 || got.End.Offset != 4 {
		t.Fatalf("remapped keyword offsets = %d..%d, want 0..4", got.Start.Offset, got.End.Offset)
	}
	if got.Start.Line != 1 || got.Start.Column != 1 || got.End.Line != 1 || got.End.Column != 5 {
		t.Fatalf("remapped keyword position = %#v, want line 1 columns 1..5", got)
	}

	got, ok = sm.RemapSpanProjected(diag.Span{
		Start: token.Pos{Offset: 3, Line: 1, Column: 4},
		End:   token.Pos{Offset: 7, Line: 1, Column: 8},
	})
	if !ok {
		t.Fatal("RemapSpan for suffix returned false")
	}
	if got.Start.Offset != 5 || got.End.Offset != 9 {
		t.Fatalf("remapped suffix offsets = %d..%d, want 5..9", got.Start.Offset, got.End.Offset)
	}
}

func TestComposeProjectsCanonicalSpansThroughSourceTransform(t *testing.T) {
	original := []byte("func main() {}\n")
	transformed := []byte("fn main() {}\n")
	transformMap := FromSourceTransform(original, transformed)
	if transformMap == nil {
		t.Fatal("FromSourceTransform returned nil")
	}

	builder := NewBuilder()
	builder.Add("keyword", 0, 2, diag.Span{
		Start: token.Pos{Offset: 0, Line: 1, Column: 1},
		End:   token.Pos{Offset: 2, Line: 1, Column: 3},
	})
	canonicalMap := builder.Build(transformed)
	composed := canonicalMap.Compose(transformMap)
	if composed == nil {
		t.Fatal("Compose returned nil")
	}

	got, ok := composed.RemapSpanProjected(diag.Span{
		Start: token.Pos{Offset: 0, Line: 1, Column: 1},
		End:   token.Pos{Offset: 2, Line: 1, Column: 3},
	})
	if !ok {
		t.Fatal("RemapSpanProjected returned false")
	}
	if got.Start.Offset != 0 || got.End.Offset != 4 {
		t.Fatalf("composed offsets = %d..%d, want 0..4", got.Start.Offset, got.End.Offset)
	}
}

func TestRemapSpanKeepsLegacyWholeEntrySemantics(t *testing.T) {
	sm := &Map{entries: []Entry{{
		Generated: diag.Span{
			Start: token.Pos{Offset: 10, Line: 1, Column: 11},
			End:   token.Pos{Offset: 20, Line: 1, Column: 21},
		},
		Original: diag.Span{
			Start: token.Pos{Offset: 100, Line: 2, Column: 1},
			End:   token.Pos{Offset: 120, Line: 2, Column: 21},
		},
	}}}

	got, ok := sm.RemapSpan(diag.Span{
		Start: token.Pos{Offset: 12, Line: 1, Column: 13},
		End:   token.Pos{Offset: 14, Line: 1, Column: 15},
	})
	if !ok {
		t.Fatal("RemapSpan returned false")
	}
	if got.Start.Offset != 100 || got.End.Offset != 120 {
		t.Fatalf("RemapSpan offsets = %d..%d, want whole entry 100..120", got.Start.Offset, got.End.Offset)
	}

	projected, ok := sm.RemapSpanProjected(diag.Span{
		Start: token.Pos{Offset: 12, Line: 1, Column: 13},
		End:   token.Pos{Offset: 14, Line: 1, Column: 15},
	})
	if !ok {
		t.Fatal("RemapSpanProjected returned false")
	}
	if projected.Start.Offset != 104 || projected.End.Offset != 108 {
		t.Fatalf("RemapSpanProjected offsets = %d..%d, want 104..108", projected.Start.Offset, projected.End.Offset)
	}
}

func TestRemapSpanPreservesSourceIdentityAndProvenance(t *testing.T) {
	originalID := spanid.SourceFileIDFor("/tmp/original.osty")
	generatedID := spanid.DerivedSourceFileID(originalID, spanid.ProvenanceCanonical, "canonical")
	original := diag.StampSpanSourceFileID(diag.Span{
		Start: token.Pos{Offset: 10, Line: 1, Column: 11},
		End:   token.Pos{Offset: 15, Line: 1, Column: 16},
	}, originalID)
	generated := diag.StampSpanSourceFileID(diag.Span{
		Start: token.Pos{Offset: 100, Line: 5, Column: 1},
		End:   token.Pos{Offset: 105, Line: 5, Column: 6},
	}, generatedID)
	sm := &Map{entries: []Entry{{
		Kind:      "ident",
		Generated: generated,
		Original:  original,
	}}}
	sm.StampFileIDs(originalID, generatedID)

	got, ok := sm.RemapSpan(generated)
	if !ok {
		t.Fatal("RemapSpan() = false")
	}
	if got.SourceFileID != originalID || got.ID == "" {
		t.Fatalf("remapped identity = (%q, %q), want original file and non-empty span", got.SourceFileID, got.ID)
	}
	provenance := spanid.ProvenanceEntries(got.Provenance)
	if len(provenance) == 0 {
		t.Fatalf("remapped provenance is empty: %#v", got)
	}
	last := provenance[len(provenance)-1]
	if last.Kind != spanid.ProvenanceCanonical || last.SourceFileID != generatedID {
		t.Fatalf("provenance = %#v, want canonical edge from generated file", last)
	}
}
