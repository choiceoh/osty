package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/stdlib"
)

func TestStdlibCheckResultClipboard(t *testing.T) {
	reg := stdlib.LoadCached()
	chk := stdlibCheckResult(reg, "clipboard")
	if chk == nil {
		t.Fatalf("stdlibCheckResult(clipboard) = nil, want non-nil *check.Result")
	}
	var errs []string
	for _, d := range chk.Diags {
		if d != nil && strings.Contains(d.Error(), "error") {
			msg := d.Error()
			if len(d.Notes) > 0 {
				msg += "\n  notes: " + strings.Join(d.Notes, "\n  ")
			}
			errs = append(errs, msg)
		}
	}
	if len(errs) > 0 {
		t.Fatalf("clipboard module check produced %d error diagnostic(s):\n%s",
			len(errs), strings.Join(errs, "\n"))
	}
}

func TestInjectReachableClipboardPreservesOsRuntimeBoundary(t *testing.T) {
	reg := stdlib.LoadCached()
	call := &ir.CallExpr{
		Callee: &ir.FieldExpr{X: &ir.Ident{Name: "clipboard"}, Name: "readText"},
	}
	mod := &ir.Module{
		Package: "main",
		Script:  []ir.Stmt{&ir.ExprStmt{X: call}},
	}
	injected, issues := injectReachableStdlibBodies(mod, reg)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none for clipboard injection", issues)
	}
	names := fnDeclNames(injected)
	if !clipboardContainsString(names, "osty_std_clipboard__readText") {
		t.Fatalf("injected names = %v, want osty_std_clipboard__readText", names)
	}
	ident, ok := call.Callee.(*ir.Ident)
	if !ok || ident.Name != "osty_std_clipboard__readText" {
		t.Fatalf("clipboard call not rewritten to injected fn: %T %#v", call.Callee, call.Callee)
	}

	foundRuntimeExec := false
	for _, decl := range injected {
		ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
			call, ok := n.(*ir.CallExpr)
			if !ok || call == nil {
				return true
			}
			id, ok := call.Callee.(*ir.Ident)
			if ok && id.Name == "std.os.execWith" {
				foundRuntimeExec = true
			}
			return true
		}), decl)
	}
	if !foundRuntimeExec {
		t.Fatalf("clipboard injected bodies did not retain std.os.execWith runtime boundary; injected=%v", names)
	}
}

func clipboardContainsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
