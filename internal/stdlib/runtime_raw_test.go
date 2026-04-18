package stdlib

import (
	"testing"

	"github.com/osty/osty/internal/resolve"
)

// TestRuntimeRawModuleLoads verifies the nested `modules/runtime/raw.osty`
// stub is embedded and loaded as `std.runtime.raw`. Exercises the
// moduleName extension that converts nested paths to dotted logical
// names.
func TestRuntimeRawModuleLoads(t *testing.T) {
	reg := Load()
	mod := reg.Modules["runtime.raw"]
	if mod == nil {
		t.Fatalf("std.runtime.raw module not registered; Modules=%v",
			moduleKeys(reg))
	}
	if mod.Package == nil {
		t.Fatalf("std.runtime.raw has no resolved Package")
	}
}

// TestRuntimeRawLookupPackage exercises the StdlibProvider-facing
// lookup for nested paths. The resolver consults this when a user
// writes `use std.runtime.raw`.
func TestRuntimeRawLookupPackage(t *testing.T) {
	reg := Load()
	pkg := reg.LookupPackage("std.runtime.raw")
	if pkg == nil {
		t.Fatalf("LookupPackage(\"std.runtime.raw\") returned nil")
	}
}

// TestRuntimeRawIntrinsicsExposed checks every §19.5 intrinsic is
// present as a top-level symbol in the module's package scope.
func TestRuntimeRawIntrinsicsExposed(t *testing.T) {
	reg := Load()
	mod := reg.Modules["runtime.raw"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.runtime.raw not loaded")
	}
	intrinsics := []string{
		"null",
		"fromBits",
		"bits",
		"alloc",
		"free",
		"zero",
		"copy",
		"offset",
		"read",
		"write",
		"cas",
		"sizeOf",
		"alignOf",
	}
	for _, name := range intrinsics {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Errorf("std.runtime.raw missing intrinsic `%s`", name)
			continue
		}
		if sym.Kind != resolve.SymFn {
			t.Errorf("std.runtime.raw.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Errorf("std.runtime.raw.%s is not pub", name)
		}
	}
}

// TestFlatModulesStillWork pins that the moduleName change did not
// break existing flat stubs.
func TestFlatModulesStillWork(t *testing.T) {
	reg := Load()
	// A representative flat module that was previously keyed by
	// basename — must still be reachable.
	for _, name := range []string{"fs", "cmp", "io", "error"} {
		if reg.Modules[name] == nil {
			t.Errorf("flat module `%s` no longer registered after moduleName change", name)
		}
	}
	// LookupPackage for a flat module must also still work.
	if reg.LookupPackage("std.fs") == nil {
		t.Errorf("LookupPackage(\"std.fs\") regressed")
	}
}

func moduleKeys(r *Registry) []string {
	out := make([]string, 0, len(r.Modules))
	for k := range r.Modules {
		out = append(out, k)
	}
	return out
}
