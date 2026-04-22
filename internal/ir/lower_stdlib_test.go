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
	reg := stdlib.LoadCached()
	fn := reg.LookupFnDecl("strings", "compare")
	if fn == nil {
		t.Fatalf("stdlib.LookupFnDecl(strings, compare) = nil — stdlib stubs missing?")
	}
	mod := reg.Modules["strings"]
	if mod == nil || mod.Package == nil || len(mod.Package.Files) == 0 {
		t.Fatalf("stdlib strings module missing parsed package")
	}
	pf := mod.Package.Files[0]
	res := &resolve.Result{
		RefsByID:      pf.RefsByID,
		TypeRefsByID:  pf.TypeRefsByID,
		RefIdents:     pf.RefIdents,
		TypeRefIdents: pf.TypeRefIdents,
		FileScope:     pf.FileScope,
	}

	out, issues := LowerFnDecl("stdlib_strings", fn, res, nil)
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
