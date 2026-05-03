package backend

import (
	"context"
	"strings"
	"testing"
)

// TestLLVMBackendEmitFmtJoinWithGenericClosure locks in the fix for
// monomorphized `fmt.joinWith<T>` end-to-end emission. The wall this
// covered:
//
//   - After ir.Monomorphize specializes joinWith<T> to T=Int, the
//     `mapper(items[i])` call inside the StringLit interpolation
//     `"{result}{sep}{mapper(items[i])}"` lost its checker-side type
//     annotations: the IR Ident kept Kind=IdentUnknown / Type=<error>
//     and the surrounding CallExpr inherited <error> as its T.
//
//   - That poisoned the MIR temp the emitter allocated for the call's
//     return value, and the lowerer routed the indirect call as a
//     direct FnRef{Symbol: "mapper"}.
//
//   - The two halves combined to produce two cascading walls:
//     `unsupported local type <error>` and
//     `call to unresolved symbol mapper`.
//
// The fix lives in two places (internal/mir/lower.go):
//
//  1. recoverFnTypedLocalReturn — when CallExpr.Callee is a bare
//     Ident and global signature lookup fails, scan fn.Locals for a
//     same-named fn-typed slot and pull the FnType.Return.
//  2. resolveCall — IdentUnknown callees that resolve to a fn-typed
//     local route as IndirectCall instead of falling through to a
//     "call to unresolved symbol" FnRef.
//
// Without either half, the joinWith program rejects with one of the
// two LLVM000 walls; with both, the LLVM backend emits cleanly. We
// scan all warnings — the goal is to catch any regression that
// surfaces either pattern again.
func TestLLVMBackendEmitFmtJoinWithGenericClosure(t *testing.T) {
	t.Setenv("OSTY_STDLIB_BODY_LOWER", "1")
	src := `use std.fmt
fn main() {
    let xs: List<Int> = [1, 2, 3]
    let s = fmt.joinWith(xs, ", ", |i| "{i}")
    println(s)
}
`
	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitBinary, src)
	res, err := backend.Emit(context.Background(), req)
	if err != nil {
		if res != nil {
			for i, w := range res.Warnings {
				t.Logf("warning[%d]: %v", i, w)
			}
		}
		t.Fatalf("Emit failed: %v", err)
	}
	if res != nil {
		for _, w := range res.Warnings {
			msg := w.Error()
			if strings.Contains(msg, "<error>") {
				t.Errorf("warning surfaces <error>-typed local: %s", msg)
			}
			if strings.Contains(msg, "unresolved symbol mapper") {
				t.Errorf("indirect call to mapper routed as FnRef: %s", msg)
			}
		}
	}
}
