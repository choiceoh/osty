package lsp

import "testing"

// TestResolveOverlapsDropsStrictOverlap verifies that an edit whose
// range starts before a kept edit ended is dropped, while an edit
// that starts exactly at the previous end is retained (adjacency is
// legal per LSP 3.17).
func TestResolveOverlapsDropsStrictOverlap(t *testing.T) {
	// First edit: replace chars 0-10 on line 0.
	// Second edit: replace chars 5-8 on line 0 — strict overlap, drop.
	// Third edit: replace chars 10-15 on line 0 — touches the first
	//             edit's end, keep.
	in := []TextEdit{
		{Range: mkRange(0, 0, 0, 10), NewText: "A"},
		{Range: mkRange(0, 5, 0, 8), NewText: "B"},
		{Range: mkRange(0, 10, 0, 15), NewText: "C"},
	}
	out := resolveOverlaps(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 surviving edits, got %d: %+v", len(out), out)
	}
	if out[0].NewText != "A" || out[1].NewText != "C" {
		t.Errorf("wrong survivors: %+v", out)
	}
}

// TestResolveOverlapsPrioritizesFirstSource verifies that when two
// edits share a start point and one is a pure insert at the same
// point, the first-inserted (stable-sort) wins. Source ordering
// encodes severity priority in the real caller.
func TestResolveOverlapsPrioritizesFirstSource(t *testing.T) {
	// Two pure inserts at the exact same point — only the first
	// survives.
	in := []TextEdit{
		{Range: mkRange(2, 4, 2, 4), NewText: "first"},
		{Range: mkRange(2, 4, 2, 4), NewText: "second"},
	}
	out := resolveOverlaps(in)
	if len(out) != 1 {
		t.Fatalf("expected 1 edit, got %d: %+v", len(out), out)
	}
	if out[0].NewText != "first" {
		t.Errorf("expected 'first' to win; got %q", out[0].NewText)
	}
}

// TestResolveOverlapsKeepsDisjointReorder confirms that two edits on
// the same line at distinct ranges are both kept, and that the output
// is sorted by start position even when the input was reverse-order.
func TestResolveOverlapsKeepsDisjointReorder(t *testing.T) {
	in := []TextEdit{
		{Range: mkRange(0, 20, 0, 25), NewText: "LATE"},
		{Range: mkRange(0, 0, 0, 5), NewText: "EARLY"},
	}
	out := resolveOverlaps(in)
	if len(out) != 2 {
		t.Fatalf("expected both edits, got %+v", out)
	}
	if out[0].NewText != "EARLY" || out[1].NewText != "LATE" {
		t.Errorf("expected sort by start; got %+v", out)
	}
}

func mkRange(sLine, sChar, eLine, eChar uint32) Range {
	return Range{
		Start: Position{Line: sLine, Character: sChar},
		End:   Position{Line: eLine, Character: eChar},
	}
}
