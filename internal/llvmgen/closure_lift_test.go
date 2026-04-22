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

// Closures with captures are out of scope for this PR — they need a
// Phase 4 capture-env layout (see MIR's emitClosureEnv). The lift
// pre-pass deliberately skips them, leaving the legacy emitter to
// trip the existing LLVM013 wall with its original message. Locks
// the boundary so a future capture-aware lifter can flip it
// confidently.
func TestClosureLiftSkipsCapturingClosure(t *testing.T) {
	src := `fn apply(f: fn(Int) -> Int, x: Int) -> Int {
    f(x)
}

fn main() {
    let outer = 10
    let _ = apply(|n| n + outer, 5)
}
`
	mod := lowerSrcLLVM(t, src)
	_, err := GenerateModule(mod, Options{PackageName: "main", SourcePath: "/tmp/closure_lift_capture.osty"})
	if err == nil {
		t.Fatalf("capturing closure expected to wall (LLVM013 or LLVM015) for this PR")
	}
	if !strings.Contains(err.Error(), "LLVM01") && !strings.Contains(err.Error(), "Closure") {
		t.Fatalf("capturing closure walled with unexpected error: %v", err)
	}
}
