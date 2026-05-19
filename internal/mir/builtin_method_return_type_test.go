package mir

import (
	"testing"

	"github.com/osty/osty/internal/ir"
)

// TestBuiltinMethodReturnTypeOptionMaybeMethodCoverage exercises the
// expanded Option/Maybe coverage in `builtinMethodReturnType` so a
// poisoned MIR temp (e.g., the dest of a checker-skipped Option method
// call inside a string interpolation) can still recover its return type
// from the receiver shape. The same set is mirrored in
// `recoveredMethodReturnType` for the structural `*ir.OptionalType`
// receiver form.
//
// `getOr` is NOT in this table — it is a Map method
// (`internal/stdlib/modules/map.osty`), not an Option method. Recovering
// it on Option would mask an invalid call instead of letting the
// upstream checker surface E0703.
func TestBuiltinMethodReturnTypeOptionMaybeMethodCoverage(t *testing.T) {
	option := &ir.NamedType{Name: "Option", Args: []ir.Type{ir.TInt}, Builtin: true}
	cases := []struct {
		method string
		want   ir.Type
	}{
		{"isSome", ir.TBool},
		{"isNone", ir.TBool},
		{"isSomeAnd", ir.TBool},
		{"isNoneOr", ir.TBool},
		{"contains", ir.TBool},
		{"count", ir.TInt},
		{"unwrap", ir.TInt},
		{"unwrapOr", ir.TInt},
		{"unwrapOrElse", ir.TInt},
		{"expect", ir.TInt},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			got := builtinMethodReturnType(option, tc.method)
			if got == nil {
				t.Fatalf("builtinMethodReturnType(Option<Int>, %q) = nil, want %v", tc.method, tc.want)
			}
			if got.String() != tc.want.String() {
				t.Fatalf("builtinMethodReturnType(Option<Int>, %q) = %v, want %v", tc.method, got, tc.want)
			}
		})
	}
	// take / replace / or / orElse / filter / inspect all keep the
	// same Option<T> shape — verify the Inner type is preserved
	// verbatim.
	for _, m := range []string{"take", "replace", "or", "orElse", "filter", "inspect"} {
		t.Run(m, func(t *testing.T) {
			got := builtinMethodReturnType(option, m)
			if got == nil {
				t.Fatalf("builtinMethodReturnType(Option<Int>, %q) = nil, want Option<Int>", m)
			}
			nt, ok := got.(*ir.NamedType)
			if !ok || nt.Name != "Option" || len(nt.Args) != 1 || nt.Args[0] != ir.TInt {
				t.Fatalf("builtinMethodReturnType(Option<Int>, %q) = %v, want Option<Int>", m, got)
			}
		})
	}
	// toString → String, toList → List<T>, okOr → Result<T, Error>.
	t.Run("toString", func(t *testing.T) {
		got := builtinMethodReturnType(option, "toString")
		if got != ir.TString {
			t.Fatalf("builtinMethodReturnType(Option<Int>, \"toString\") = %v, want String", got)
		}
	})
	t.Run("toList", func(t *testing.T) {
		got := builtinMethodReturnType(option, "toList")
		nt, ok := got.(*ir.NamedType)
		if !ok || nt.Name != "List" || len(nt.Args) != 1 || nt.Args[0] != ir.TInt {
			t.Fatalf("builtinMethodReturnType(Option<Int>, \"toList\") = %v, want List<Int>", got)
		}
	})
	t.Run("okOr", func(t *testing.T) {
		got := builtinMethodReturnType(option, "okOr")
		nt, ok := got.(*ir.NamedType)
		if !ok || nt.Name != "Result" || len(nt.Args) != 2 || nt.Args[0] != ir.TInt {
			t.Fatalf("builtinMethodReturnType(Option<Int>, \"okOr\") = %v, want Result<Int, Error>", got)
		}
		errArg, ok := nt.Args[1].(*ir.NamedType)
		if !ok || errArg.Name != "Error" {
			t.Fatalf("builtinMethodReturnType(Option<Int>, \"okOr\").Args[1] = %v, want Error", nt.Args[1])
		}
	})
	// `okOrElse<E>` is generic in the error arm — recovery deliberately
	// stays nil because the type depends on the closure's inferred
	// return.
	t.Run("okOrElse-uncovered", func(t *testing.T) {
		if got := builtinMethodReturnType(option, "okOrElse"); got != nil {
			t.Fatalf("builtinMethodReturnType(Option<Int>, \"okOrElse\") = %v, want nil (generic in E)", got)
		}
	})
}

// TestBuiltinMethodReturnTypeResultMethodCoverage exercises the
// expanded Result<T, E> coverage. `unwrap*`/`expect*`/`isOk`/`isErr`
// were already covered; the new entries are `ok`/`err` (which return
// Option<T> / Option<E>), `expect`/`expectErr` (T / E), and the
// predicate / counter family (`contains`, `containsErr`, `isOkAnd`,
// `isErrAnd`, `count`) registered by
// `internal/stdlib/modules/result.osty`.
func TestBuiltinMethodReturnTypeResultMethodCoverage(t *testing.T) {
	intStr := &ir.NamedType{
		Name:    "Result",
		Args:    []ir.Type{ir.TInt, ir.TString},
		Builtin: true,
	}
	cases := []struct {
		method string
		want   ir.Type
	}{
		{"unwrap", ir.TInt},
		{"unwrapOr", ir.TInt},
		{"unwrapOrElse", ir.TInt},
		{"expect", ir.TInt},
		{"unwrapErr", ir.TString},
		{"expectErr", ir.TString},
		{"isOk", ir.TBool},
		{"isErr", ir.TBool},
		{"isOkAnd", ir.TBool},
		{"isErrAnd", ir.TBool},
		{"contains", ir.TBool},
		{"containsErr", ir.TBool},
		{"count", ir.TInt},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			got := builtinMethodReturnType(intStr, tc.method)
			if got == nil {
				t.Fatalf("builtinMethodReturnType(Result<Int, String>, %q) = nil, want %v", tc.method, tc.want)
			}
			if got.String() != tc.want.String() {
				t.Fatalf("builtinMethodReturnType(Result<Int, String>, %q) = %v, want %v", tc.method, got, tc.want)
			}
		})
	}
	// `ok` / `err` wrap into Option<T> / Option<E>.
	if got := builtinMethodReturnType(intStr, "ok"); got == nil {
		t.Fatal("builtinMethodReturnType(Result, \"ok\") = nil")
	} else if ot, ok := got.(*ir.OptionalType); !ok || ot.Inner != ir.TInt {
		t.Fatalf("builtinMethodReturnType(Result, \"ok\") = %v, want Option<Int>", got)
	}
	if got := builtinMethodReturnType(intStr, "err"); got == nil {
		t.Fatal("builtinMethodReturnType(Result, \"err\") = nil")
	} else if ot, ok := got.(*ir.OptionalType); !ok || ot.Inner != ir.TString {
		t.Fatalf("builtinMethodReturnType(Result, \"err\") = %v, want Option<String>", got)
	}
	// inspect / inspectErr hand back the same Result<T, E> shape.
	for _, m := range []string{"inspect", "inspectErr"} {
		t.Run(m, func(t *testing.T) {
			got := builtinMethodReturnType(intStr, m)
			if got == nil {
				t.Fatalf("builtinMethodReturnType(Result, %q) = nil, want Result<Int, String>", m)
			}
			nt, ok := got.(*ir.NamedType)
			if !ok || nt.Name != "Result" || len(nt.Args) != 2 || nt.Args[0] != ir.TInt || nt.Args[1] != ir.TString {
				t.Fatalf("builtinMethodReturnType(Result, %q) = %v, want Result<Int, String>", m, got)
			}
		})
	}
	t.Run("toString", func(t *testing.T) {
		if got := builtinMethodReturnType(intStr, "toString"); got != ir.TString {
			t.Fatalf("builtinMethodReturnType(Result, \"toString\") = %v, want String", got)
		}
	})
	t.Run("toList", func(t *testing.T) {
		got := builtinMethodReturnType(intStr, "toList")
		nt, ok := got.(*ir.NamedType)
		if !ok || nt.Name != "List" || len(nt.Args) != 1 || nt.Args[0] != ir.TInt {
			t.Fatalf("builtinMethodReturnType(Result, \"toList\") = %v, want List<Int>", got)
		}
	})
}

// TestRecoveredMethodReturnTypeListCollectionMethods exercises the
// `*bodyState.recoveredMethodReturnType` arm for List<T> intrinsic
// methods whose return type is fully determined by the receiver:
//
//   - `indexOf` / `lastIndexOf` → Option<Int> (per
//     mir.go::IntrinsicListIndexOf which produces an `Int?` dest)
//   - `toSet` → Set<T> (per IntrinsicListToSet)
//
// Recovery uses `bs.l.listElementType(recvT)` so a poisoned MIR temp
// produced by a checker-skipped interpolation (`"{xs.indexOf(needle)}"`)
// still gets a usable type for downstream lowering.
//
// The test runs through a real `bodyState` constructed from a tiny
// module so the lowerer's element-type helpers are wired. We don't try
// to hit a runtime — just verify the recovery function returns the
// expected ir.Type for each method.
func TestRecoveredMethodReturnTypeListCollectionMethods(t *testing.T) {
	listInt := &ir.NamedType{Name: "List", Args: []ir.Type{ir.TInt}, Builtin: true}
	bs := newRecoveryTestBodyState()
	cases := []struct {
		method string
		want   ir.Type
	}{
		{"indexOf", &ir.OptionalType{Inner: ir.TInt}},
		{"lastIndexOf", &ir.OptionalType{Inner: ir.TInt}},
		{"toSet", &ir.NamedType{Name: "Set", Args: []ir.Type{ir.TInt}, Builtin: true}},
		{"len", ir.TInt},
		{"isEmpty", ir.TBool},
		{"contains", ir.TBool},
		{"toString", ir.TString},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			got := bs.recoveredMethodReturnType(listInt, tc.method)
			if got == nil {
				t.Fatalf("recoveredMethodReturnType(List<Int>, %q) = nil, want %v", tc.method, tc.want)
			}
			if got.String() != tc.want.String() {
				t.Fatalf("recoveredMethodReturnType(List<Int>, %q) = %v, want %v", tc.method, got, tc.want)
			}
		})
	}
}

// TestRecoveredMethodReturnTypeSetCollectionMethods covers Set<T>'s
// `toList` recovery (per IntrinsicSetToList) alongside the existing
// `len`/`isEmpty`/`contains`/`toString` set.
func TestRecoveredMethodReturnTypeSetCollectionMethods(t *testing.T) {
	setStr := &ir.NamedType{Name: "Set", Args: []ir.Type{ir.TString}, Builtin: true}
	bs := newRecoveryTestBodyState()
	cases := []struct {
		method string
		want   ir.Type
	}{
		{"toList", &ir.NamedType{Name: "List", Args: []ir.Type{ir.TString}, Builtin: true}},
		{"len", ir.TInt},
		{"isEmpty", ir.TBool},
		{"contains", ir.TBool},
		{"toString", ir.TString},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			got := bs.recoveredMethodReturnType(setStr, tc.method)
			if got == nil {
				t.Fatalf("recoveredMethodReturnType(Set<String>, %q) = nil, want %v", tc.method, tc.want)
			}
			if got.String() != tc.want.String() {
				t.Fatalf("recoveredMethodReturnType(Set<String>, %q) = %v, want %v", tc.method, got, tc.want)
			}
		})
	}
}

// newRecoveryTestBodyState builds a minimal `*bodyState` with the
// lowerer plumbing required by `bs.l.listElementType` and
// `bs.l.setElementType`. The lowerer's stdlib / module state are left
// empty — the helpers under test only need the receiver type's
// argument list to extract the element type.
func newRecoveryTestBodyState() *bodyState {
	l := &lowerer{}
	return &bodyState{l: l}
}

// TestBuiltinMethodReturnTypeOptionUncoveredMethodReturnsNil locks the
// "no recovery" branch for two distinct categories of Option calls:
//
//   - Generic methods (`map`, `andThen`, `and`) whose return type
//     depends on the closure's inferred result type, which MIR doesn't
//     carry on the poisoned dest. Recovering with a placeholder would
//     invent a wrong type, so we leave them to the upstream checker.
//   - Methods that don't exist on Option at all (`getOr` belongs to Map
//     per internal/stdlib/modules/map.osty; the Option/Maybe arm must
//     not silently swallow it because doing so masks an invalid call
//     where the user meant `unwrapOr` and would otherwise see E0703).
func TestBuiltinMethodReturnTypeOptionUncoveredMethodReturnsNil(t *testing.T) {
	option := &ir.NamedType{Name: "Option", Args: []ir.Type{ir.TInt}, Builtin: true}
	for _, m := range []string{"map", "andThen", "and", "getOr"} {
		t.Run(m, func(t *testing.T) {
			if got := builtinMethodReturnType(option, m); got != nil {
				t.Fatalf("builtinMethodReturnType(Option, %q) = %v, want nil (generic / not-an-Option-method)", m, got)
			}
		})
	}
}
