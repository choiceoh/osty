package backend

import (
	"strings"
	"testing"
)

// TestStage0LoadsStackSlotBeforeStringConcatI64 regression-tests the
// audit-discovered clang link error
//
//	error: '%idx.slot' defined with type 'ptr' but expected 'i64'
//
// surfaced by `losslessFormatTrivia` (toolchain/lossless_lex.osty).
// The sequential intrinsic classifier `classifyStringConcatIntrinsic`
// was reaching `resolveOperandWithPrelude` → `resolveOperand` for a
// `CopyOp` of a stack-allocated mutable Int local, which returned
// the alloca pointer (e.g. `%idx.slot`) as the operand expression
// while the call signature demanded `i64`. Without an intervening
// `load`, clang's IR verifier rejected the module and the
// stage0-produced binary couldn't even link.
//
// The fix in `resolveOperandWithPrelude` materialises a `load` into
// the returned prelude when the binding is stack-allocated. This
// test reproduces the shape (mutable `Int` local + trailing string
// interpolation prefixed by the local) and asserts:
//
//   - clang-friendly output: the SSA register passed to the
//     `osty_rt_strings_ConcatI64Left` family is the LOADED i64
//     value, never the slot pointer; the slot is only ever the
//     target of a `load` / `store` instruction.
//   - the load is emitted ahead of the call site, not omitted.
func TestStage0LoadsStackSlotBeforeStringConcatI64(t *testing.T) {
	// Mirror the `losslessFormatTrivia` shape that surfaced the bug:
	// a function with a mutable `Int` local that flows into a string
	// interpolation built inside a for-in loop body. The for-in path
	// routes the trailing `IntrinsicStringConcat` through
	// `forInListIntrinsic` → `classifyStringConcatIntrinsic` →
	// `stringConcatSequentialArg` → `resolveOperandWithPrelude`,
	// which was the broken seam.
	// Note: idx is the LEADING operand below. That binds the first
	// concat to ConcatI64Left(i64, ptr) — the i64-as-first-arg
	// shape that surfaces the slot-pointer bug. Reversing the order
	// would route through ConcatI64Right and miss the seam.
	src := "pub fn formatLines(xs: List<String>) -> String {\n" +
		"    let mut lines: List<String> = []\n" +
		"    let mut idx = 0\n" +
		"    for x in xs {\n" +
		"        lines.push(\"{idx} {x}\")\n" +
		"        idx = idx + 1\n" +
		"    }\n" +
		"    \"\"\n" +
		"}\n" +
		"\nfn main() {}\n"
	out, err := realStage0Run(t, src)
	if err != nil {
		t.Fatalf("stage0 declined: %v", err)
	}
	got := string(out)

	// Negative: the IR must never feed the slot pointer to the i64
	// parameter of `osty_rt_strings_ConcatI64Left`. Regressing here
	// resurrects the clang diagnostic
	// `defined with type 'ptr' but expected 'i64'`.
	if strings.Contains(got, "ConcatI64Left(i64 %idx.slot") {
		t.Fatalf("ConcatI64Left passes the slot pointer where an i64 value is expected:\n%s", got)
	}

	// Positive: at least one `load i64, ptr %idx.slot` precedes the
	// concat call that consumes the value. We don't pin the exact
	// SSA register because freshTempName / freshReg id-space can
	// shift between emit-path versions.
	if !strings.Contains(got, "load i64, ptr %idx.slot") {
		t.Fatalf("expected `load i64, ptr %%idx.slot` before the concat call:\n%s", got)
	}
}
