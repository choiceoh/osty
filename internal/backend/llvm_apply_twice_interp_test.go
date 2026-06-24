package backend

import (
	"context"
	"strings"
	"testing"
)

// TestLLVMBackendEmitApplyTwiceInterp locks in the canonical
// user-facing repro from STRING_INTERP_RESOLVE_GAP.md (#1359):
//
//	fn applyTwice(mapper: fn(Int) -> String) -> String {
//	    "{mapper(1)}-{mapper(2)}"
//	}
//
// The plan calls this out specifically — the bug shows up only
// when a fn-typed parameter is called *inside* a string interpolation.
// `let r = mapper(1)` outside the interp lowered cleanly even before
// the fix; the wall fired on calls nested inside `"{...}"`.
//
// Coverage layers (each shipped independently, all kept by this test):
//
//   - #1360 (Day 1) — lexer exposes interp source byte ranges
//     (`OstyLexStringPart.srcStart` / `srcEnd`) so future parsers
//     can re-lex without depending on Go-side `interpolationTokens`.
//   - #1361 — MIR-side recovery: `recoverFnTypedLocalReturn` +
//     IdentUnknown→IndirectCall fallback so the joinWith family
//     compiles even when the IR Ident lost its annotation.
//   - #1364 (Day 2) — canonical Osty parser sub-parses interp
//     expressions into `AstNStringLit.children`.
//   - #1376 (Day 3) — verifies resolver's generic walker recurses
//     into `AstNStringLit.children`.
//
// Day 4's full architectural fix (Go-side `astbridge` consumes arena
// children directly, drops `interp_adapter.go`'s text re-parse path,
// preserves NodeIDs end-to-end) is blocked on regenerating
// `internal/selfhost/generated.go` from the canonical Osty source —
// the seed has the old parser. The MIR-layer recovery from #1361
// handles the user-facing case in the meantime, which is what this
// test verifies.
func TestLLVMBackendEmitApplyTwiceInterp(t *testing.T) {
	installNativeMIRPayloadStub(t)
	src := `fn applyTwice(mapper: fn(Int) -> String) -> String {
    "{mapper(1)}-{mapper(2)}"
}

fn main() {
    let r = applyTwice(|n| "v")
    println(r)
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
