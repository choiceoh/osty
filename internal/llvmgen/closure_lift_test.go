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
	chk := check.File(file, res, check.Opts{
		UseGolegacy:   true,
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
		"@osty.rt.closure_env_alloc_v1",
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
//   - capturing thunk emits `getelementptr i8, ptr %env, i64 16` +
//     `load i64, ptr %cap0_slot` and calls the lifted fn with
//     `(orig_args..., cap0)`
//   - maker call site stores the thunk symbol at env slot 0 and
//     the captured value at offset 16
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
		// Capturing thunk loads the capture from env at offset 16
		// and reorders args.
		"%cap0_slot = getelementptr i8, ptr %env, i64 16",
		"%cap0 = load i64",
		"call i64 @__osty_closure_",
		// Maker call site allocates env, stores thunk + capture.
		"call ptr @osty.rt.closure_env_alloc_v1(i64 1",
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
	// Two capture loads at offsets 16 and 24.
	for _, want := range []string{
		"= getelementptr i8, ptr %env, i64 16",
		"= getelementptr i8, ptr %env, i64 24",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("multi-capture closure missing %q in IR:\n%s", want, got)
		}
	}
}
