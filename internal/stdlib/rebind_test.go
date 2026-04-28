package stdlib

import (
	"strings"
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
		if mod.Package.PkgScope == nil {
			t.Fatalf("std.%s loaded without package scope; registry diagnostics:\n%s", tc.module, stdlibRebindDiagSummary(reg))
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
		if mod == nil || mod.Package == nil {
			t.Fatalf("std.%s not loaded", tc.module)
		}
		if mod.Package.PkgScope == nil {
			t.Fatalf("std.%s loaded without package scope; registry diagnostics:\n%s", tc.module, stdlibRebindDiagSummary(reg))
		}
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

func stdlibRebindDiagSummary(reg *Registry) string {
	if reg == nil || len(reg.Diags) == 0 {
		return "<none>"
	}
	var b strings.Builder
	for _, d := range reg.Diags {
		if d == nil {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		if d.File != "" {
			b.WriteString(d.File)
			b.WriteString(": ")
		}
		b.WriteString(d.Message)
	}
	if b.Len() == 0 {
		return "<none>"
	}
	return b.String()
}
