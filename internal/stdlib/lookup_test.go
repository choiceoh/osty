package stdlib

import "testing"

func TestLookupFnDeclFindsStringsCompare(t *testing.T) {
	reg := LoadCached()
	fn := reg.LookupFnDecl("strings", "compare")
	if fn == nil {
		t.Fatalf("LookupFnDecl(strings, compare) = nil, want *ast.FnDecl")
	}
	if fn.Name != "compare" {
		t.Fatalf("fn.Name = %q, want compare", fn.Name)
	}
	if fn.Body == nil {
		t.Fatalf("fn.Body = nil, want non-nil body for a bodied stdlib fn")
	}
	if len(fn.Params) != 2 {
		t.Fatalf("fn.Params = %d, want 2", len(fn.Params))
	}
}

func TestLookupFnDeclUnknownReturnsNil(t *testing.T) {
	reg := LoadCached()
	cases := []struct {
		module, name string
	}{
		{"strings", "nonexistent_fn_zzz"},
		{"no_such_module", "compare"},
		{"", "compare"},
		{"strings", ""},
	}
	for _, tc := range cases {
		if got := reg.LookupFnDecl(tc.module, tc.name); got != nil {
			t.Errorf("LookupFnDecl(%q, %q) = %v, want nil", tc.module, tc.name, got)
		}
	}
}

func TestLookupFnDeclNilReceiver(t *testing.T) {
	var reg *Registry
	if got := reg.LookupFnDecl("strings", "compare"); got != nil {
		t.Fatalf("nil.LookupFnDecl = %v, want nil", got)
	}
}
