package llvmgen

import (
	"errors"
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
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

// End-to-end: full compilation pipeline (parser -> resolve -> check ->
// ir.Lower -> mir.Lower -> GenerateFromMIR, with legacy fallback on
// ErrUnsupported) produces coalesce semantics on the same path that real
// `osty build` takes. The MIR path now handles Option<String> directly, so
// the proof checks for the lowered tagged-option branch rather than legacy
// `coalesce.*` labels.
func TestGenerateCoalesceFullPipeline(t *testing.T) {
	src := `fn resolve(name: String?) -> String {
    name ?? "anonymous"
}

fn main() {}
`
	file := parseLLVMGenFile(t, src)
	res := resolve.ResolveFileDefault(file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{

		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
		Privileged:    true,
	})
	mod, issues := ir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower issues: %v", issues)
	}
	monoMod, monoErrs := ir.Monomorphize(mod)
	if len(monoErrs) != 0 {
		t.Fatalf("monomorphize: %v", monoErrs)
	}
	mirMod := mir.Lower(monoMod)
	opts := Options{PackageName: "main", SourcePath: "/tmp/pipeline_coalesce.osty"}

	// Mirror the dispatcher: MIR first, legacy fallback on ErrUnsupported.
	out, err := GenerateFromMIR(mirMod, opts)
	if err != nil {
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("GenerateFromMIR hard error (not ErrUnsupported): %v", err)
		}
		out, err = GenerateModule(mod, opts)
		if err != nil {
			t.Fatalf("GenerateModule fallback error: %v", err)
		}
	}

	got := string(out)
	for _, want := range []string{
		"%Option.string = type { i64, i64 }",
		"define ptr @resolve(%Option.string %arg0)",
		"switch i64",
		"i64 1, label",
		"inttoptr i64",
		"store ptr @.str.0",
		"ret ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("full-pipeline IR missing %q:\n%s", want, got)
		}
	}
}

// Right-side side effects (and their GC-visible fallout) must
// execute exactly once and only on the None path. Here the RHS is a
// user function call that would normally emit an `osty.gc.safepoint`
// marker — it must appear after the `coalesce.none` label, never
// before the null-check, and never twice.
func TestGenerateCoalesceRightSideEffectsLazy(t *testing.T) {
	file := parseLLVMGenFile(t, `fn compute() -> Int {
    42
}

fn pick(age: Int?) -> Int {
    age ?? compute()
}
`)

	irOut, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/coalesce_sideffect.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(irOut)

	callIdx := strings.Index(got, "call i64 @compute()")
	if callIdx < 0 {
		t.Fatalf("RHS call `@compute` missing from IR:\n%s", got)
	}
	if strings.Count(got, "call i64 @compute()") != 1 {
		t.Fatalf("expected exactly one call to @compute (lazy, once), got %d:\n%s",
			strings.Count(got, "call i64 @compute()"), got)
	}
	noneIdx := strings.Index(got, "coalesce.none")
	if noneIdx < 0 || callIdx < noneIdx {
		t.Fatalf("RHS call (%d) must appear after `coalesce.none` label (%d):\n%s",
			callIdx, noneIdx, got)
	}
}

// Canonical spec pattern (LANG_SPEC_v0.5 §4 / CLAUDE.md §A.6):
// `user?.profile?.title ?? "Untitled"` — optional chain feeding a
// coalesce. Exercises the interaction between `?.` (each hop emits
// its own null-check + phi) and `??` (final fallback) so a
// regression in either direction surfaces here.
func TestGenerateCoalesceOptionalChainFallback(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Profile {
    title: String,
}

struct User {
    pub profile: Profile?,
}

fn titleOf(user: User?) -> String {
    user?.profile?.title ?? "Untitled"
}
`)

	irOut, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/coalesce_chain_opt.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(irOut)
	// Each `?.` emits an `optional.*` label, and the `??` emits its
	// own coalesce.* labels. Missing either means one half of the
	// composition regressed.
	for _, want := range []string{
		"optional.then",
		"optional.nil",
		"coalesce.some",
		"coalesce.none",
		"phi ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("optional-chain + coalesce IR missing %q:\n%s", want, got)
		}
	}
}
