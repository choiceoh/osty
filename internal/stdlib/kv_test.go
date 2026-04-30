package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestKvModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["kv"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.kv not loaded")
	}
	for _, name := range []string{
		"open", "ensure",
		"entry", "entryAt", "tombstone", "tombstoneAt",
		"text", "integer", "float", "boolean", "nullValue",
		"encodeEntry", "parseEntry", "parseLog", "replay", "inMemory",
		"load", "snapshot", "keys", "get", "getString", "getInt", "getFloat", "getBool", "getJson", "contains",
		"put", "putAt", "putString", "putInt", "putFloat", "putBool",
		"putJson", "remove", "removeAt", "deleteKey", "clear", "compact", "encodeSnapshot", "appendEntry",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.kv missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.kv.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.kv.%s not public", name)
		}
	}
	for _, name := range []string{"Store", "Entry", "Snapshot"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.kv missing type %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.kv.%s not public", name)
		}
	}
}

func TestKvModuleSourcePinsJsonlFileBackedBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["kv"]
	if mod == nil {
		t.Fatal("std.kv module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`use std.fs`,
		`use std.json`,
		`pub fn putAt(store: Store, key: String, value: String, updatedAtMs: Int) -> Result<(), Error>`,
		`pub fn compact(store: Store) -> Result<Int, Error>`,
		`pub fn getString(store: Store, key: String) -> Result<String?, Error>`,
		`pub fn getJson(store: Store, key: String) -> Result<json.Json?, Error>`,
		`fn decodeJsonString(chars: List<Char>, start: Int) -> Result<String, Error>`,
		`values.remove(item.key)`,
		`fs.writeString(store.path, next)?`,
		`fn parentDir(path: String) -> String`,
		`append-log format`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.kv source missing %q", want)
		}
	}
}
