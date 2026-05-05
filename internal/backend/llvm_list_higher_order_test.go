package backend

import (
	"context"
	"strings"
	"testing"
)

// TestLLVMBackendEmitListHigherOrder covers `List<T>.map`,
// `List<T>.filter`, `List<T>.fold`, `List<T>.reduce`. Before this
// fix, these programs walled with `LLVM000 call to unresolved
// symbol _ZTSN4main4ListIlEE__map` (etc.) because:
//
//  1. `ir.ReachMethods`'s `methodReachVisitor` filtered MethodCalls
//     to types whose receiver `NamedType.Package` was non-empty,
//     but builtin generic types (`List<T>`, `Map<K,V>`, …) reach
//     IR with `Builtin: true` and `Package: ""`. The body-injector
//     never saw the call site, so the bodied stdlib helper
//     (`internal/stdlib/modules/collections.osty`) wasn't injected.
//  2. `RewriteStdlibMethodCallsites` carried the same gate, so
//     even if injection had run, the user-side call wouldn't have
//     been rewritten to the mangled symbol.
//  3. `methodToFreeFn` synthesised a free fn with `selfTy:
//     NamedType{Name: "List"}` — no type-args — and never moved
//     the owner struct's generics (`T` from `List<T>`) onto the
//     free fn's generics list. Even after fixing (1)/(2), the
//     monomorpher rejected the call site with
//     `arity mismatch: 2 type args vs 1 generics`.
//
// Fix:
//   - `ir.BuiltinTypeOwningModule(name)` — exported helper mapping
//     builtin generic names to the canonical module that owns
//     their bodied helpers. Today only `List` is whitelisted; Map
//     / Set primitive ops route through the runtime intrinsic
//     path, and Option / Result are dispatched per-intrinsic in
//     mir_generator.go.
//   - `methodReachVisitor` + `RewriteStdlibMethodCallsites` —
//     fall through `BuiltinTypeOwningModule` when `named.Package`
//     is empty.
//   - `methodToFreeFn` — looks up the owner type's generics via
//     the new `Registry.LookupTypeGenerics`, prepends them to the
//     free fn's generics list, and pins the self-param's
//     `NamedType.Args` to the matching `TypeVar` chain so the
//     body's `for item in self` substitutes correctly at
//     monomorph time.
//
// Note: `List<T>.any` / `List<T>.all` / `List<T>.forEach` still
// trip a separate "Closure: param[0] nil Type" wall — that's a
// closure-inference issue downstream and is tracked separately.
func TestLLVMBackendEmitListHigherOrder(t *testing.T) {
	t.Setenv("OSTY_STDLIB_BODY_LOWER", "1")
	installNativeMIRPayloadStub(t)
	cases := []struct {
		name string
		src  string
	}{
		{
			"map",
			`fn main() {
    let xs = [1, 2, 3]
    let doubled = xs.map(|x| x * 2)
    println(doubled.len())
}`,
		},
		{
			"filter",
			`fn main() {
    let xs = [1, 2, 3, 4]
    let evens = xs.filter(|x| x % 2 == 0)
    println(evens.len())
}`,
		},
		{
			"fold",
			`fn main() {
    let xs = [1, 2, 3, 4]
    let sum = xs.fold(0, |acc, x| acc + x)
    println(sum)
}`,
		},
		{
			"reduce",
			`fn main() {
    let xs: List<Int> = [1, 2, 3]
    let r = xs.reduce(|a, b| a + b)
    println(r.unwrapOr(0))
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
					if strings.Contains(msg, "unresolved symbol _ZTSN") {
						t.Errorf("regression: stdlib body wasn't injected: %s", msg)
					}
					if strings.Contains(msg, "arity mismatch") {
						t.Errorf("regression: free-fn generics mismatch: %s", msg)
					}
				}
			}
		})
	}
}
