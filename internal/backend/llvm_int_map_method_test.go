package backend

import (
	"context"
	"strings"
	"testing"
)

// TestLLVMBackendEmitIntMapMethodCalls covers method calls on
// untyped Int-keyed map literals where the result type is generic
// in V (Map<K, V>.get → V?, .remove → V?, .keys → List<K>, etc.).
//
// Before this fix, programs like:
//
//	fn main() {
//	    let m = {1: "one"}
//	    let v = m.get(1)
//	    println(v.unwrapOr("?"))
//	}
//
// walled with `LLVM000 println of non-primitive V` because the
// checker left `m.get(1)` typed as `V?` (the generic signature's
// TypeVar). Substitution from the receiver's type
// (`Map<Int, String>`) wasn't applied at the call site, so `V`
// leaked into the let binding's local and then into println.
//
// Fix in `internal/ir/lower.go`:
//
//   1. `recoverMethodReturnTypeFromType` — added Map<K, V> case
//      covering `.get`, `.remove`, `.keys`, `.values`,
//      `.containsKey`, `.insert`. Each pulls the substituted
//      arg straight off the receiver's type.
//   2. `lowerMethodCall` — fall through to the recovery path also
//      when the recorded type contains a TypeVar leak (not just
//      poisoned types). Uses `containsTypeVar` to detect, which
//      already exists for the monomorph subst pass.
//
// Companion to PR #1381 (untyped map K/V inference): together,
// `let m = {1: "one"}` followed by `m.get(1).unwrapOr("?")` now
// lowers cleanly without an explicit `Map<Int, String>`
// annotation.
func TestLLVMBackendEmitIntMapMethodCalls(t *testing.T) {
	t.Setenv("OSTY_STDLIB_BODY_LOWER", "1")
	cases := []struct {
		name string
		src  string
	}{
		{
			"get_unwrap_or",
			`fn main() {
    let m = {1: "one"}
    let v = m.get(1)
    println(v.unwrapOr("?"))
}`,
		},
		{
			"remove_unwrap_or",
			`fn main() {
    let mut m = {1: "one"}
    let r = m.remove(1)
    println(r.unwrapOr("?"))
}`,
		},
		{
			"keys_len",
			`fn main() {
    let m = {1: "one", 2: "two"}
    println(m.keys().len())
}`,
		},
		{
			"contains_key",
			`fn main() {
    let m = {1: "one"}
    println(m.containsKey(1))
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
					if strings.Contains(msg, "non-primitive V") {
						t.Errorf("regression: V TypeVar leaked: %s", msg)
					}
					if strings.Contains(msg, "non-primitive K") {
						t.Errorf("regression: K TypeVar leaked: %s", msg)
					}
				}
			}
		})
	}
}
