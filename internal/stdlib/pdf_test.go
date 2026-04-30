package stdlib

import (
	"strings"
	"testing"
)

func TestPdfModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["pdf"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.pdf not loaded")
	}
	for _, name := range []string{
		"mime", "isPdf", "version", "parse", "metadata", "pageCount", "objects",
		"isEncrypted", "isLinearized", "hasXrefStream", "extractText",
		"extractTextWithOptions", "hasText", "kindName",
	} {
		requirePublicFn(t, mod, "pdf", name)
	}
	for _, name := range []string{"Version", "Info", "Document", "ObjectKind", "ObjectHeader", "TextOptions"} {
		requirePublicType(t, mod, "pdf", name)
	}
}

func TestPdfModuleSourcePinsInspectionBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["pdf"]
	if mod == nil {
		t.Fatal("std.pdf module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub fn extractTextWithOptions(data: Bytes, options: TextOptions) -> Result<String, Error>`,
		`fn countPageTypes(text: String) -> Int`,
		`fn objectHeadersFromText(text: String) -> List<ObjectHeader>`,
		`fn readLiteralString(chars: List<Char>, at: Int) -> (String, Int)`,
		`fn readHexString(chars: List<Char>, at: Int) -> (String, Int)`,
		`!strings.contains(dict, "/Filter")`,
		`strings.contains(body, "/Type /XRef")`,
		`c.toInt() == 0x0C`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.pdf source missing %q", want)
		}
	}
}
