package stdlib

import (
	"strings"
	"testing"
)

func TestDiffModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["diff"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.diff not loaded")
	}

	for _, name := range []string{
		"options", "lines", "diffText", "diffLines", "hunks",
		"stats", "changed", "summary", "addedLines", "deletedLines",
		"applyLines", "applyText", "unified", "unifiedWithContext",
		"unifiedText", "unifiedLines", "formatUnified",
	} {
		requirePublicFn(t, mod, "diff", name)
	}
	for _, name := range []string{"ChangeKind", "Change", "Stats", "Hunk", "UnifiedOptions"} {
		requirePublicType(t, mod, "diff", name)
	}
	for _, method := range []string{"marker", "label", "isChange"} {
		if got := reg.LookupMethodDecl("diff", "ChangeKind", method); got == nil {
			t.Fatalf("LookupMethodDecl(diff, ChangeKind, %s) = nil", method)
		}
	}
	for _, method := range []string{"marker", "isChange", "format"} {
		if got := reg.LookupMethodDecl("diff", "Change", method); got == nil {
			t.Fatalf("LookupMethodDecl(diff, Change, %s) = nil", method)
		}
	}
	for _, method := range []string{"isEmpty", "header", "format"} {
		if got := reg.LookupMethodDecl("diff", "Hunk", method); got == nil {
			t.Fatalf("LookupMethodDecl(diff, Hunk, %s) = nil", method)
		}
	}
	for _, method := range []string{"changed", "total", "summary"} {
		if got := reg.LookupMethodDecl("diff", "Stats", method); got == nil {
			t.Fatalf("LookupMethodDecl(diff, Stats, %s) = nil", method)
		}
	}
	for _, method := range []string{"withPaths", "withContext"} {
		if got := reg.LookupMethodDecl("diff", "UnifiedOptions", method); got == nil {
			t.Fatalf("LookupMethodDecl(diff, UnifiedOptions, %s) = nil", method)
		}
	}
}

func TestDiffModuleSourcePinsBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["diff"]
	if mod == nil {
		t.Fatalf("std.diff not loaded")
	}
	src := string(mod.Source)
	for _, want := range []string{
		"strings.splitLines(text)",
		"fn lcsTable(before: List<String>, after: List<String>) -> List<List<Int>>",
		"table[i][j + 1] > table[i + 1][j]",
		"pub fn hunks(changes: List<Change>, context: Int) -> List<Hunk>",
		"pub fn unifiedText(fromFile: String, toFile: String, before: String, after: String, context: Int) -> String",
		"@@ -{rangeText(self.oldStart, self.oldLen)} +{rangeText(self.newStart, self.newLen)} @@",
		"--- {opts.fromFile}",
		"+++ {opts.toFile}",
		"pub fn applyLines(before: List<String>, changes: List<Change>) -> List<String>",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.diff source missing %q", want)
		}
	}
}
