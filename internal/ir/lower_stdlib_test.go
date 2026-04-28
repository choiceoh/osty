package ir

import (
	"testing"

	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// TestLowerFnDeclLowersStdlibStringsCompare drives LowerFnDecl against a
// real stdlib function — strings.compare — using the stdlib's own
// resolve.Package for identifier context. The asserts here are deliberately
// loose (non-nil IR, no hard-fail issues): the goal is to verify that
// LowerFnDecl is usable with stdlib-owned resolve data rather than to
// lock in an exact IR shape that might legitimately change.
func TestLowerFnDeclLowersStdlibStringsCompare(t *testing.T) {
	out, issues := lowerStdlibFn(t, "strings", "compare")
	if out == nil {
		t.Fatalf("LowerFnDecl returned nil for strings.compare")
	}
	if out.Name != "compare" {
		t.Errorf("out.Name = %q, want compare", out.Name)
	}
	if out.Body == nil {
		t.Errorf("out.Body = nil, want lowered block")
	}
	if len(out.Params) != 2 {
		t.Errorf("out.Params = %d, want 2", len(out.Params))
	}
	// Issues are non-fatal; surface them for visibility but don't fail.
	for _, issue := range issues {
		t.Logf("non-fatal lowering issue: %v", issue)
	}
}

func TestLowerFnDeclLowersStdlibNetSplitHostPort(t *testing.T) {
	out, issues := lowerStdlibFn(t, "net", "splitHostPort")
	if out == nil {
		t.Fatalf("LowerFnDecl returned nil for net.splitHostPort")
	}
	if out.Name != "splitHostPort" {
		t.Errorf("out.Name = %q, want splitHostPort", out.Name)
	}
	if out.Body == nil {
		t.Errorf("out.Body = nil, want lowered block")
	}
	if len(out.Params) != 1 {
		t.Errorf("out.Params = %d, want 1", len(out.Params))
	}
	for _, issue := range issues {
		t.Logf("non-fatal lowering issue: %v", issue)
	}
}

func TestLowerFnDeclLowersStdlibNetResolve(t *testing.T) {
	out, issues := lowerStdlibFn(t, "net", "resolve")
	if out == nil {
		t.Fatalf("LowerFnDecl returned nil for net.resolve")
	}
	if out.Name != "resolve" {
		t.Errorf("out.Name = %q, want resolve", out.Name)
	}
	if out.Body == nil {
		t.Errorf("out.Body = nil, want lowered block")
	}
	if len(out.Params) != 1 {
		t.Errorf("out.Params = %d, want 1", len(out.Params))
	}
	for _, issue := range issues {
		t.Logf("non-fatal lowering issue: %v", issue)
	}
}

func TestLowerFnDeclLowersStdlibNetTcpConnect(t *testing.T) {
	out, issues := lowerStdlibFn(t, "net", "tcpConnect")
	if out == nil {
		t.Fatalf("LowerFnDecl returned nil for net.tcpConnect")
	}
	if out.Name != "tcpConnect" {
		t.Errorf("out.Name = %q, want tcpConnect", out.Name)
	}
	if out.Body == nil {
		t.Errorf("out.Body = nil, want lowered block")
	}
	if len(out.Params) != 1 {
		t.Errorf("out.Params = %d, want 1", len(out.Params))
	}
	for _, issue := range issues {
		t.Logf("non-fatal lowering issue: %v", issue)
	}
}

func lowerStdlibFn(t *testing.T, module, name string) (*FnDecl, []error) {
	t.Helper()
	reg := stdlib.LoadCached()
	fn := reg.LookupFnDecl(module, name)
	if fn == nil {
		t.Fatalf("stdlib.LookupFnDecl(%s, %s) = nil — stdlib stubs missing?", module, name)
	}
	mod := reg.Modules[module]
	if mod == nil || mod.Package == nil || len(mod.Package.Files) == 0 {
		t.Fatalf("stdlib %s module missing parsed package", module)
	}
	pf := mod.Package.Files[0]
	res := &resolve.Result{
		RefsByID:      pf.RefsByID,
		TypeRefsByID:  pf.TypeRefsByID,
		RefIdents:     pf.RefIdents,
		TypeRefIdents: pf.TypeRefIdents,
		FileScope:     pf.FileScope,
	}
	return LowerFnDecl("stdlib_"+module, fn, res, nil)
}
