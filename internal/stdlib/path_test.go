package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
)

func TestPathModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["path"]
	if mod == nil || mod.File == nil {
		t.Fatalf("std.path not loaded")
	}
	decls := map[string]ast.Decl{}
	for _, decl := range mod.File.Decls {
		if fn, ok := decl.(*ast.FnDecl); ok {
			decls[fn.Name] = fn
		}
	}
	for _, name := range []string{"join", "split", "extension", "dirname", "basename", "isAbsolute", "separator"} {
		fn, ok := decls[name].(*ast.FnDecl)
		if !ok {
			t.Fatalf("std.path missing fn %q", name)
		}
		if !fn.Pub {
			t.Errorf("std.path.%s not public", name)
		}
		if fn.Body == nil {
			t.Errorf("std.path.%s body = nil, want lexical Osty implementation", name)
		}
	}
	for _, name := range []string{"absolute", "canonical"} {
		fn, ok := decls[name].(*ast.FnDecl)
		if !ok {
			t.Fatalf("std.path missing fn %q", name)
		}
		if !fn.Pub {
			t.Errorf("std.path.%s not public", name)
		}
		if fn.Body != nil {
			t.Errorf("std.path.%s body != nil, want host-runtime boundary", name)
		}
	}
}

func TestOsModuleNoLongerOwnsPathHelpers(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["os"]
	if mod == nil {
		t.Fatal("stdlib os module missing")
	}
	src := string(mod.Source)
	for _, forbidden := range []string{"pub let path", "pub struct Path", "fn join", "fn basename", "fn dirname"} {
		if strings.Contains(src, forbidden) {
			t.Fatalf("std.os still exposes path helper surface %q", forbidden)
		}
	}
}
