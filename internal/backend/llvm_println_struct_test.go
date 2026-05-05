package backend

import (
	"context"
	"strings"
	"testing"
)

// TestLLVMBackendEmitPrintlnStructAutoToString covers auto-dispatch
// of `.toString()` for non-primitive print arguments. Programs like:
//
//	struct P {
//	    x: Int; y: Int
//	    fn toString(self) -> String { "({self.x}, {self.y})" }
//	}
//	fn main() {
//	    let p = P { x: 1, y: 2 }
//	    println(p)
//	}
//
// previously walled with `LLVM000 println of non-primitive P`
// because `emitPrintlnLike` (the LLVM-side single-value print path)
// only handles primitives natively. The user-visible `println(p)`
// was effectively unsupported even though the corresponding
// interpolation form `"{p}"` already routed through the
// string-concat boxing chain.
//
// Fix in `internal/ir/lower.go` — `lowerCall`:
//
//   - When a print-family intrinsic (`print`/`println`/`eprint`/
//     `eprintln`) sees a non-primitive arg whose type isn't
//     poisoned and isn't a builtin container, wrap the arg in an
//     explicit `MethodCall{Name: "toString", T: TString}`.
//   - The wrapped call routes to the user-defined / auto-derived
//     `toString` impl through the existing method-dispatch path,
//     and the resulting String hits `emitPrintlnLike`'s String
//     arm naturally.
//
// Test cases:
//   - User struct with explicit toString → cleanly compiles.
//   - Stdlib Uuid (returned by `uuid.v4()`) — has its own toString,
//     same path triggers auto-wrap and resolves to the stdlib impl.
//
// Structs without a toString method now produce a sharper
// `call to unresolved symbol P__toString` diagnostic instead of
// the opaque `non-primitive P` wall — actionable for the user.
// Auto-derived ToString implementations are tracked separately;
// covered by a follow-up.
func TestLLVMBackendEmitPrintlnStructAutoToString(t *testing.T) {
	t.Setenv("OSTY_STDLIB_BODY_LOWER", "1")
	installNativeMIRPayloadStub(t)
	cases := []struct {
		name string
		src  string
	}{
		{
			"user_struct_with_toString",
			`struct P {
    x: Int
    y: Int

    fn toString(self) -> String {
        "({self.x}, {self.y})"
    }
}
fn main() {
    let p = P { x: 1, y: 2 }
    println(p)
}`,
		},
		{
			"uuid_stdlib_struct",
			`use std.uuid
fn main() {
    let id = uuid.v4()
    println(id)
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
					if strings.Contains(w.Error(), "non-primitive") {
						t.Errorf("regression: non-primitive println not wrapped: %s", w.Error())
					}
				}
			}
		})
	}
}
