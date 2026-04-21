package genpatch

import (
	"strings"
	"testing"
)

func TestNormalizeGeneratedSourceCommentRewritesIndentedMarkers(t *testing.T) {
	src := strings.Join([]string{
		"// Osty source: /var/folders/test/osty-bootstrap-gen-1/selfhost_merged.osty",
		"func demo() {",
		"\t// Osty: /var/folders/test/osty-bootstrap-gen-1/selfhost_merged.osty:10:2",
		"    // Osty: C:\\Users\\user\\AppData\\Local\\Temp\\osty-bootstrap-gen-2\\selfhost_merged.osty:11:4",
		"}",
	}, "\n")

	got := NormalizeGeneratedSourceComment(src)

	if strings.Contains(got, "/var/folders/") || strings.Contains(got, `C:\Users\user\AppData\Local\Temp`) {
		t.Fatalf("NormalizeGeneratedSourceComment left host paths behind:\n%s", got)
	}
	if !strings.Contains(got, "// Osty source: /tmp/selfhost_merged.osty") {
		t.Fatalf("NormalizeGeneratedSourceComment did not rewrite top-level source comment:\n%s", got)
	}
	if !strings.Contains(got, "\t// Osty: /tmp/selfhost_merged.osty:10:2") {
		t.Fatalf("NormalizeGeneratedSourceComment did not rewrite indented tab marker:\n%s", got)
	}
	if !strings.Contains(got, "    // Osty: /tmp/selfhost_merged.osty:11:4") {
		t.Fatalf("NormalizeGeneratedSourceComment did not rewrite indented space marker:\n%s", got)
	}
}

func TestReplaceGeneratedFunctionHandlesBracesInStringsAndComments(t *testing.T) {
	src := strings.Join([]string{
		"package demo",
		"",
		"func containsInterpolation(text string) bool {",
		`	if text == "{" {`,
		"		return false",
		"	}",
		"	/* comment with } */",
		"	raw := `{`",
		"	_ = raw",
		"	return true",
		"}",
		"",
		"func after() bool {",
		"	return true",
		"}",
	}, "\n")

	replacement := strings.Join([]string{
		"func containsInterpolation(text string) bool {",
		`	return strings.Contains(text, "{")`,
		"}",
	}, "\n")

	got, err := ReplaceGeneratedFunction(src, "containsInterpolation", replacement)
	if err != nil {
		t.Fatalf("ReplaceGeneratedFunction returned error: %v", err)
	}
	if !strings.Contains(got, replacement) {
		t.Fatalf("ReplaceGeneratedFunction did not install replacement:\n%s", got)
	}
	if !strings.Contains(got, "func after() bool {") {
		t.Fatalf("ReplaceGeneratedFunction clobbered following declarations:\n%s", got)
	}
	if strings.Contains(got, `if text == "{" {`) {
		t.Fatalf("ReplaceGeneratedFunction left original function body behind:\n%s", got)
	}
}
