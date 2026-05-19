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
	// take / replace / or / orElse / filter all keep the same
	// Option<T> shape — verify the Inner type is preserved verbatim.
	for _, m := range []string{"take", "replace", "or", "orElse", "filter"} {
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
