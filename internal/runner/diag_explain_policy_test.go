package runner

import (
	"reflect"
	"testing"
)

func TestParseExplainDocEmpty(t *testing.T) {
	d := ParseExplainDoc(nil)
	if d.Summary != "" || len(d.Body) != 0 || d.Spec != "" || d.Example != "" || d.Fix != "" {
		t.Errorf("empty input → non-empty doc: %+v", d)
	}
}

func TestParseExplainDocSummaryTrailingPeriod(t *testing.T) {
	if got := ParseExplainDoc([]string{"A short summary"}).Summary; got != "A short summary." {
		t.Errorf("Summary = %q, want %q", got, "A short summary.")
	}
	if got := ParseExplainDoc([]string{"A short summary."}).Summary; got != "A short summary." {
		t.Errorf("Summary preserves period = %q", got)
	}
}

func TestParseExplainDocMultilineSummary(t *testing.T) {
	d := ParseExplainDoc([]string{"First line", "second line", "third"})
	if d.Summary != "First line second line third." {
		t.Errorf("Summary = %q", d.Summary)
	}
	if len(d.Body) != 0 {
		t.Errorf("Body should be empty, got %v", d.Body)
	}
}

func TestParseExplainDocBodyParagraphs(t *testing.T) {
	d := ParseExplainDoc([]string{
		"Summary.", "",
		"First.", "",
		"Second.", "Continued.",
	})
	want := []string{"First.", "Second. Continued."}
	if !reflect.DeepEqual(d.Body, want) {
		t.Errorf("Body = %v, want %v", d.Body, want)
	}
}

func TestParseExplainDocSpec(t *testing.T) {
	d := ParseExplainDoc([]string{"Summary.", "Spec: §1.6.3"})
	if d.Spec != "§1.6.3" {
		t.Errorf("Spec = %q", d.Spec)
	}
}

func TestParseExplainDocFix(t *testing.T) {
	d := ParseExplainDoc([]string{"Summary.", "Fix: close the string with a matching quote"})
	if d.Fix != "close the string with a matching quote" {
		t.Errorf("Fix = %q", d.Fix)
	}
}

func TestParseExplainDocExampleBlock(t *testing.T) {
	d := ParseExplainDoc([]string{
		"Summary.",
		"Example:",
		"    let x = \"unterminated",
		"    let y = 1",
	})
	want := "let x = \"unterminated\nlet y = 1"
	if d.Example != want {
		t.Errorf("Example = %q, want %q", d.Example, want)
	}
}

func TestParseExplainDocExampleStripsLeadingBlanks(t *testing.T) {
	d := ParseExplainDoc([]string{
		"Summary.",
		"Example:",
		"",
		"    line",
		"",
	})
	if d.Example != "line" {
		t.Errorf("Example = %q", d.Example)
	}
}

func TestParseExplainDocExampleNoCommonIndent(t *testing.T) {
	d := ParseExplainDoc([]string{
		"Summary.",
		"Example:",
		"no leading space",
		"    indented",
	})
	if d.Example != "no leading space\n    indented" {
		t.Errorf("Example = %q", d.Example)
	}
}

func TestParseExplainDocAllSections(t *testing.T) {
	d := ParseExplainDoc([]string{
		"Headline.",
		"",
		"Body paragraph.",
		"Spec: §2.2",
		"Fix: do the right thing",
		"Example:",
		"    println(\"ok\")",
	})
	if d.Summary != "Headline." {
		t.Errorf("Summary = %q", d.Summary)
	}
	if !reflect.DeepEqual(d.Body, []string{"Body paragraph."}) {
		t.Errorf("Body = %v", d.Body)
	}
	if d.Spec != "§2.2" {
		t.Errorf("Spec = %q", d.Spec)
	}
	if d.Fix != "do the right thing" {
		t.Errorf("Fix = %q", d.Fix)
	}
	if d.Example != `println("ok")` {
		t.Errorf("Example = %q", d.Example)
	}
}

func TestParseExplainDocBlankSummarySkipped(t *testing.T) {
	d := ParseExplainDoc([]string{"."})
	if d.Summary != "" {
		t.Errorf("Summary should be empty for bare period, got %q", d.Summary)
	}
}
