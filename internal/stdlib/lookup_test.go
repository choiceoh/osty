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

func TestLookupFnDeclFindsOptionAndResultHelpers(t *testing.T) {
	reg := LoadCached()
	cases := []struct {
		module string
		name   string
	}{
		{"option", "flatten"},
		{"option", "transpose"},
		{"option", "unzip"},
		{"option", "values"},
		{"option", "any"},
		{"option", "all"},
		{"option", "traverse"},
		{"option", "filterMap"},
		{"option", "findMap"},
		{"option", "map2"},
		{"option", "map3"},
		{"result", "flatten"},
		{"result", "transpose"},
		{"result", "values"},
		{"result", "errors"},
		{"result", "partition"},
		{"result", "all"},
		{"result", "traverse"},
		{"result", "map2"},
		{"result", "map3"},
		{"result", "allErrors"},
		{"result", "traverseErrors"},
	}
	for _, tc := range cases {
		fn := reg.LookupFnDecl(tc.module, tc.name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(%s, %s) = nil, want *ast.FnDecl", tc.module, tc.name)
		}
		if fn.Body == nil {
			t.Fatalf("LookupFnDecl(%s, %s) body = nil, want bodied helper", tc.module, tc.name)
		}
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

func TestLookupMethodDeclFindsStructMethod(t *testing.T) {
	reg := LoadCached()
	fn := reg.LookupMethodDecl("encoding", "Hex", "encode")
	if fn == nil {
		t.Fatalf("LookupMethodDecl(encoding, Hex, encode) = nil, want *ast.FnDecl")
	}
	if fn.Name != "encode" {
		t.Fatalf("fn.Name = %q, want encode", fn.Name)
	}
	if len(fn.Params) != 1 {
		t.Fatalf("fn.Params = %d, want 1 (the `data` param; self is implicit)", len(fn.Params))
	}
}

func TestLookupMethodDeclFindsEnumMethod(t *testing.T) {
	reg := LoadCached()
	fn := reg.LookupMethodDecl("option", "Option", "isSome")
	if fn == nil {
		t.Fatalf("LookupMethodDecl(option, Option, isSome) = nil, want *ast.FnDecl")
	}
	if fn.Name != "isSome" {
		t.Fatalf("fn.Name = %q, want isSome", fn.Name)
	}
	if fn.Body == nil {
		t.Fatalf("fn.Body = nil, want bodied enum method")
	}
}

func TestLookupMethodDeclFindsExpandedOptionAndResultMethods(t *testing.T) {
	reg := LoadCached()
	cases := []struct {
		module   string
		typeName string
		method   string
	}{
		{"option", "Option", "zipWith"},
		{"option", "Option", "reduce"},
		{"result", "Result", "zip"},
		{"result", "Result", "zipWith"},
		{"result", "Result", "toList"},
	}
	for _, tc := range cases {
		fn := reg.LookupMethodDecl(tc.module, tc.typeName, tc.method)
		if fn == nil {
			t.Fatalf("LookupMethodDecl(%s, %s, %s) = nil, want *ast.FnDecl", tc.module, tc.typeName, tc.method)
		}
		if fn.Body == nil {
			t.Fatalf("LookupMethodDecl(%s, %s, %s) body = nil, want bodied enum method", tc.module, tc.typeName, tc.method)
		}
	}
}

func TestLookupMethodDeclUnknownReturnsNil(t *testing.T) {
	reg := LoadCached()
	cases := []struct {
		module, typeName, methodName string
	}{
		{"encoding", "Hex", "no_such_method"},
		{"encoding", "NoSuchType", "encode"},
		{"no_such_module", "Hex", "encode"},
		{"", "Hex", "encode"},
		{"encoding", "", "encode"},
		{"encoding", "Hex", ""},
	}
	for _, tc := range cases {
		if got := reg.LookupMethodDecl(tc.module, tc.typeName, tc.methodName); got != nil {
			t.Errorf("LookupMethodDecl(%q, %q, %q) = %v, want nil",
				tc.module, tc.typeName, tc.methodName, got)
		}
	}
}

func TestLookupMethodDeclNilReceiver(t *testing.T) {
	var reg *Registry
	if got := reg.LookupMethodDecl("encoding", "Hex", "encode"); got != nil {
		t.Fatalf("nil.LookupMethodDecl = %v, want nil", got)
	}
}

// TestLookupMethodDeclAndFnAreDistinctSurfaces guards against the
// regression where a future refactor collapses methods and free fns
// into one lookup that treats `LookupFnDecl(module, methodName)` as
// hitting struct methods. Methods must remain hidden from the free-fn
// surface so existing callers don't accidentally route a method body
// through the free-fn injection path that does not pass a receiver.
func TestLookupMethodDeclAndFnAreDistinctSurfaces(t *testing.T) {
	reg := LoadCached()
	if got := reg.LookupFnDecl("encoding", "encode"); got != nil {
		t.Fatalf("LookupFnDecl(encoding, encode) = %v, want nil — encode is a struct method, not a free fn", got)
	}
	if got := reg.LookupMethodDecl("encoding", "Hex", "encode"); got == nil {
		t.Fatalf("LookupMethodDecl(encoding, Hex, encode) = nil, want non-nil")
	}
}
