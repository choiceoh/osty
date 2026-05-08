package backend

import (
	"context"
	"strings"
	"testing"

	"github.com/osty/osty/internal/mir"
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
			var sawPrintString bool
			var sawToStringCall bool
			withNativeMIRPayloadEmitter(t, func(entry Entry, target string) ([]byte, bool, []error, error) {
				sawPrintString, sawToStringCall = printlnToStringMIRShape(entry.MIR)
				return []byte("; stub native MIR payload IR\nsource_filename = \"stub.osty\"\n"), true, nil, nil
			})
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
			if !sawPrintString {
				t.Fatal("regression: println MIR payload did not carry a String operand")
			}
			if !sawToStringCall {
				t.Fatal("regression: println MIR payload did not call a toString method before printing")
			}
		})
	}
}

func printlnToStringMIRShape(module *mir.Module) (sawPrintString bool, sawToStringCall bool) {
	if module == nil {
		return false, false
	}
	for _, fn := range module.Functions {
		if fn == nil {
			continue
		}
		for _, block := range fn.Blocks {
			if block == nil {
				continue
			}
			for _, instr := range block.Instrs {
				switch x := instr.(type) {
				case *mir.CallInstr:
					if ref, ok := x.Callee.(*mir.FnRef); ok && strings.Contains(ref.Symbol, "__toString") {
						sawToStringCall = true
					}
				case *mir.IntrinsicInstr:
					if x.Kind == mir.IntrinsicPrintln && len(x.Args) == 1 && x.Args[0].Type() == mir.TString {
						sawPrintString = true
					}
				}
			}
		}
	}
	return sawPrintString, sawToStringCall
}
