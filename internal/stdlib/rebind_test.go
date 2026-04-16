package stdlib

import (
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
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

func TestQualifiedStdlibTypesSharePreludeIdentity(t *testing.T) {
	src := []byte(`use std.collections
use std.option
use std.result
use std.error

pub fn collectionsRoundTrip(xs: List<Int>, m: Map<String, Int>, s: Set<Int>) -> Int {
    let qxs: collections.List<Int> = xs
    let bxs: List<Int> = qxs
    let qm: collections.Map<String, Int> = m
    let bm: Map<String, Int> = qm
    let qs: collections.Set<Int> = s
    let bs: Set<Int> = qs
    bxs.len() + bm.len() + bs.len()
}

pub fn optionRoundTrip(x: Int?) -> Int? {
    let q: option.Option<Int> = x
    let b: Int? = q
    b
}

pub fn resultRoundTrip(r: Result<Int, Error>) -> result.Result<Int, error.Error> {
    let q: result.Result<Int, error.Error> = r
    let b: Result<Int, Error> = q
    b
}

pub fn errorRoundTrip(e: Error) -> error.Error {
    let q: error.Error = e
    let b: Error = q
    b
}
`)
	file, parseDiags := parser.ParseDiagnostics(src)
	for _, d := range parseDiags {
		if d.Severity == diag.Error {
			t.Fatalf("parse: %s", d.Error())
		}
	}
	reg := Load()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	chk := check.File(file, res, check.Opts{
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
	})
	for _, d := range append(res.Diags, chk.Diags...) {
		if d.Severity == diag.Error {
			t.Fatalf("unexpected diagnostic: %s", d.Error())
		}
	}
}

func TestCollectionsPureBodiesTypeCheckAfterRebind(t *testing.T) {
	reg := Load()
	mod := reg.Modules["collections"]
	if mod == nil || mod.Package == nil {
		t.Fatal("std.collections not loaded")
	}
	chk := check.Package(mod.Package, &resolve.PackageResult{PackageScope: mod.Package.PkgScope}, check.Opts{
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
	})
	for _, d := range chk.Diags {
		if d.Severity == diag.Error {
			t.Fatalf("std.collections should type-check: %s", d.Error())
		}
	}
}
