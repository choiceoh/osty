package stdlib

import (
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestPreludeBuiltinRebindings(t *testing.T) {
	reg := Load()
	for _, tc := range []struct {
		module string
		names  []string
	}{
		{"collections", []string{"List", "Map", "Set"}},
		{"error", []string{"Error"}},
		{"option", []string{"Option"}},
		{"result", []string{"Result"}},
	} {
		mod := reg.Modules[tc.module]
		if mod == nil || mod.Package == nil {
			t.Fatalf("std.%s not loaded", tc.module)
		}
		for _, name := range tc.names {
			sym := mod.Package.PkgScope.LookupLocal(name)
			if sym == nil {
				t.Fatalf("std.%s missing export %s", tc.module, name)
			}
			if sym.Kind != resolve.SymBuiltin {
				t.Fatalf("std.%s.%s kind = %s, want builtin", tc.module, name, sym.Kind)
			}
		}
	}

	for _, tc := range []struct {
		module string
		names  []string
	}{
		{"option", []string{"Some", "None"}},
		{"result", []string{"Ok", "Err"}},
	} {
		mod := reg.Modules[tc.module]
		for _, name := range tc.names {
			sym := mod.Package.PkgScope.LookupLocal(name)
			if sym == nil {
				t.Fatalf("std.%s missing export %s", tc.module, name)
			}
			if sym.Kind != resolve.SymVariant {
				t.Fatalf("std.%s.%s kind = %s, want variant", tc.module, name, sym.Kind)
			}
		}
	}
}
