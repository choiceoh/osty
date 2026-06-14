package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ir"
)

func warningContaining(warnings []error, substr string) error {
	for _, w := range warnings {
		if w != nil && strings.Contains(w.Error(), substr) {
			return w
		}
	}
	return nil
}

// TestPrepareEntryInjectsStdlib verifies injection runs in the real
// PrepareEntry path by inspecting entry.IR directly — bypasses the
// llvmgen/AST legacy bridge so we see the HIR-level result without any
// downstream transformations. Stdlib body injection is default-on; no
// env override is required.
func TestPrepareEntryInjectsStdlib(t *testing.T) {
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

// TestPrepareEntryInjectsStdlibGlobals verifies that the body-injection
// pipeline pulls in stdlib `pub let` definitions
// (top-level globals) referenced by injected fn bodies. Concrete case:
// `strings.graphemes` chains through `graphemeBreakProperty` which
// reads `graphemeBreakCR` (a `pub let`) — without this pass the
// emitted `.ll` references `@graphemeBreakCR` undefined and clang
// fails at link time.
//
// The verification is structural: after PrepareEntry, the user
// module's IR must contain at least one mangled
// `osty_std_strings__graphemeBreak*` LetDecl, and no injected fn
// body may still carry a bare `Ident{Kind: IdentGlobal, Name:
// "graphemeBreakCR"}` — every reference must resolve to the
// mangled name.
func TestPrepareEntryInjectsStdlibGlobals(t *testing.T) {
	req := newBackendRequest(t, EmitBinary, `use std.strings

fn main() {
    let n = strings.graphemes("hi").len()
    println(n)
}
`)
	if req.Entry.IR == nil {
		t.Fatalf("entry.IR is nil")
	}
	letNames := map[string]bool{}
	for _, d := range req.Entry.IR.Decls {
		ld, ok := d.(*ir.LetDecl)
		if !ok || ld == nil {
			continue
		}
		letNames[ld.Name] = true
	}
	foundMangledLet := false
	for n := range letNames {
		if strings.HasPrefix(n, "osty_std_strings__graphemeBreak") {
			foundMangledLet = true
			break
		}
	}
	if !foundMangledLet {
		names := make([]string, 0, len(letNames))
		for n := range letNames {
			names = append(names, n)
		}
		t.Fatalf("expected at least one osty_std_strings__graphemeBreak* LetDecl in entry.IR; have lets: %v", names)
	}
	var unmangled []string
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		id, ok := n.(*ir.Ident)
		if !ok || id == nil || id.Kind != ir.IdentGlobal {
			return true
		}
		if strings.HasPrefix(id.Name, "graphemeBreak") && !strings.HasPrefix(id.Name, "osty_std_strings__graphemeBreak") {
			unmangled = append(unmangled, id.Name)
		}
		return true
	}), req.Entry.IR)
	if len(unmangled) > 0 {
		t.Fatalf("injected bodies still carry unmangled global Idents: %v", unmangled)
	}
}
