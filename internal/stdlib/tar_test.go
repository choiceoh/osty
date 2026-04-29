package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestTarModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["tar"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.tar not loaded")
	}
	for _, name := range []string{"file", "directory", "withMode", "withMtime", "encode", "decode", "list", "extract", "contains", "isArchive"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.tar missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.tar.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.tar.%s not public", name)
		}
	}
	for _, name := range []string{"EntryType", "Entry"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.tar missing type %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.tar.%s not public", name)
		}
	}
}

func TestTarModuleSourcePinsUstarBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["tar"]
	if mod == nil {
		t.Fatal("std.tar module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub fn encode(entries: List<Entry>) -> Result<Bytes, Error>`,
		`pub fn decode(archive: Bytes) -> Result<List<Entry>, Error>`,
		`writeStringField(header, 257, 6, "ustar")`,
		`writeOctalField(header, 148, 8, checksum(header))`,
		`fn checksumBlock(data: Bytes, offset: Int) -> Int`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.tar source missing %q", want)
		}
	}
}
