package diag

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/token"
)

// TestFormatterUsesDiagFileHeader verifies the location header reflects
// d.File when it is set, even when the Formatter was seeded with a
// different default Filename.
func TestFormatterUsesDiagFileHeader(t *testing.T) {
	d := &Diagnostic{
		Severity: Error,
		Code:     "E0500",
		Message:  "undefined name `foo`",
		Spans: []LabeledSpan{{
			Span:    Span{Start: token.Pos{Line: 3, Column: 5}, End: token.Pos{Line: 3, Column: 8}},
			Label:   "not in scope",
			Primary: true,
		}},
		File: "pkg/b.osty",
	}
	f := &Formatter{Filename: "pkg/a.osty"}
	out := f.Format(d)
	if !strings.Contains(out, "pkg/b.osty:3:5") {
		t.Fatalf("header missing d.File path; got:\n%s", out)
	}
	if strings.Contains(out, "pkg/a.osty") {
		t.Fatalf("header leaked default Filename when d.File is set; got:\n%s", out)
	}
}

// TestFormatterSnippetPicksSourcesMap verifies that when Sources maps
// d.File to raw bytes, the snippet renders from the right file even if
// the Formatter's default Source is a different file.
func TestFormatterSnippetPicksSourcesMap(t *testing.T) {
	aSrc := []byte("line-a-1\nline-a-2\nline-a-3\n")
	bSrc := []byte("line-b-1\nline-b-2\nline-b-3\n")

	d := &Diagnostic{
		Severity: Error,
		Code:     "E0500",
		Message:  "undefined name `x`",
		Spans: []LabeledSpan{{
			Span:    Span{Start: token.Pos{Offset: 9, Line: 2, Column: 1}, End: token.Pos{Offset: 17, Line: 2, Column: 9}},
			Label:   "not in scope",
			Primary: true,
		}},
		File: "pkg/b.osty",
	}
	f := &Formatter{
		Filename: "pkg/a.osty",
		Source:   aSrc,
		Sources:  map[string][]byte{"pkg/a.osty": aSrc, "pkg/b.osty": bSrc},
	}
	out := f.Format(d)
	if !strings.Contains(out, "line-b-2") {
		t.Fatalf("snippet did not come from d.File's Source; got:\n%s", out)
	}
	if strings.Contains(out, "line-a-2") {
		t.Fatalf("snippet leaked from default Source; got:\n%s", out)
	}
}

// TestFormatterOmitsSnippetWhenFileMissing verifies the renderer does
// not quote a wrong file when d.File is set but Sources has no entry
// for it — it should still show the right header and no snippet.
func TestFormatterOmitsSnippetWhenFileMissing(t *testing.T) {
	d := &Diagnostic{
		Severity: Error,
		Message:  "missing source",
		Spans: []LabeledSpan{{
			Span:    Span{Start: token.Pos{Line: 1, Column: 1}, End: token.Pos{Line: 1, Column: 2}},
			Primary: true,
		}},
		File: "pkg/b.osty",
	}
	f := &Formatter{
		Filename: "pkg/a.osty",
		Source:   []byte("bogus content from a\n"),
	}
	out := f.Format(d)
	if !strings.Contains(out, "pkg/b.osty:1:1") {
		t.Fatalf("header did not route to d.File; got:\n%s", out)
	}
	if strings.Contains(out, "bogus content from a") {
		t.Fatalf("quoted wrong file's source; got:\n%s", out)
	}
}
