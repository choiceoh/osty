package stage0

import (
	"strings"
	"testing"
)

// TestCoerceScalarForStoreBoolUsesIcmpNeNull is the regression test for
// the PR #1882 review bug fix: opaque-pointer → bool coercion used to
// `ptrtoint ptr ... to i64` then `trunc i64 ... to i1`, which only
// reads the low bit of the pointer and was always 0 for aligned heap
// pointers (effectively "result := false" regardless of the pointer
// value). The fix emits `icmp ne ptr %v, null` so the result reflects
// the actual null-ness of the operand. Locking down here so the
// regression can't silently come back.
func TestCoerceScalarForStoreBoolUsesIcmpNeNull(t *testing.T) {
	nextSSA := 0
	ctx := &whileLoopEmitCtx{nextSSA: &nextSSA}
	var out strings.Builder
	got, ok := coerceScalarForStore(ctx, &out, "%v", scalarOpaquePtr, scalarBool)
	if !ok {
		t.Fatalf("coerceScalarForStore opaque→bool returned ok=false (want true)")
	}
	emitted := out.String()
	if !strings.Contains(emitted, "icmp ne ptr %v, null") {
		t.Fatalf("opaque→bool should emit `icmp ne ptr %%v, null`, got:\n%s", emitted)
	}
	if strings.Contains(emitted, "ptrtoint") || strings.Contains(emitted, "trunc") {
		t.Fatalf("opaque→bool must not use the old ptrtoint+trunc pair, got:\n%s", emitted)
	}
	if got == "" || got == "%v" {
		t.Fatalf("returned register should be a fresh SSA name, got %q", got)
	}
}

// TestCoerceScalarForStorePtrToIntPair covers the sibling opaque-ptr
// → int coercion that the review noted was already correct (ptrtoint
// to i64). Locks the ladder shape so a future refactor can't drop the
// case without an explicit test signal.
func TestCoerceScalarForStorePtrToIntPair(t *testing.T) {
	nextSSA := 0
	ctx := &whileLoopEmitCtx{nextSSA: &nextSSA}
	var out strings.Builder
	got, ok := coerceScalarForStore(ctx, &out, "%v", scalarOpaquePtr, scalarInt)
	if !ok {
		t.Fatalf("coerceScalarForStore opaque→int returned ok=false (want true)")
	}
	if !strings.Contains(out.String(), "ptrtoint ptr %v to i64") {
		t.Fatalf("opaque→int should emit `ptrtoint ptr %%v to i64`, got:\n%s", out.String())
	}
	if got == "" || got == "%v" {
		t.Fatalf("returned register should be a fresh SSA name, got %q", got)
	}
}

// TestCoerceScalarForStorePtrShape verifies the trivial cases — both
// pointer-shaped scalars (String/OpaquePtr) and strict match — emit no
// cast and return the original expression. These short-circuit paths
// were the PR #1877 starting point and are easy to break by accident
// when adding new ladder rungs.
func TestCoerceScalarForStorePtrShape(t *testing.T) {
	cases := []struct {
		name string
		from scalarType
		to   scalarType
	}{
		{"strict match int", scalarInt, scalarInt},
		{"strict match ptr", scalarOpaquePtr, scalarOpaquePtr},
		{"string ↔ opaque", scalarString, scalarOpaquePtr},
		{"opaque ↔ string", scalarOpaquePtr, scalarString},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nextSSA := 0
			ctx := &whileLoopEmitCtx{nextSSA: &nextSSA}
			var out strings.Builder
			got, ok := coerceScalarForStore(ctx, &out, "%v", c.from, c.to)
			if !ok {
				t.Fatalf("expected ok=true")
			}
			if got != "%v" {
				t.Fatalf("expected no-op (returns %%v), got %q", got)
			}
			if out.Len() != 0 {
				t.Fatalf("expected no IR emitted, got:\n%s", out.String())
			}
		})
	}
}
