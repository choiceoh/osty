package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestEncodingModuleSurfaceHasBodiedMethods(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["encoding"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.encoding not loaded")
	}

	for _, name := range []string{"base64", "hex", "url"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.encoding missing export %q", name)
		}
		if sym.Kind != resolve.SymLet {
			t.Fatalf("std.encoding.%s kind = %s, want let binding", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.encoding.%s not public", name)
		}
	}

	for _, tc := range []struct {
		typeName string
		methods  []string
	}{
		{"Base64", []string{"encode", "decode"}},
		{"Base64Url", []string{"encode", "decode"}},
		{"Hex", []string{"encode", "decode"}},
		{"UrlEncoding", []string{"encode", "decode"}},
	} {
		for _, method := range tc.methods {
			fn := reg.LookupMethodDecl("encoding", tc.typeName, method)
			if fn == nil {
				t.Fatalf("LookupMethodDecl(encoding, %s, %s) = nil", tc.typeName, method)
			}
			if fn.Body == nil {
				t.Fatalf("encoding.%s.%s body = nil, want pure Osty body", tc.typeName, method)
			}
		}
	}
}

func TestEncodingModuleSourcePinsPureOstyCodecs(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["encoding"]
	if mod == nil {
		t.Fatal("std.encoding module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`fn base64Encode(data: Bytes, alphabet: String, padded: Bool) -> String`,
		`fn base64Decode(text: String, alphabet: String) -> Result<Bytes, Error>`,
		`fn hexEncode(data: Bytes) -> String`,
		`fn hexDecode(text: String) -> Result<Bytes, Error>`,
		`fn urlEncode(text: String) -> String`,
		`fn urlDecode(text: String) -> Result<String, Error>`,
		`bytes.from(out)`,
		`base64UrlAlphabet()`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.encoding source missing %q", want)
		}
	}
	for _, gone := range []string{
		"Body-less signature stubs",
		"remaining blockers",
	} {
		if strings.Contains(src, gone) {
			t.Fatalf("std.encoding source still contains stale blocker text %q", gone)
		}
	}
}
