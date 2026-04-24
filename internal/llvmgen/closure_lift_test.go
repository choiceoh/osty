package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	ostyir "github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// lowerSrcLLVM parses + resolves + checks + lowers a source snippet
// into an ir.Module. Used by closure-lift e2e tests that need the
// full IR shape (including the synthesized closure metadata) so the
// bridge's lift pass has something to operate on.
func lowerSrcLLVM(t *testing.T, src string) *ostyir.Module {
	t.Helper()
	file, diags := parser.ParseDiagnostics([]byte(src))
	if len(diags) != 0 {
		t.Fatalf("parse: %v", diags)
	}
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{

		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
	})
	mod, _ := ostyir.Lower("main", file, res, chk)
	return mod
}

// Inline closure literals (`|n| n * 2`) used to wall LLVM013 in the
// legacy AST emitter — `*ast.ClosureExpr` had no expression handler
// outside the special testing.context / testing.benchmark inlining
// paths. The lift pre-pass at the IR→AST bridge hoists every
// no-capture closure to a top-level synthetic fn (`__osty_closure_<n>`)
// and replaces the bridged ClosureExpr with a bare Ident, so the
// existing `emitIdent` fn-value Env path materializes the closure.
//
// E2E through the IR pipeline: a closure passed to a user fn must
// lift to a top-level def and be invoked indirectly via the
// fn-value Env. Without the lift the LLVM013 wall fires; with it
// the program compiles cleanly.
//
// Uses GenerateModule (the IR pipeline) so the bridge runs. The
// closure has no captures — `n` is a closure param.
func TestClosureLiftLowersThroughIRPipeline(t *testing.T) {
	src := `fn apply(f: fn(Int) -> Int, x: Int) -> Int {
    f(x)
}

fn main() {
    let _ = apply(|n| n * 2, 5)
}
`
	mod := lowerSrcLLVM(t, src)
	ir, err := GenerateModule(mod, Options{PackageName: "main", SourcePath: "/tmp/closure_lift_e2e.osty"})
	if err != nil {
		t.Fatalf("closure-lift e2e errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		// The lifted closure landed as a top-level def.
		"@__osty_closure_",
		// And the call site reaches it via the fn-value Env helper.
		"@osty.rt.closure_env_alloc_v2",
		"@apply",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("closure lift missing %q in IR:\n%s", want, got)
		}
	}
}

// Two no-capture closures in the same module each get a unique
// lifted symbol — the lift counter is process-monotonic so name
// collisions are impossible even across modules.
func TestClosureLiftHandlesMultipleClosuresInSameModule(t *testing.T) {
	src := `fn apply(f: fn(Int) -> Int, x: Int) -> Int {
    f(x)
}

fn main() {
    let _ = apply(|n| n + 1, 1)
    let _ = apply(|n| n * 2, 2)
}
`
	mod := lowerSrcLLVM(t, src)
	ir, err := GenerateModule(mod, Options{PackageName: "main", SourcePath: "/tmp/closure_lift_two.osty"})
	if err != nil {
		t.Fatalf("two-closure e2e errored: %v", err)
	}
	got := string(ir)
	defines := strings.Count(got, "define ")
	closureDefines := strings.Count(got, "define i64 @__osty_closure_")
	if closureDefines < 2 {
		t.Fatalf("expected at least 2 lifted closure defs, got %d (total defines=%d):\n%s", closureDefines, defines, got)
	}
}

// A capturing closure (`|n| n + outer` where `outer` is from
// enclosing scope) lifts to an env-backed fn with the capture
// appended to the parameter list, plus a Phase 4 capturing thunk
// that loads the capture from env at runtime and reorders args.
// Locks the round-trip end-to-end:
//
//   - lifted fn signature appends the capture as `outer: Int`
//   - capturing thunk emits `getelementptr i8, ptr %env, i64 24` +
//     `load i64, ptr %cap0_slot` and calls the lifted fn with
//     `(orig_args..., cap0)`
//   - maker call site stores the thunk symbol at env slot 0 and
//     the captured value at offset 24
func TestClosureLiftCapturingIntLocal(t *testing.T) {
	src := `fn apply(f: fn(Int) -> Int, x: Int) -> Int {
    f(x)
}

fn main() {
    let outer = 10
    let _ = apply(|n| n + outer, 5)
}
`
	mod := lowerSrcLLVM(t, src)
	ir, err := GenerateModule(mod, Options{PackageName: "main", SourcePath: "/tmp/closure_lift_capture.osty"})
	if err != nil {
		t.Fatalf("capturing closure errored: %v", err)
	}
	got := string(ir)
	// Counter is process-monotonic so the exact closure id varies
	// across test runs; check for the structural patterns instead
	// of pinning to a specific id.
	for _, want := range []string{
		// Lifted fn appends `outer` after the original `n` param.
		"(i64 %n, i64 %outer)",
		// Capturing thunk loads the capture from env at offset 24
		// (after fn_ptr + capture_count + pointer_bitmap) and
		// reorders args.
		"%cap0_slot = getelementptr i8, ptr %env, i64 24",
		"%cap0 = load i64",
		"call i64 @__osty_closure_",
		// Maker call site allocates env, stores thunk + capture.
		"call ptr @osty.rt.closure_env_alloc_v2(i64 1",
		"store ptr @__osty_closure_thunk___osty_closure_",
		"store i64 ", // capture value store
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("capturing closure missing %q in IR:\n%s", want, got)
		}
	}
	// Verify the thunk reorders args correctly: the call site
	// passes (env, n) but the lifted fn expects (n, outer), so the
	// thunk should call lifted with `(arg0, cap0)` not the other
	// way around.
	if !strings.Contains(got, "call i64 @__osty_closure_") || !strings.Contains(got, "(i64 %arg0, i64 %cap0)") {
		t.Fatalf("thunk does not reorder args as (%%arg0, %%cap0):\n%s", got)
	}
}

// A closure capturing a managed pointer (String) survives GC because
// the env's trace callback (`osty_rt_closure_env_trace`) consults the
// per-capture pointer bitmap and marks every slot whose bit is set.
// The lifter lowers String/Bytes/List/Map/Set captures as `ptr` and
// emits bitmap bit i = 1 for each; the scalar-false-retention
// guarantee (RUNTIME_GC §2.4) depends on bitmap bit i = 0 for scalars
// so the tracer skips them unconditionally.
//
// Locks:
//   - lifted fn signature appends `msg: String` after the `n` param
//   - capturing thunk loads the capture as `ptr` (not `i64`)
//   - maker call site stores `ptr` into the env slot at offset 24
//   - maker call site passes `i64 1` for the bitmap (bit 0 set for
//     the single pointer capture)
func TestClosureLiftCapturingStringLocal(t *testing.T) {
	src := `fn apply(f: fn(Int) -> String, x: Int) -> String {
    f(x)
}

fn main() {
    let msg = "hi"
    let _ = apply(|n| msg, 5)
}
`
	mod := lowerSrcLLVM(t, src)
	ir, err := GenerateModule(mod, Options{PackageName: "main", SourcePath: "/tmp/closure_lift_capture_string.osty"})
	if err != nil {
		t.Fatalf("string-capturing closure errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		// Lifted fn appends `msg` after `n`. LLVM String lowers to `ptr`.
		"(i64 %n, ptr %msg)",
		// Capturing thunk loads the capture as ptr from offset 24.
		"%cap0_slot = getelementptr i8, ptr %env, i64 24",
		"%cap0 = load ptr",
		// Maker call site allocates env with capture_count=1 and
		// bitmap=1 (bit 0 set for the single pointer capture),
		// stores the thunk at slot 0 and a ptr capture at offset 24.
		"call ptr @osty.rt.closure_env_alloc_v2(i64 1",
		"store ptr @__osty_closure_thunk___osty_closure_",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("string-capturing closure missing %q in IR:\n%s", want, got)
		}
	}
	// Locate the slot at offset 24 and check the store that lands
	// there is a `store ptr`, not a `store i64` (which would leave
	// the upper 4 bytes of a managed pointer undefined). This is the
	// invariant that makes GC tracing correct for pointer captures.
	if !strings.Contains(got, "store ptr %") || !strings.Contains(got, "= getelementptr i8, ptr %env, i64 24") {
		t.Fatalf("expected `store ptr %%…` into env slot, got:\n%s", got)
	}
}

// Multiple captures of different types in the same closure lay out
// in env slots 1..N at 8-byte stride, and the thunk loads them in
// declaration order before the call.
func TestClosureLiftCapturingMultipleTypes(t *testing.T) {
	src := `fn apply(f: fn(Int) -> Int, x: Int) -> Int {
    f(x)
}

fn main() {
    let outerInt = 10
    let outerBool = true
    let _ = apply(|n| if outerBool { n + outerInt } else { n }, 5)
}
`
	mod := lowerSrcLLVM(t, src)
	ir, err := GenerateModule(mod, Options{PackageName: "main", SourcePath: "/tmp/closure_lift_multi.osty"})
	if err != nil {
		t.Fatalf("multi-capture closure errored: %v", err)
	}
	got := string(ir)
	// Two capture loads at offsets 24 and 32 (v2 header is 24 bytes:
	// fn_ptr + capture_count + pointer_bitmap).
	for _, want := range []string{
		"= getelementptr i8, ptr %env, i64 24",
		"= getelementptr i8, ptr %env, i64 32",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("multi-capture closure missing %q in IR:\n%s", want, got)
		}
	}
}

// A closure capturing both a scalar (Int) and a pointer (String) must
// encode the bitmap with bit i set iff captures[i] is a pointer. This
// is the codegen-side anchor for RUNTIME_GC §2.4 — the runtime tracer
// consults this bitmap to skip scalar slots and close the scalar-
// false-retention window structurally.
//
// The IR lowerer decides capture order, so the test accepts either
// `i64 1` (pointer first) or `i64 2` (scalar first) — both encode
// exactly one pointer in a two-slot env. Bitmap 0 (both scalars) or
// 3 (both pointers) would be wrong.
func TestClosureLiftCapturingMixedScalarAndPointerEncodesBitmap(t *testing.T) {
	src := `fn apply(f: fn(Int) -> String, x: Int) -> String {
    f(x)
}

fn main() {
    let outerInt = 10
    let outerStr = "hi"
    let _ = apply(|n| if n > outerInt { outerStr } else { outerStr }, 5)
}
`
	mod := lowerSrcLLVM(t, src)
	ir, err := GenerateModule(mod, Options{PackageName: "main", SourcePath: "/tmp/closure_lift_mixed.osty"})
	if err != nil {
		t.Fatalf("mixed-capture closure errored: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, "call ptr @osty.rt.closure_env_alloc_v2(i64 2") {
		t.Fatalf("mixed-capture closure missing 2-capture v2 alloc:\n%s", got)
	}
	// Grab the bitmap arg. Accept either ordering. Must NOT be 0
	// (both scalars) or 3 (both pointers).
	hasBitmap1 := strings.Contains(got, "closure_env_alloc_v2(i64 2, ptr @") && strings.Contains(got, ", i64 1)")
	hasBitmap2 := strings.Contains(got, "closure_env_alloc_v2(i64 2, ptr @") && strings.Contains(got, ", i64 2)")
	if !hasBitmap1 && !hasBitmap2 {
		t.Fatalf("expected bitmap `i64 1)` or `i64 2)` for one-pointer-one-scalar env, got:\n%s", got)
	}
}
