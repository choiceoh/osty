package docgen

import (
	"strings"
	"testing"
)

// TestParseDocSimple covers the bare "just a summary sentence" form
// that's by far the most common doc comment. No labeled sections,
// so only Summary should be populated.
func TestParseDocSimple(t *testing.T) {
	info := parseDocComment("Add two integers.")
	if info.Summary != "Add two integers." {
		t.Errorf("Summary: got %q want %q", info.Summary, "Add two integers.")
	}
	if len(info.Body) != 0 || len(info.Params) != 0 || info.Returns != "" {
		t.Errorf("expected only Summary populated, got %+v", info)
	}
}

// TestParseDocSummaryAndBody verifies that the first paragraph becomes
// Summary and subsequent blank-separated paragraphs become Body —
// preserving the convention every well-documented language follows.
func TestParseDocSummaryAndBody(t *testing.T) {
	info := parseDocComment(strings.Join([]string{
		"Add two integers.",
		"",
		"Wraps on overflow.",
		"",
		"Cheap — no allocations.",
	}, "\n"))
	if info.Summary != "Add two integers." {
		t.Errorf("Summary wrong: %q", info.Summary)
	}
	if len(info.Body) != 2 {
		t.Fatalf("Body len = %d, want 2; got %+v", len(info.Body), info.Body)
	}
	if info.Body[0] != "Wraps on overflow." {
		t.Errorf("Body[0] = %q", info.Body[0])
	}
}

// TestParseDocSections exercises every labeled section the parser
// recognises: Params, Returns, Example, See.
func TestParseDocSections(t *testing.T) {
	doc := strings.Join([]string{
		"Add two integers.",
		"",
		"Params:",
		"  x: the first addend.",
		"  y: the second addend.",
		"Returns: the sum.",
		"Example:",
		"    let z = add(1, 2)",
		"    assert(z == 3)",
		"See: subtract, multiply",
	}, "\n")
	info := parseDocComment(doc)

	if info.Summary != "Add two integers." {
		t.Errorf("Summary: %q", info.Summary)
	}
	if len(info.Params) != 2 {
		t.Fatalf("Params len = %d, want 2", len(info.Params))
	}
	if info.Params[0].Name != "x" || info.Params[0].Desc != "the first addend." {
		t.Errorf("Params[0] = %+v", info.Params[0])
	}
	if info.Returns != "the sum." {
		t.Errorf("Returns: %q", info.Returns)
	}
	if len(info.Examples) != 1 {
		t.Fatalf("Examples len = %d, want 1", len(info.Examples))
	}
	if !strings.Contains(info.Examples[0], "let z = add(1, 2)") {
		t.Errorf("Example missing content: %q", info.Examples[0])
	}
	// Shared indent should be stripped.
	if strings.HasPrefix(info.Examples[0], " ") {
		t.Errorf("Example not dedented: %q", info.Examples[0])
	}
	if len(info.See) != 2 {
		t.Fatalf("See len = %d, want 2", len(info.See))
	}
	if info.See[0] != "subtract" || info.See[1] != "multiply" {
		t.Errorf("See: %+v", info.See)
	}
}

// TestParseDocInlineReturns confirms the `Return:` singular alias and
// the inline form `Returns: foo` (no continuation lines) both work.
// Users are inconsistent with the label spelling; we accept both.
func TestParseDocInlineReturns(t *testing.T) {
	info := parseDocComment("Summary.\nReturn: the value.")
	if info.Returns != "the value." {
		t.Errorf("Returns: %q", info.Returns)
	}
}

// TestParseDocParamContinuation verifies that a multi-line param
// description is joined into one Desc. This matters because authors
// often wrap long descriptions to stay within 80 columns.
func TestParseDocParamContinuation(t *testing.T) {
	doc := strings.Join([]string{
		"Summary.",
		"Params:",
		"  buffer: a pre-allocated slice",
		"    that receives the read bytes.",
	}, "\n")
	info := parseDocComment(doc)
	if len(info.Params) != 1 {
		t.Fatalf("Params len = %d, want 1", len(info.Params))
	}
	if !strings.Contains(info.Params[0].Desc, "a pre-allocated slice") ||
		!strings.Contains(info.Params[0].Desc, "receives the read bytes") {
		t.Errorf("continuation not joined: %q", info.Params[0].Desc)
	}
}

// TestParseDocMultipleExamples handles the case where a decl has
// more than one Example: block. Each should be preserved as its own
// entry so the renderer can emit one code block per example.
func TestParseDocMultipleExamples(t *testing.T) {
	doc := strings.Join([]string{
		"Summary.",
		"Example:",
		"    add(1, 2)",
		"Example:",
		"    add(-1, 1)",
	}, "\n")
	info := parseDocComment(doc)
	if len(info.Examples) != 2 {
		t.Fatalf("Examples len = %d, want 2", len(info.Examples))
	}
	if info.Examples[0] != "add(1, 2)" || info.Examples[1] != "add(-1, 1)" {
		t.Errorf("Examples: %+v", info.Examples)
	}
}

// TestParseDocNonSectionColon guards against over-eager section
// detection: lines like "Note: the behaviour is ..." inside prose
// should stay prose and not open a (non-existent) `Note:` section.
func TestParseDocNonSectionColon(t *testing.T) {
	info := parseDocComment("Summary.\n\nNote: the behaviour is undefined for NaN.")
	if info.Summary != "Summary." {
		t.Errorf("Summary: %q", info.Summary)
	}
	if len(info.Body) != 1 || !strings.HasPrefix(info.Body[0], "Note:") {
		t.Errorf("expected prose body with Note:, got %+v", info.Body)
	}
}
