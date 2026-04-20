package llvmgen

import (
	"strings"
	"testing"
)

// `a ?? b` on a `String?` (inner type is ptr) lowers to a branch+phi
// with no intermediate load — the optional ptr doubles as the Some
// payload. Right-side is lazy, reached only from the null branch, and
// the phi merges at `coalesce.end`.
func TestGenerateCoalescePtrInner(t *testing.T) {
	file := parseLLVMGenFile(t, `fn resolve(name: String?) -> String {
    name ?? "anonymous"
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/coalesce_ptr.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define ptr @resolve(ptr %name)",
		"icmp eq ptr",
		"coalesce.some",
		"coalesce.none",
		"coalesce.end",
		"phi ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// `a ?? b` on an `Int?` (inner type is i64, boxed) loads the payload
// from the non-null pointer in the Some branch and phis it against
// the literal fallback in the None branch. The phi must be typed
// `i64`, not `ptr`.
func TestGenerateCoalesceBoxedInt(t *testing.T) {
	file := parseLLVMGenFile(t, `fn ageOr(age: Int?) -> Int {
    age ?? 0
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/coalesce_int.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define i64 @ageOr(ptr %age)",
		"icmp eq ptr",
		"coalesce.some",
		"coalesce.none",
		"load i64, ptr",
		"phi i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// Right-side laziness is visible in the IR: the RHS instructions must
// be emitted inside the `coalesce.none` block, after the null-check
// branch. If the emitter eagerly evaluated both sides, the RHS would
// appear before the branch.
func TestGenerateCoalesceRightSideIsLazy(t *testing.T) {
	file := parseLLVMGenFile(t, `fn pick(a: Int?, b: Int, c: Int) -> Int {
    a ?? (b + c)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/coalesce_lazy.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	branchIdx := strings.Index(got, "coalesce.none")
	if branchIdx < 0 {
		t.Fatalf("missing coalesce.none label in IR:\n%s", got)
	}
	labelEnd := strings.Index(got[branchIdx:], ":")
	if labelEnd < 0 {
		t.Fatalf("malformed coalesce.none label (no trailing colon):\n%s", got)
	}
	afterLabel := branchIdx + labelEnd
	addIdx := strings.Index(got, "add i64 %b, %c")
	if addIdx < 0 {
		t.Fatalf("missing `add` for b + c in IR:\n%s", got)
	}
	if addIdx < afterLabel {
		t.Fatalf("right-side `add` (%d) appears before `coalesce.none` label (%d) — right side is not lazy:\n%s",
			addIdx, afterLabel, got)
	}
}

// `a ?? b ?? c` is right-associative per the grammar, so it parses
// as `a ?? (b ?? c)`. In IR that produces two nested branch+phi
// shapes — two `coalesce.end` labels and two phis.
func TestGenerateCoalesceChainsRightAssoc(t *testing.T) {
	file := parseLLVMGenFile(t, `fn pickTitle(a: String?, b: String?) -> String {
    a ?? b ?? "fallback"
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/coalesce_chain.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	if count := strings.Count(got, "coalesce.end"); count < 4 {
		// Two `??` ops × two references each (label use + definition)
		// in the textual IR. <4 hits means the inner `??` collapsed.
		t.Fatalf("expected chained `??` to produce two nested branch+phi shapes (>=4 `coalesce.end` occurrences), got %d:\n%s",
			count, got)
	}
	if count := strings.Count(got, "phi ptr"); count < 2 {
		t.Fatalf("expected two phi nodes for chained `??`, got %d:\n%s", count, got)
	}
}
