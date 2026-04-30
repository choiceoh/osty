package stdlib

import (
	"strings"
	"testing"
)

func TestXlsxModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["xlsx"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.xlsx not loaded")
	}

	for _, name := range []string{
		"cell", "text", "number", "int", "bool", "blank", "sheet", "workbook",
		"fromRows", "fromTable", "toTable", "rows", "sheetNames", "encodeRows",
		"encodeTable", "encode", "decode", "decodeRows", "isWorkbook", "kindName",
	} {
		requirePublicFn(t, mod, "xlsx", name)
	}
	for _, name := range []string{"CellKind", "Cell", "Sheet", "Workbook"} {
		requirePublicType(t, mod, "xlsx", name)
	}
	for _, method := range []string{"rows", "toTable"} {
		if got := reg.LookupMethodDecl("xlsx", "Sheet", method); got == nil {
			t.Fatalf("LookupMethodDecl(xlsx, Sheet, %s) = nil", method)
		}
	}
	for _, method := range []string{"sheetNames", "firstSheet"} {
		if got := reg.LookupMethodDecl("xlsx", "Workbook", method); got == nil {
			t.Fatalf("LookupMethodDecl(xlsx, Workbook, %s) = nil", method)
		}
	}
}

func TestXlsxModuleSourcePinsPackageBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["xlsx"]
	if mod == nil {
		t.Fatal("std.xlsx module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`use std.zip`,
		`escapeXml`,
		`unescapeXml`,
		`encodeParts(parts)`,
		`appendEndOfCentralDirectory`,
		`inlineStr`,
		`parseWorkbookRels(relsText)?`,
		`parseSharedStrings(text)`,
		`table.fromRows(columns, body)`,
		`return Err(error.new("xlsx: workbook needs at least one sheet"))`,
		`return Err(error.new("xlsx: sheet name is longer than 31 characters"))`,
		`std.zip can expose their entries`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.xlsx source missing %q", want)
		}
	}
}
