package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestKeychainAndSecretsModuleSurfaces(t *testing.T) {
	reg := LoadCached()
	for _, tt := range []struct {
		module string
		fns    []string
		types  []string
	}{
		{
			module: "keychain",
			fns: []string{
				"backend", "isAvailable", "get", "set", "delete",
				"entry", "getEntry", "setEntry", "deleteEntry",
				"getApiKey", "setApiKey", "deleteApiKey",
			},
			types: []string{"Entry"},
		},
		{
			module: "secrets",
			fns: []string{
				"backend", "isAvailable", "get", "set", "delete",
				"getApiKey", "setApiKey", "deleteApiKey",
			},
		},
	} {
		mod := reg.Modules[tt.module]
		if mod == nil || mod.Package == nil {
			t.Fatalf("std.%s not loaded", tt.module)
		}
		for _, name := range tt.fns {
			sym := mod.Package.PkgScope.LookupLocal(name)
			if sym == nil {
				t.Fatalf("std.%s missing export %q", tt.module, name)
			}
			if sym.Kind != resolve.SymFn {
				t.Fatalf("std.%s.%s kind = %s, want fn", tt.module, name, sym.Kind)
			}
			if !sym.Pub {
				t.Fatalf("std.%s.%s not public", tt.module, name)
			}
		}
		for _, name := range tt.types {
			sym := mod.Package.PkgScope.LookupLocal(name)
			if sym == nil {
				t.Fatalf("std.%s missing type %q", tt.module, name)
			}
			if !sym.Pub {
				t.Fatalf("std.%s.%s not public", tt.module, name)
			}
		}
	}
}

func TestKeychainModuleSourcePinsRuntimeBackedApiKeySurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["keychain"]
	if mod == nil {
		t.Fatal("std.keychain module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub fn backend() -> String`,
		`pub fn isAvailable() -> Bool`,
		`pub fn get(service: String, account: String) -> Result<String, Error>`,
		`pub fn set(service: String, account: String, secret: String) -> Result<(), Error>`,
		`pub fn delete(service: String, account: String) -> Result<(), Error>`,
		`pub let defaultApiKeyService: String = "osty.api"`,
		`pub fn setApiKey(provider: String, secret: String) -> Result<(), Error>`,
		`strings.trimSpace(secret)`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.keychain source missing %q", want)
		}
	}
}
