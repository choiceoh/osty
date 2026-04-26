package llvmgen

import (
	"strings"
	"testing"
)

// hoistAllocasToEntry's job is to move any `alloca` line that landed in
// a non-entry basic block to just before the entry block's terminator.
// These tests drive each shape we expect to encounter from the legacy
// snapshot codegen path.

func TestHoistAllocasToEntry_SimpleLoopBody(t *testing.T) {
	// Mirrors the IR pattern that triggered the O(N²) bug in big-map:
	// `let s = i.toString()` inside `for i < n` emits the alloca into
	// %for.body1 instead of entry.
	body := []string{
		"  %t0 = alloca i64",
		"  store i64 0, ptr %t0",
		"  br label %for.cond0",
		"for.cond0:",
		"  %t1 = load i64, ptr %t0",
		"  %t2 = icmp slt i64 %t1, 100",
		"  br i1 %t2, label %for.body1, label %for.end",
		"for.body1:",
		"  %t3 = load i64, ptr %t0",
		"  %t4 = call ptr @osty_rt_int_to_string(i64 %t3)",
		"  %t5 = alloca ptr",
		"  store ptr %t4, ptr %t5",
		"  br label %for.cond0",
		"for.end:",
		"  ret i32 0",
	}
	got := hoistAllocasToEntry(body)
	want := []string{
		"  %t0 = alloca i64",
		"  store i64 0, ptr %t0",
		"  %t5 = alloca ptr",
		"  br label %for.cond0",
		"for.cond0:",
		"  %t1 = load i64, ptr %t0",
		"  %t2 = icmp slt i64 %t1, 100",
		"  br i1 %t2, label %for.body1, label %for.end",
		"for.body1:",
		"  %t3 = load i64, ptr %t0",
		"  %t4 = call ptr @osty_rt_int_to_string(i64 %t3)",
		"  store ptr %t4, ptr %t5",
		"  br label %for.cond0",
		"for.end:",
		"  ret i32 0",
	}
	assertSameLines(t, got, want)
}

func TestHoistAllocasToEntry_NoAllocas_ReturnsUnchanged(t *testing.T) {
	body := []string{
		"  br label %for.cond0",
		"for.cond0:",
		"  ret i32 0",
	}
	got := hoistAllocasToEntry(body)
	assertSameLines(t, got, body)
}

func TestHoistAllocasToEntry_AllocasAlreadyInEntry_LeftAlone(t *testing.T) {
	body := []string{
		"  %t0 = alloca i64",
		"  %t1 = alloca ptr",
		"  store i64 0, ptr %t0",
		"  br label %for.cond0",
		"for.cond0:",
		"  ret i32 0",
	}
	got := hoistAllocasToEntry(body)
	assertSameLines(t, got, body)
}

func TestHoistAllocasToEntry_NoBranch_ReturnsUnchanged(t *testing.T) {
	// Single-block function (no `br` terminator): every line is in
	// the entry block by definition; nothing to hoist.
	body := []string{
		"  %t0 = alloca i64",
		"  store i64 42, ptr %t0",
		"  %t1 = load i64, ptr %t0",
		"  ret i64 %t1",
	}
	got := hoistAllocasToEntry(body)
	assertSameLines(t, got, body)
}

func TestHoistAllocasToEntry_MultipleAllocasInLoop_PreservesOrder(t *testing.T) {
	// When several `let`s appear in the same loop body, their allocas
	// should hoist together AND retain their original relative order so
	// any dependency between later store-into-slot lines and the slot
	// pointer they reference still resolves.
	body := []string{
		"  br label %loop",
		"loop:",
		"  %t0 = alloca ptr",
		"  store ptr null, ptr %t0",
		"  %t1 = alloca i64",
		"  store i64 0, ptr %t1",
		"  %t2 = alloca double",
		"  store double 0.0, ptr %t2",
		"  br label %loop",
	}
	got := hoistAllocasToEntry(body)
	want := []string{
		"  %t0 = alloca ptr",
		"  %t1 = alloca i64",
		"  %t2 = alloca double",
		"  br label %loop",
		"loop:",
		"  store ptr null, ptr %t0",
		"  store i64 0, ptr %t1",
		"  store double 0.0, ptr %t2",
		"  br label %loop",
	}
	assertSameLines(t, got, want)
}

func TestHoistAllocasToEntry_Idempotent(t *testing.T) {
	// Running the hoist twice must produce the same body as running it
	// once — there should be no alloca past the entry's `br` after the
	// first pass, so the second pass is a no-op.
	body := []string{
		"  %t0 = alloca ptr",
		"  br label %loop",
		"loop:",
		"  %t1 = alloca i64",
		"  br label %loop",
	}
	once := hoistAllocasToEntry(body)
	twice := hoistAllocasToEntry(once)
	assertSameLines(t, once, twice)
}

func TestHoistAllocasToEntry_AllocaArrayShape(t *testing.T) {
	// `expr.go:247` emits `alloca [N x ptr]` for ConcatN's stack
	// arg vector. The hoist matcher must accept this shape too.
	body := []string{
		"  br label %loop",
		"loop:",
		"  %arr = alloca [4 x ptr]",
		"  store ptr @.str0, ptr %arr",
		"  br label %loop",
	}
	got := hoistAllocasToEntry(body)
	want := []string{
		"  %arr = alloca [4 x ptr]",
		"  br label %loop",
		"loop:",
		"  store ptr @.str0, ptr %arr",
		"  br label %loop",
	}
	assertSameLines(t, got, want)
}

func TestHoistAllocasToEntry_DoesNotMatchUnrelatedLines(t *testing.T) {
	// Lines that mention "alloca" inside other contexts (function calls,
	// metadata) must not be rewritten. The matcher only fires on the
	// canonical `  %name = alloca ...` form.
	body := []string{
		"  br label %loop",
		"loop:",
		"  %t0 = call ptr @some_alloca_lookalike()",
		"  call void @osty.gc.alloca_track(ptr %t0)",
		"  br label %loop",
	}
	got := hoistAllocasToEntry(body)
	assertSameLines(t, got, body)
}

func assertSameLines(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("line count mismatch: got %d, want %d\ngot:\n%s\nwant:\n%s",
			len(got), len(want),
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("line %d: got %q, want %q", i, got[i], want[i])
		}
	}
}
