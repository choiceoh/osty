//go:build selfhostgen

package main

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/gen"
)

func TestSelfhostGenPreservesListLiteralElementTypes(t *testing.T) {
	dir := t.TempDir()
	target := writeGenTestFile(t, dir, "main.osty", `use std.strings as strings

fn wrap(parts: List<String>) -> String {
    "".join(parts)
}

fn render(name: String) -> String {
    let wrapped = wrap(["<", name, ">"])
    strings.join([wrapped, "!"], "")
}
`)

	entry, err := loadGenPackageEntry(target)
	if err != nil {
		t.Fatalf("loadGenPackageEntry() error = %v", err)
	}
	blocking, deferred := splitGenCheckDiags(entry.chk.Diags)
	if len(blocking) != 0 {
		t.Fatalf("blocking checker diags = %#v", blocking)
	}
	if len(deferred) != 0 {
		t.Logf("deferred checker diags = %#v", deferred)
	}
	file, _, err := parseGenEmitFile(entry.pkg)
	if err != nil {
		t.Fatalf("parseGenEmitFile() error = %v", err)
	}
	src, err := gen.GenerateMapped("main", file, entry.fileResult(), entry.chk, target)
	if err != nil {
		t.Logf("GenerateMapped() warning = %v", err)
	}
	got := string(src)
	if strings.Contains(got, "wrap([]any") {
		t.Fatalf("generated source used []any for a typed user-function list arg:\n%s", got)
	}
	if strings.Contains(got, "_ostyStdStrings_join([]any") || strings.Contains(got, "strings.Join([]any") {
		t.Fatalf("generated source used []any for string joins:\n%s", got)
	}
	if want := `wrap([]string{"<", name, ">"})`; !strings.Contains(got, want) {
		t.Fatalf("generated source missing typed list arg %q:\n%s", want, got)
	}
	if want := `_ostyStdStrings_join([]string{wrapped, "!"}, "")`; !strings.Contains(got, want) {
		t.Fatalf("generated source missing typed strings.Join call %q:\n%s", want, got)
	}
}

func TestSelfhostGenPreservesFFIListLiteralElementTypes(t *testing.T) {
	dir := t.TempDir()
	target := writeGenTestFile(t, dir, "main.osty", `use go "strings" as strings {
    fn Join(parts: List<String>, sep: String) -> String
}

fn render(name: String) -> String {
    strings.Join(["<", name, ">"], "")
}
`)

	entry, err := loadGenPackageEntry(target)
	if err != nil {
		t.Fatalf("loadGenPackageEntry() error = %v", err)
	}
	blocking, _ := splitGenCheckDiags(entry.chk.Diags)
	if len(blocking) != 0 {
		t.Fatalf("blocking checker diags = %#v", blocking)
	}
	file, _, err := parseGenEmitFile(entry.pkg)
	if err != nil {
		t.Fatalf("parseGenEmitFile() error = %v", err)
	}
	src, err := gen.GenerateMapped("main", file, entry.fileResult(), entry.chk, target)
	if err != nil {
		t.Logf("GenerateMapped() warning = %v", err)
	}
	got := string(src)
	if strings.Contains(got, `strings.Join([]any`) {
		t.Fatalf("generated source used []any for an FFI string join:\n%s", got)
	}
	if want := `strings.Join([]string{"<", name, ">"}, "")`; !strings.Contains(got, want) {
		t.Fatalf("generated source missing typed FFI strings.Join call %q:\n%s", want, got)
	}
}

func TestSelfhostGenRewritesFFIBackedListPush(t *testing.T) {
	dir := t.TempDir()
	target := writeGenTestFile(t, dir, "main.osty", `use go "strings" as strings {
    fn Fields(s: String) -> List<String>
}

fn grow() -> List<String> {
    let mut parts = strings.Fields("a b")
    parts.push("c")
    parts
}
`)

	entry, err := loadGenPackageEntry(target)
	if err != nil {
		t.Fatalf("loadGenPackageEntry() error = %v", err)
	}
	blocking, _ := splitGenCheckDiags(entry.chk.Diags)
	if len(blocking) != 0 {
		t.Fatalf("blocking checker diags = %#v", blocking)
	}
	file, _, err := parseGenEmitFile(entry.pkg)
	if err != nil {
		t.Fatalf("parseGenEmitFile() error = %v", err)
	}
	src, err := gen.GenerateMapped("main", file, entry.fileResult(), entry.chk, target)
	if err != nil {
		t.Logf("GenerateMapped() warning = %v", err)
	}
	got := string(src)
	if strings.Contains(got, ".push(") {
		t.Fatalf("generated source left a push call unreduced:\n%s", got)
	}
	if want := `parts = append(parts, "c")`; !strings.Contains(got, want) {
		t.Fatalf("generated source missing append rewrite %q:\n%s", want, got)
	}
}
