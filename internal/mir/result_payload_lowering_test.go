package mir

import (
	"strings"
	"testing"
)

// TestLowerResultPayloadProjectsThroughVariant covers the canonical
// `Result<T, E>` payload projection shapes through the full source →
// MIR pipeline. Companion to
// `TestLowerOptionalStructPayloadProjectsThroughVariant` for the
// other prelude sum type.
//
// Plan entry: 🟡 Tier B "`Result<T, String>` ABI" / "struct/enum
// 복합 payload" rows in `LLVM_MIGRATION_PLAN.md` document the
// LLVM-side payload widening; this test locks the MIR layer so
// regressions in the front-end / variant-info lookup land here
// before the LIR Proto parity fixtures.
//
// PR #1820 added `builtinVariantPayloadType` to synthesise prelude
// Option/Result payload types when `variantLayout` returns nil
// (builtin enums whose decls don't live in the user module). This
// test exercises the Result side of that synthesis. Without the
// fallback, Ok/Err pattern bindings would lower to `<error>` typed
// locals and downstream field projection / arithmetic would all be
// poisoned.
//
// Each subtest exercises one canonical Result shape and asserts:
//
//  1. the MIR contains the expected payload-projection /
//     discriminant probe shape;
//  2. the bound payload's declared type is concrete (no `<error>`);
//  3. `?` propagation through Result re-wraps the Err side with
//     the original variant args rather than a synthesised
//     ErrType payload.
func TestLowerResultPayloadProjectsThroughVariant(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantMIR []string
	}{
		{
			name: "match_ok_struct_payload",
			src: `struct Box { n: Int }

fn unbox(r: Result<Box, String>) -> Int {
    match r {
        Ok(box) -> box.n,
        Err(_) -> 0,
    }
}
`,
			wantMIR: []string{
				"@Ok",
				".n",
				"discriminant",
			},
		},
		{
			name: "if_let_ok_struct_payload",
			src: `struct Box { n: Int }

fn unbox(r: Result<Box, String>) -> Int {
    if let Ok(box) = r {
        box.n
    } else {
        0
    }
}
`,
			wantMIR: []string{
				"@Ok.0",
				// Locked in by builtinVariantPayloadType — without
				// the synth fallback this slot would read
				// `<error>  // box`.
				"Box  // box",
				"discriminant",
			},
		},
		{
			name: "err_string_constructor",
			src: `fn fail() -> Result<Int, String> {
    Err("bad")
}
`,
			wantMIR: []string{
				"aggregate variant Err(",
			},
		},
		{
			name: "question_propagation_through_result",
			src: `fn outer() -> Result<Int, String> {
    let x = inner()?
    Ok(x + 1)
}

fn inner() -> Result<Int, String> {
    Ok(42)
}
`,
			wantMIR: []string{
				// `?` lowers to a discriminant probe + Ok-branch
				// payload bind + Err-branch early return.
				"discriminant",
				"@Ok.0",
				// Ok branch wraps the incremented value back.
				"aggregate variant Ok(",
				// Err branch propagates the Result through use of
				// the scrutinee local (no aggregation rebuild for
				// scalar payload types — copy is enough).
				"use _2",
			},
		},
		{
			name: "question_propagation_struct_payload",
			src: `struct Box { n: Int }

fn unbox(r: Result<Box, String>) -> Int {
    let b = r?
    b.n
}
`,
			wantMIR: []string{
				"@Ok.0",
				// Bound `b` carries its payload type, not <error>.
				"Box  // b",
				// Err propagation rebuilds the Result with the
				// original Err payload by-name.
				"aggregate variant Err(",
				"@Err.0",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mirText := lowerSourceToMIR(t, c.src)
			for _, w := range c.wantMIR {
				if !strings.Contains(mirText, w) {
					t.Errorf("missing %q in MIR:\n%s", w, mirText)
				}
			}
			for _, b := range []string{
				"unsupported projection",
				"projection on non-aggregate",
			} {
				if strings.Contains(mirText, b) {
					t.Errorf("GAP-INSTR-006 marker %q in MIR:\n%s", b, mirText)
				}
			}
			if strings.Contains(mirText, "<error>") {
				t.Errorf("ErrTypeVal leaked into MIR for %s:\n%s", c.name, mirText)
			}
		})
	}
}
