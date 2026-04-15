package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFirstMappedGoLocationUsesNearestOstyMarker(t *testing.T) {
	dir := t.TempDir()
	goPath := filepath.Join(dir, "main.go")
	srcPath := filepath.Join(dir, "main.osty")
	body := strings.Join([]string{
		"package main",
		"",
		"// Osty: " + srcPath + ":3:1",
		"func main() {",
		"\tprintln(\"before\")",
		"// Osty: " + srcPath + ":7:5",
		"\tpanic(\"boom\")",
		"}",
		"",
	}, "\n")
	if err := os.WriteFile(goPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	stderr := "panic: boom\n\ngoroutine 1 [running]:\nmain.main()\n\t./main.go:7 +0x25\n"
	mapped, ok := firstMappedGoLocation(stderr, []string{goPath}, dir)
	if !ok {
		t.Fatalf("expected mapping for stderr:\n%s", stderr)
	}
	if mapped.Osty.Path != srcPath || mapped.Osty.Line != 7 || mapped.Osty.Column != 5 {
		t.Fatalf("mapped to %+v, want %s:7:5", mapped.Osty, srcPath)
	}
	if mapped.MarkerGoLine != 6 || mapped.GoLine != 7 {
		t.Fatalf("go mapping = marker %d line %d, want marker 6 line 7", mapped.MarkerGoLine, mapped.GoLine)
	}
}

func TestClassifyGoFailurePackageImport(t *testing.T) {
	category, explanation := classifyGoFailure(`main.go:5:2: no required module provides package example.com/missing`)
	if category != "package/import" {
		t.Fatalf("category = %q, want package/import", category)
	}
	if !strings.Contains(explanation, "import") {
		t.Fatalf("explanation should mention import, got %q", explanation)
	}
}

func TestParseOstyMarkerKeepsColonInPath(t *testing.T) {
	loc, ok := parseOstyMarker(`// Osty: C:\tmp\main.osty:12:3`)
	if !ok {
		t.Fatal("expected marker to parse")
	}
	if loc.Path != `C:\tmp\main.osty` || loc.Line != 12 || loc.Column != 3 {
		t.Fatalf("loc = %+v", loc)
	}
}
