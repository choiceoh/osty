package backend

import (
	"context"
	"strings"
	"testing"
)

// TestLLVMBackendEmitForInSet locks in `for x in set { ... }`
// support. Before this fix, the program walled with
// `for-in over non-List/Map/Channel iterable is not lowered to MIR
// yet: _ZTSN4main3SetIlEE` because `lowerForIn` (`internal/mir/lower.go`)
// only special-cased Channel / Map / Range / List iterables.
//
// Fix: added `lowerForInSet` — same shape as `lowerForInMap` but
// uses `IntrinsicSetToList` to materialise the set as a `List<T>`
// snapshot, then iterates by index. Two helper predicates
// (`isSetType`, `setElementType`) round out the symmetry with the
// existing List/Map helpers.
func TestLLVMBackendEmitForInSet(t *testing.T) {
	t.Setenv("OSTY_STDLIB_BODY_LOWER", "1")
	installNativeMIRPayloadStub(t)
	cases := []struct {
		name string
		src  string
	}{
		{
			"int_set",
			`fn main() {
    let mut s: Set<Int> = {}
    s.insert(1)
    s.insert(2)
    for x in s {
        println(x)
    }
}`,
		},
		{
			"string_set",
			`fn main() {
    let mut s: Set<String> = {}
    s.insert("a")
    s.insert("b")
    for x in s {
        println(x)
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
					if strings.Contains(w.Error(), "non-List/Map/Channel iterable") {
						t.Errorf("regression: Set for-in not handled: %s", w.Error())
					}
				}
			}
		})
	}
}
