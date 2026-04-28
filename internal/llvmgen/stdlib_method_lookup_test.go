package llvmgen

import (
	"testing"

	"github.com/osty/osty/internal/stdlib"
)

// TestLookupMapCanonicalHelpersPresent confirms every canonical §B.9.1.64
// Map helper has a bodied AST FnDecl reachable through the registry.
// This is load-bearing for future monomorphization: if a stdlib
// refactor accidentally removes one of these bodies (e.g. moves it to
// a declaration-only intrinsic), the monomorph pass would silently
// skip it and the hand-emit path would have to cover the gap
// indefinitely.
func TestLookupMapCanonicalHelpersPresent(t *testing.T) {
	reg := stdlib.LoadCached()
	for _, name := range MapCanonicalHelperNames {
		fn := LookupStructMethod(reg, "Map", name)
		if fn == nil {
			t.Errorf("Map.%s has no bodied AST in stdlib — canonical helper missing or declaration-only", name)
			continue
		}
		if fn.Body == nil {
			t.Errorf("Map.%s AST found but body is nil — lookup mis-filtered", name)
		}
	}
}

// TestLookupStructMethodWithGenericsCapturesStructParams verifies the
// enclosing struct's generic list travels with the method body.
// Future monomorphization needs both: the method AST to clone, and
// the generic parameters to substitute (K, V → concrete types).
func TestLookupStructMethodWithGenericsCapturesStructParams(t *testing.T) {
	reg := stdlib.LoadCached()
	got := LookupStructMethodWithGenerics(reg, "Map", "getOr")
	if got == nil {
		t.Fatalf("Map.getOr not found")
	}
	if got.StructName != "Map" {
		t.Fatalf("StructName = %q, want %q", got.StructName, "Map")
	}
	if len(got.StructGenerics) != 2 {
		t.Fatalf("Map should have 2 generic params (K, V); got %d", len(got.StructGenerics))
	}
	if got.StructGenerics[0].Name != "K" || got.StructGenerics[1].Name != "V" {
		t.Fatalf("Map generics should be [K, V]; got [%s, %s]",
			got.StructGenerics[0].Name, got.StructGenerics[1].Name)
	}
	// Method-local generics (e.g. mapValues<R>) should remain on the method.
	// containsKey has no method-local generics — cross-check that the
	// enclosing-struct lookup doesn't conflate them.
	if len(got.Method.Generics) != 0 {
		t.Fatalf("Map.getOr has no method-local generics, got %d", len(got.Method.Generics))
	}
}

// TestLookupListCanonicalHelpersPresent mirrors the Map check for the
// List<T> helper set. `groupBy` lives on List (returns Map<K, List<T>>)
// — this pins it as reachable so the future monomorph dispatcher can
// find the body when the first `List.groupBy<K>` callsite materialises.
func TestLookupListCanonicalHelpersPresent(t *testing.T) {
	reg := stdlib.LoadCached()
	for _, name := range ListCanonicalHelperNames {
		fn := LookupStructMethod(reg, "List", name)
		if fn == nil {
			t.Errorf("List.%s has no bodied AST in stdlib", name)
		}
	}
}

// TestLookupEnumMethodReachesOptionIsSome anchors the body-dependency
// chain `Map.containsKey` → `self.get(key).isSome()` →
// `Option.isSome { match self { Some(_) -> true, None -> false } }`.
// Any monomorphization of containsKey has to walk into Option.isSome
// — this test asserts the walk's starting point is reachable.
func TestLookupEnumMethodReachesOptionIsSome(t *testing.T) {
	reg := stdlib.LoadCached()
	fn := LookupEnumMethod(reg, "Option", "isSome")
	if fn == nil {
		t.Fatalf("Option.isSome not reachable via enum-method lookup")
	}
	if fn.Body == nil {
		t.Fatalf("Option.isSome found but body is nil")
	}
}

func TestLookupEnumMethodReachesExpandedOptionAndResultBodies(t *testing.T) {
	reg := stdlib.LoadCached()
	cases := []struct {
		enumName   string
		methodName string
	}{
		{"Option", "zipWith"},
		{"Option", "reduce"},
		{"Result", "zip"},
		{"Result", "zipWith"},
		{"Result", "toList"},
	}
	for _, tc := range cases {
		fn := LookupEnumMethod(reg, tc.enumName, tc.methodName)
		if fn == nil {
			t.Fatalf("%s.%s not reachable via enum-method lookup", tc.enumName, tc.methodName)
		}
		if fn.Body == nil {
			t.Fatalf("%s.%s found but body is nil", tc.enumName, tc.methodName)
		}
	}
}
