package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// TestInjectedMapTypeMonomorphizes drives the Option B Phase 1
// pipeline end-to-end at the IR layer:
//
//  1. User code references `Map<String, Int>` in a function signature
//     and calls `m.containsKey(k)` inside the body.
//  2. injectReachableStdlibTypes appends Map's generic StructDecl to
//     the module.
//  3. ir.Monomorphize specializes Map<K, V> → Map$String$Int with its
//     methods (containsKey, getOr, update, …) pre-substituted.
//
// The assertion: the output IR module contains a specialized struct
// for Map<String, Int>, and its Methods list includes the bodied
// helpers with K / V replaced. If this test fails, the
// monomorphization foundation is broken and no later Phase can
// rescue it.
func TestInjectedMapTypeMonomorphizes(t *testing.T) {
	// Exercise each canonical helper at least once. mapValues has a
	// method-local generic (R) so it only specializes when actually
	// called with a concrete R — this block pins K=String / V=Int /
	// R=String for that specialization.
	src := `fn stringify(n: Int) -> String { "{n}" }
fn bump(n: Int?) -> Int { (n ?? 0) + 1 }
fn hit(k: String, v: Int) -> Bool { v > 0 }
fn add(a: Int, b: Int) -> Int { a + b }

fn touch(m: Map<String, Int>, o: Map<String, Int>, k: String) -> Map<String, Int> {
    let _c = m.containsKey(k)
    let _g = m.getOr(k, 0)
    let mut mm: Map<String, Int> = m
    mm.update(k, bump)
    mm.retainIf(hit)
    let _labels: Map<String, String> = m.mapValues(stringify)
    m.mergeWith(o, add)
}

fn main() {}
`
	mod := lowerUserProgramForTest(t, src)
	reg := stdlib.LoadCached()

	injected, _ := injectReachableStdlibTypes(mod, reg)
	if len(injected) == 0 {
		t.Fatalf("injectReachableStdlibTypes returned no decls for code that uses Map<String, Int>")
	}
	// Confirm `Map` is among the injected generic decls.
	foundMap := false
	for _, d := range injected {
		if sd, ok := d.(*ir.StructDecl); ok && sd.Name == "Map" {
			foundMap = true
			if len(sd.Generics) != 2 {
				t.Fatalf("injected Map has %d generic params, want 2", len(sd.Generics))
			}
			// Every canonical helper listed in
			// internal/llvmgen/stdlib_method_lookup.go should appear
			// on the injected generic template.
			for _, want := range []string{"containsKey", "getOr", "update", "retainIf", "mergeWith", "mapValues"} {
				hit := false
				for _, m := range sd.Methods {
					if m != nil && m.Name == want {
						hit = true
						break
					}
				}
				if !hit {
					t.Errorf("injected Map template missing method %q", want)
				}
			}
		}
	}
	if !foundMap {
		t.Fatalf("injected decls don't include Map StructDecl:\n%v", injected)
	}
	mod.Decls = append(mod.Decls, injected...)

	monoMod, monoErrs := ir.Monomorphize(mod)
	if monoMod == nil {
		t.Fatalf("ir.Monomorphize returned nil module; errs: %v", monoErrs)
	}

	// After monomorphization the output must contain a specialized
	// (non-generic) StructDecl whose mangled name mentions `Map` and
	// whose Methods list includes all canonical helpers with their
	// K/V already substituted. The exact mangling scheme is
	// ir.MonomorphMangleStruct's responsibility — we check for Map's
	// substring so the test stays robust to mangler changes.
	var specialized *ir.StructDecl
	for _, d := range monoMod.Decls {
		sd, ok := d.(*ir.StructDecl)
		if !ok {
			continue
		}
		if len(sd.Generics) != 0 {
			// Generic template (if preserved) — skip.
			continue
		}
		if strings.Contains(sd.Name, "Map") && sd.Name != "Map" {
			specialized = sd
			break
		}
	}
	if specialized == nil {
		for i, d := range monoMod.Decls {
			switch x := d.(type) {
			case *ir.StructDecl:
				t.Logf("decl[%d] = StructDecl %q generics=%d methods=%d", i, x.Name, len(x.Generics), len(x.Methods))
			case *ir.EnumDecl:
				t.Logf("decl[%d] = EnumDecl %q generics=%d", i, x.Name, len(x.Generics))
			case *ir.FnDecl:
				t.Logf("decl[%d] = FnDecl %q generics=%d", i, x.Name, len(x.Generics))
			}
		}
		t.Fatalf("monomorph output has no Map specialization")
	}
	t.Logf("specialized Map type: %s with %d methods", specialized.Name, len(specialized.Methods))
	// Helpers without method-local generics: struct-level K/V are
	// enough to specialize, so they must appear on the concrete
	// Map$String$Int decl after monomorph.
	for _, want := range []string{"containsKey", "getOr", "update", "retainIf", "mergeWith"} {
		hit := false
		for _, m := range specialized.Methods {
			if m != nil && m.Name == want {
				hit = true
				break
			}
		}
		if !hit {
			t.Errorf("specialized Map is missing method %q — monomorph didn't carry over all canonical helpers", want)
		}
	}
	// mapValues<R> carries a method-local generic (R). It specializes
	// only when the checker recorded TypeArgs on the MethodCall, which
	// today happens for explicit turbofish (`mapValues::<String>(...)`)
	// but not for argument-driven inference. The enclosing monomorph
	// pass treats the generic template as a placeholder and drops it
	// when no concrete R emerges. This is tracked as a Phase 1b
	// follow-up (checker must persist inferred TypeArgs on MethodCall).
	hasMapValues := false
	for _, m := range specialized.Methods {
		if m != nil && strings.HasPrefix(m.Name, "mapValues") {
			hasMapValues = true
			break
		}
	}
	if hasMapValues {
		t.Logf("bonus: mapValues variant appeared on specialization — checker plumbed TypeArgs correctly")
	}
}

// TestInjectReachableStdlibTypesSkipsUnusedTypes verifies that user
// code referencing only Map does NOT pull in List / Set / Result.
// The injector must be pay-as-you-go so compilation cost stays
// proportional to what the user actually uses.
func TestInjectReachableStdlibTypesSkipsUnusedTypes(t *testing.T) {
	src := `fn touch(m: Map<String, Int>) -> Int {
    m.len()
}

fn main() {}
`
	mod := lowerUserProgramForTest(t, src)
	reg := stdlib.LoadCached()
	injected, _ := injectReachableStdlibTypes(mod, reg)

	names := map[string]bool{}
	for _, d := range injected {
		switch x := d.(type) {
		case *ir.StructDecl:
			names[x.Name] = true
		case *ir.EnumDecl:
			names[x.Name] = true
		}
	}
	if !names["Map"] {
		t.Errorf("expected Map to be injected")
	}
	for _, unused := range []string{"List", "Set", "Result"} {
		if names[unused] {
			t.Errorf("unused stdlib type %q was injected — injector is not pay-as-you-go", unused)
		}
	}
}

// TestInjectReachableStdlibTypesOptionFromOptionalSyntax verifies that
// user code using `T?` (not `Option<T>` explicitly) still pulls in the
// Option enum so `??` / `.isSome()` dispatch has its template to
// specialize. The surface `T?` lowers to ir.OptionalType at first,
// and the injector must see through that wrapper.
func TestInjectReachableStdlibTypesOptionFromOptionalSyntax(t *testing.T) {
	src := `fn fallback(x: Int?) -> Int {
    x ?? 0
}

fn main() {}
`
	mod := lowerUserProgramForTest(t, src)
	reg := stdlib.LoadCached()
	injected, _ := injectReachableStdlibTypes(mod, reg)
	foundOption := false
	for _, d := range injected {
		if ed, ok := d.(*ir.EnumDecl); ok && ed.Name == "Option" {
			foundOption = true
			break
		}
	}
	if !foundOption {
		t.Fatalf("Option enum not injected for `Int?` parameter — surface `T?` sugar must trigger injection")
	}
}

// lowerUserProgramForTest runs the parse / resolve / check / ir.Lower
// stack on user source and returns the resulting IR module. Helper for
// the injection tests so each one focuses on the injection semantics
// rather than boilerplate.
func lowerUserProgramForTest(t *testing.T, src string) *ir.Module {
	t.Helper()
	file, parseDiags := parser.ParseCanonical([]byte(src))
	for _, d := range parseDiags {
		if d.Severity == 0 { // Error = 0
			t.Fatalf("parse error: %v", d)
		}
	}
	reg := stdlib.LoadCached()
	res := resolve.ResolveFileDefault(file, reg)
	chk := check.SelfhostFile(file, res, check.Opts{
		Source: []byte(src),
		Stdlib: reg,
	})
	mod, lowerErrs := ir.Lower("main", file, res, chk)
	for _, e := range lowerErrs {
		t.Logf("ir.Lower issue: %v", e)
	}
	if mod == nil {
		t.Fatalf("ir.Lower returned nil")
	}
	return mod
}
