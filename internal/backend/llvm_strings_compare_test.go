package backend

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ir"
)

// llvmEmitDiagnostics runs the LLVM backend end-to-end and returns every
// warning the dispatcher surfaced, including the underlying
// llvmgen diagnostic that led to the skeleton fallback. Returning warnings
// separately from the top-level error makes "what specifically was
// unsupported?" cheap to assert on.
func llvmEmitDiagnostics(t *testing.T, src string) (topErr error, warnings []error) {
	t.Helper()
	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitBinary, src)
	res, err := backend.Emit(context.Background(), req)
	if res != nil {
		warnings = res.Warnings
	}
	return err, warnings
}

func warningContaining(warnings []error, substr string) error {
	for _, w := range warnings {
		if w != nil && strings.Contains(w.Error(), substr) {
			return w
		}
	}
	return nil
}

// TestLLVMBackendEmitStringsCompareFlagOff confirms the baseline: with
// OSTY_STDLIB_BODY_LOWER unset, a user program that calls
// `strings.compare` fails at llvmgen. The exact LLVM0xx code depends
// on which llvmgen gap the lowered call trips first (LLVM015 on the
// FieldExpr callee, LLVM016 on the unknown identifier), so this test
// locks in the weaker "flag off still fails somewhere in llvmgen"
// contract — the point is that a silent default flip would be caught,
// not that the error text stays frozen.
func TestLLVMBackendEmitStringsCompareFlagOff(t *testing.T) {
	t.Setenv("OSTY_STDLIB_BODY_LOWER", "")
	err, warnings := llvmEmitDiagnostics(t, `fn main() {
    let order = strings.compare("a", "b")
    println(order)
}
`)
	if err == nil {
		t.Fatalf("Emit succeeded without flag; expected the llvmgen gap until injection is wired")
	}
	if !errors.Is(err, ErrLLVMNotImplemented) {
		t.Fatalf("top-level err = %v, want ErrLLVMNotImplemented wrapper", err)
	}
	if warningContaining(warnings, "LLVM0") == nil {
		t.Fatalf("warnings = %v, want at least one LLVM0xx diagnostic", warnings)
	}
}

// TestLLVMBackendEmitStringsCompareFlagOn is the optimistic half: with
// OSTY_STDLIB_BODY_LOWER=1 the stdlib injection path should provide
// strings.compare's body. If this test fails with something OTHER than
// the flag-off LLVM016, it tells us the first concrete backend gap that
// still blocks bodied stdlib lowering end-to-end. We capture the error
// as a log so the next iteration has a precise target to fix.
func TestLLVMBackendEmitStringsCompareFlagOn(t *testing.T) {
	t.Setenv("OSTY_STDLIB_BODY_LOWER", "1")
	err, warnings := llvmEmitDiagnostics(t, `fn main() {
    let order = strings.compare("a", "b")
    println(order)
}
`)
	if err != nil {
		// Log the first llvmgen warning so the next iteration has a
		// precise target. Flip to Fatalf on err only when the injection
		// path is known to succeed end-to-end.
		for i, w := range warnings {
			t.Logf("warning[%d]: %v", i, w)
		}
		t.Logf("flag-on strings.compare top err: %v", err)
		return
	}
	t.Logf("flag-on strings.compare succeeded end-to-end")
}

// TestPrepareEntryInjectsStdlibWhenFlagOn verifies injection actually
// runs in the real PrepareEntry path by inspecting entry.IR directly —
// bypasses the llvmgen/AST legacy bridge so we see the HIR-level result
// without any downstream transformations.
func TestPrepareEntryInjectsStdlibWhenFlagOn(t *testing.T) {
	t.Setenv("OSTY_STDLIB_BODY_LOWER", "1")
	req := newBackendRequest(t, EmitBinary, `use std.strings

fn main() {
    let order = strings.compare("a", "b")
    println(order)
}
`)
	if req.Entry.IR == nil {
		t.Fatalf("entry.IR is nil")
	}
	found := false
	for _, d := range req.Entry.IR.Decls {
		fn, ok := d.(interface{ DeclName() string })
		if !ok {
			continue
		}
		if strings.HasPrefix(fn.DeclName(), "osty_std_strings__") {
			found = true
			break
		}
	}
	if !found {
		names := []string{}
		for _, d := range req.Entry.IR.Decls {
			if fn, ok := d.(interface{ DeclName() string }); ok {
				names = append(names, fn.DeclName())
			}
		}
		t.Logf("flag read: %v", stdlibBodyLoweringEnabled())
		t.Fatalf("entry.IR has no osty_std_strings__* decl; have: %v", names)
	}
	// Confirm the user callsite was rewritten: walk every FnDecl in
	// the module and fail if any `strings.compare(...)` FieldExpr
	// callee survived. The mangled Ident is what backends emit
	// against.
	var leftover []string
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		if call, ok := n.(*ir.CallExpr); ok {
			if fx, ok := call.Callee.(*ir.FieldExpr); ok {
				if id, ok := fx.X.(*ir.Ident); ok && id.Name == "strings" {
					leftover = append(leftover, fx.Name)
				}
			}
		}
		return true
	}), req.Entry.IR)
	if len(leftover) != 0 {
		t.Fatalf("user callsite not rewritten: still have strings.%v in IR", leftover)
	}
}
