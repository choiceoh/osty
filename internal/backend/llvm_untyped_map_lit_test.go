package backend

import (
	"context"
	"strings"
	"testing"
)

// TestLLVMBackendEmitUntypedMapLitFor locks in the entry-driven
// recovery for untyped map literals. Before this fix, the bare
// `let m = {"a": 1}` shape walled at MIR with `for-in over Map
// with unresolved key/value type` because:
//
//   - The Go-side checker left `Types[m]` as `Map<<error>,
//     <error>>` — top-level shape pinned but K/V dropped.
//   - `lowerMapLit` populated KeyT/ValT from `l.exprType(m)`
//     verbatim, so the ir.MapLit carried the same poisoned args.
//   - `lowerLet`'s `usableRecoveredType` chain stopped at the IR
//     binding level (it hooked the LetStmt fine, with the right
//     type), but `lowerIdent` re-resolved `m`'s type via
//     `chk.SymTypes[sym]` which still produced the poisoned
//     `Map<<error>, <error>>`.
//   - The for-iter Ident inherited that type and the MIR
//     for-in lowerer rejected it.
//
// Fix in `internal/ir/lower.go`:
//
//  1. `lowerMapLit` — when the recorded MapLit type is missing or
//     poisoned (PrimInvalid placeholders + ErrType args both),
//     pull KeyT/ValT off the first entry's lowered Key/Value
//     types.
//  2. `hasPoisonedTypeArg` — extended to also catch
//     `*PrimType{Kind: PrimInvalid}` (the checker's "unknown
//     type" placeholder for type-args).
//  3. `lowerIdent` — when the resolved type carries a poisoned
//     type-arg, fall through to `identTypeFromDecl`.
//  4. `identTypeFromDecl` — added an `*ast.IdentPat` case (the
//     shape resolve.Symbol records for `let x = ...` bindings)
//     that consults `bindingPatTypes`, which `lowerLetStmt`
//     already populates with the entry-recovered type.
//
// Coverage:
//
//   - `for (k, v) in m { ... }` over an untyped map literal.
//   - `m.len()` on an untyped map literal.
//   - `m.entries()` for-in (alternative iter form).
//
// `int_to_string` (a follow-up `println(v.unwrapOr("?"))` where the
// value type is String) is *not* covered here — that surfaces a
// separate "println of non-primitive V" wall on the unwrapOr
// result, deferred to a follow-up.
func TestLLVMBackendEmitUntypedMapLitFor(t *testing.T) {
	t.Setenv("OSTY_STDLIB_BODY_LOWER", "1")
	cases := []struct {
		name string
		src  string
	}{
		{
			"for_in_direct",
			`fn main() {
    let m = {"a": 1}
    for (k, v) in m {
        println(v)
    }
}`,
		},
		{
			"len_method",
			`fn main() {
    let m = {"a": 1, "b": 2}
    println(m.len())
}`,
		},
		{
			"for_in_entries",
			`fn main() {
    let m = {"a": 1}
    for (k, v) in m.entries() {
        println("{k}={v}")
    }
}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tc := &fakeLLVMToolchain{}
			backend := LLVMBackend{toolchain: tc}
			req := newBackendRequest(t, EmitBinary, c.src)
			res, err := backend.Emit(context.Background(), req)
			if err != nil {
				if res != nil {
					for i, w := range res.Warnings {
						t.Logf("warning[%d]: %v", i, w)
					}
				}
				t.Fatalf("Emit failed: %v", err)
			}
			if res != nil {
				for _, w := range res.Warnings {
					msg := w.Error()
					if strings.Contains(msg, "unresolved key/value type") {
						t.Errorf("regression: untyped map K/V leaked to MIR: %s", msg)
					}
				}
			}
		})
	}
}
