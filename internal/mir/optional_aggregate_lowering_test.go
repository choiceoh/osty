package mir

import (
	"strings"
	"testing"
)

// TestLowerOptionalStructPayloadProjectsThroughVariant covers the
// canonical Option<Struct> payload projection shapes used across
// the toolchain. Plan entry: 🔴 Tier A "Optional aggregate lowering
// — struct payload `?` / `?.field` 미커버" in `LLVM_MIGRATION_PLAN.md`.
// The LIR Proto layer already has parity fixtures
// (`source_optional_field_chain`) for the LLVM-side
// `extractvalue` / null-phi cascade; this test locks the matching
// MIR layer so a regression at either layer surfaces in isolation
// rather than being shadowed by the other.
//
// Each subtest is a single-fn source program that exercises one
// canonical Option<Struct> shape, and asserts:
//
//  1. the MIR contains the expected projection / discriminant
//     probe shape (e.g. `_<x>@Some.0.<field>` for `?.field` chain,
//     `_<x>@Some` for match-arm payload bind);
//  2. no GAP-INSTR-006 markers (`unsupported projection` /
//     `projection on non-aggregate`) leak through;
//  3. no `<error>` types leak through the body — the intermediate
//     coalesce target and any `none` constructor must carry the
//     concrete `Int?` type that `recoverFieldType` derives from
//     the receiver's optional struct payload. Without that
//     recovery, `?.field` on `Foo?` falls back to ErrTypeVal
//     because the checker doesn't always thread the optional-
//     chaining return type through.
func TestLowerOptionalStructPayloadProjectsThroughVariant(t *testing.T) {
	cases := []struct {
		name           string
		src            string
		wantMIR        []string
		dontWant       []string
		allowErrorType bool
	}{
		{
			name: "question_field_chain_with_default",
			src: `struct Box { n: Int }

fn get(b: Box?) -> Int {
    b?.n ?? 0
}
`,
			wantMIR: []string{
				// Through-variant chained projection: read the Box
				// payload's .n field without materialising the Box
				// itself into a scratch slot.
				"@Some.0.n",
				// Discriminant probe + branch on the chained Option.
				"discriminant",
				"switchInt",
				// None arm builds a None.
				"= none",
				// Coalesce default fallback.
				"const 0 Int",
			},
		},
		{
			name: "match_some_struct_payload_unwrap",
			src: `struct Box { n: Int }

fn get(b: Box?) -> Int {
    match b {
        Some(box) -> box.n,
        None -> -1,
    }
}
`,
			wantMIR: []string{
				// Pattern bind: the Some-arm projects the payload
				// into the named binding.
				"@Some",
				// Body reads the bound payload's .n field.
				".n",
				// None arm constructs the literal -1.
				"- const 1 Int",
			},
		},
		{
			name: "some_struct_lit_constructor",
			src: `struct Box { n: Int }

fn make() -> Box? {
    Some(Box { n: 42 })
}
`,
			wantMIR: []string{
				// Struct payload aggregated, then wrapped in Some.
				"aggregate struct(const 42 Int)",
				"aggregate variant Some(",
			},
		},
		{
			name: "none_with_struct_payload_type",
			src: `struct Box { n: Int }

fn make() -> Box? {
    let r: Box? = None
    r
}
`,
			wantMIR: []string{
				// The lowered None carries its declared Option<Box>
				// type, not the bare ErrType the checker emits for
				// bare `None`.
				"= none Box?",
			},
		},
		{
			name: "if_let_some_struct_field",
			src: `struct Box { n: Int }

fn print(b: Box?) -> Int {
    if let Some(box) = b {
        box.n
    } else {
        0
    }
}
`,
			wantMIR: []string{
				// `if let Some(box) = b` lowers to a discriminant
				// probe + Some-arm payload bind, same shape as a
				// 2-arm match. The bound `box` reaches the body as
				// a payload projection (`_<x>@Some.0`).
				"@Some.0",
				"discriminant",
				".n",
			},
			// The if-let payload binding's declared type currently
			// still reaches MIR as `<error>` because the checker
			// doesn't always thread the pattern position type
			// down to the binding — tracked separately from this
			// PR's `?.field` fix. The structural projection is
			// correct (`_x@Some.0` + `.n`) and downstream lowering
			// reads the field from the payload directly.
			allowErrorType: true,
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
			if !c.allowErrorType && strings.Contains(mirText, "<error>") {
				t.Errorf("ErrTypeVal leaked into MIR for %s:\n%s", c.name, mirText)
			}
			for _, dw := range c.dontWant {
				if strings.Contains(mirText, dw) {
					t.Errorf("MIR unexpectedly contains %q:\n%s", dw, mirText)
				}
			}
		})
	}
}
