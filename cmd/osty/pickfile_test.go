package main

import (
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
)

// TestPickFileHonorsDFile covers the happy path: when a diagnostic
// carries d.File, pickFile routes by path and ignores the offset.
func TestPickFileHonorsDFile(t *testing.T) {
	pkg := &resolve.Package{Files: []*resolve.PackageFile{
		{Path: "/pkg/a.osty", Source: []byte("alpha alpha alpha\n")},
		{Path: "/pkg/b.osty", Source: []byte("beta\n")},
	}}
	d := &diag.Diagnostic{
		Message: "undefined name `zz`",
		Spans: []diag.LabeledSpan{{
			Span:    diag.Span{Start: token.Pos{Offset: 0, Line: 1, Column: 1}},
			Primary: true,
		}},
		File: "/pkg/b.osty",
	}
	if got := pickFile(pkg, d); got != 1 {
		t.Fatalf("pickFile: got %d, want 1 (b.osty)", got)
	}
}

// TestPickFileByNameDisambiguatesAcrossFiles verifies the name-content
// heuristic: when two files could match by offset, only the file whose
// bytes actually spell out the backticked name is picked.
func TestPickFileByNameDisambiguatesAcrossFiles(t *testing.T) {
	// Both files are long enough that offset 10 sits inside each. a.osty
	// has `alphaSym` at offset 10; b.osty has `xxxxxxxxxx` there. The
	// diagnostic references `alphaSym` — only a.osty should match.
	pkg := &resolve.Package{Files: []*resolve.PackageFile{
		{Path: "/pkg/a.osty", Source: []byte("0123456789alphaSymRest\n")},
		{Path: "/pkg/b.osty", Source: []byte("0123456789xxxxxxxxxxRest\n")},
	}}
	d := &diag.Diagnostic{
		Message: "undefined name `alphaSym`",
		Spans: []diag.LabeledSpan{{
			Span:    diag.Span{Start: token.Pos{Offset: 10, Line: 1, Column: 11}},
			Primary: true,
		}},
	}
	if got := pickFile(pkg, d); got != 0 {
		t.Fatalf("pickFile: got %d, want 0 (a.osty via name match)", got)
	}
}

// TestPickFileByNameSkipsAmbiguousMatches verifies that when multiple
// files have the same bytes at the same offset (rare but possible), the
// name heuristic declines to pick any — leaving the legacy offset
// fallback to decide, so we never silently guess.
func TestPickFileByNameSkipsAmbiguousMatches(t *testing.T) {
	pkg := &resolve.Package{Files: []*resolve.PackageFile{
		{Path: "/pkg/a.osty", Source: []byte("zzzzzzzzzzdupe\n")},
		{Path: "/pkg/b.osty", Source: []byte("zzzzzzzzzzdupe\n")},
	}}
	d := &diag.Diagnostic{
		Message: "undefined name `dupe`",
		Spans: []diag.LabeledSpan{{
			Span:    diag.Span{Start: token.Pos{Offset: 10, Line: 1, Column: 11}},
			Primary: true,
		}},
	}
	// Either file is a legitimate match — the legacy fallback picks the
	// first. The key property is that pickFile does not crash or return
	// -1, and that the chosen file is a real candidate.
	got := pickFile(pkg, d)
	if got != 0 && got != 1 {
		t.Fatalf("pickFile on ambiguous input: got %d, want 0 or 1", got)
	}
}

// TestBacktickedIdentsCollectsAll verifies the helper returns every
// backticked name in the message (not just the first), which is what
// lets the disambiguator handle messages like "cannot assign `Int` to
// `String`".
func TestBacktickedIdentsCollectsAll(t *testing.T) {
	got := backtickedIdents("cannot assign `Int` to `String`")
	if len(got) != 2 || got[0] != "Int" || got[1] != "String" {
		t.Fatalf("backtickedIdents: got %v, want [Int String]", got)
	}
}
