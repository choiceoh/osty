package main

import (
	"strings"
	"testing"
)

// TestParseCodesSimple runs the full parser against a synthetic codes.go
// fixture covering all the structural shapes the real file uses:
//
//   - phase-separator comment (`// Lexical.`) that Go's AST attaches
//     to the following spec as its Doc
//   - structured doc with summary + body + Spec + Example + Fix
//   - summary-only doc
//   - missing doc (forces TODO emission)
//   - two separate const() blocks with distinct headings
func TestParseCodesSimple(t *testing.T) {
	src := `package diag

const (
	// Lexical.

	// A string literal reaches end-of-file without a closing quote.
	//
	// Spec: v0.3 §1.6.1
	// Example:
	//   let s = "hello
	// Fix: add the closing quote.
	CodeUnterminatedString = "E0001"

	// Bare one-liner.
	CodeOther = "E0002"

	// Declarations.

	CodeNoDoc = "E0100"
)

const (
	// Lint.

	// A let binding never read.
	// Fix: remove it.
	CodeUnusedLet = "L0001"
)
`
	doc, err := parseCodes("fixture.go", []byte(src))
	if err != nil {
		t.Fatalf("parseCodes: %v", err)
	}
	if got := len(doc.Groups); got != 3 {
		t.Fatalf("groups: got %d, want 3", got)
	}

	// Group 1: Lexical, two entries.
	g0 := doc.Groups[0]
	if !strings.HasPrefix(g0.Heading, "Lexical") {
		t.Errorf("group 0 heading: %q, want Lexical*", g0.Heading)
	}
	if len(g0.Entries) != 2 {
		t.Fatalf("group 0 entries: got %d, want 2", len(g0.Entries))
	}
	e0 := g0.Entries[0]
	if e0.Value != "E0001" || e0.Name != "CodeUnterminatedString" {
		t.Errorf("entry 0: %+v", e0)
	}
	if !strings.Contains(e0.Doc.Summary, "closing quote") {
		t.Errorf("summary: %q", e0.Doc.Summary)
	}
	if e0.Doc.Spec != "v0.3 §1.6.1" {
		t.Errorf("spec: %q", e0.Doc.Spec)
	}
	if !strings.Contains(e0.Doc.Example, `let s = "hello`) {
		t.Errorf("example: %q", e0.Doc.Example)
	}
	if !strings.Contains(e0.Doc.Fix, "add the closing quote") {
		t.Errorf("fix: %q", e0.Doc.Fix)
	}

	// Group 2: Declarations, one entry with no doc.
	g1 := doc.Groups[1]
	if !strings.HasPrefix(g1.Heading, "Declarations") {
		t.Errorf("group 1 heading: %q, want Declarations*", g1.Heading)
	}
	if len(g1.Entries) != 1 || g1.Entries[0].Name != "CodeNoDoc" {
		t.Fatalf("group 1 entries: %+v", g1.Entries)
	}
	if g1.Entries[0].Doc.Summary != "" {
		t.Errorf("CodeNoDoc should have empty summary, got %q", g1.Entries[0].Doc.Summary)
	}

	// Group 3: Lint (from the second const block).
	g2 := doc.Groups[2]
	if !strings.HasPrefix(g2.Heading, "Lint") {
		t.Errorf("group 2 heading: %q, want Lint*", g2.Heading)
	}
	if len(g2.Entries) != 1 || g2.Entries[0].Value != "L0001" {
		t.Fatalf("group 2 entries: %+v", g2.Entries)
	}
}

// TestRenderMarkdownStructure confirms the top-level markdown shape for
// a minimal parsedDocs: preamble, one section, one entry with a TODO,
// trailer.
func TestRenderMarkdownStructure(t *testing.T) {
	doc := &parsedDocs{
		Groups: []phaseGroup{
			{
				Heading: "Example (E9999)",
				Entries: []codeEntry{
					{
						Name:  "CodeExample",
						Value: "E9999",
						Doc: parsedDoc{
							Summary: "Short summary.",
							Fix:     "do X.",
						},
					},
				},
			},
		},
	}
	out := renderMarkdown(doc)
	checks := []string{
		"# Osty Diagnostic Reference",
		"## Example (E9999)",
		"### E9999 — `CodeExample`",
		"Short summary.",
		"**Fix**: do X.",
		"## How codes are assigned",
	}
	for _, c := range checks {
		if !strings.Contains(out, c) {
			t.Errorf("output missing %q\n---\n%s", c, out)
		}
	}
}

// TestNormalizeStripBOM confirms -check mode is resilient to BOM /
// CRLF differences.
func TestNormalizeStripBOM(t *testing.T) {
	raw := []byte{0xEF, 0xBB, 0xBF, 'h', 'i', '\r', '\n', 'b', 'y', 'e'}
	got := string(normalize(raw))
	want := "hi\nbye"
	if got != want {
		t.Errorf("normalize: got %q, want %q", got, want)
	}
}
