package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
)

func TestUrlModuleSurface(t *testing.T) {
	reg := LoadCached()

	mod := reg.Modules["url"]
	if mod == nil || mod.File == nil {
		t.Fatalf("std.url not loaded")
	}

	decls := map[string]ast.Decl{}
	for _, decl := range mod.File.Decls {
		switch n := decl.(type) {
		case *ast.FnDecl:
			decls[n.Name] = n
		case *ast.StructDecl:
			decls[n.Name] = n
		}
	}
	for _, name := range []string{"parse", "join"} {
		fn, ok := decls[name].(*ast.FnDecl)
		if !ok {
			t.Fatalf("std.url missing fn %q", name)
		}
		if !fn.Pub {
			t.Errorf("std.url.%s not public", name)
		}
		if fn.Body == nil {
			t.Errorf("std.url.%s body = nil, want bodied implementation", name)
		}
	}

	urlDecl, ok := decls["Url"].(*ast.StructDecl)
	if !ok {
		t.Fatalf("std.url missing Url struct")
	}
	fields := map[string]*ast.Field{}
	for _, f := range urlDecl.Fields {
		fields[f.Name] = f
	}
	for _, name := range []string{"scheme", "host", "port", "path", "query", "fragment"} {
		field := fields[name]
		if field == nil {
			t.Errorf("Url missing field %q", name)
			continue
		}
		if !field.Pub {
			t.Errorf("Url.%s not public", name)
		}
	}

	for _, method := range []string{"toString", "queryValues"} {
		fn := reg.LookupMethodDecl("url", "Url", method)
		if fn == nil {
			t.Fatalf("LookupMethodDecl(url, Url, %s) = nil", method)
		}
		if fn.Body == nil {
			t.Fatalf("Url.%s body = nil, want bodied method", method)
		}
	}
}

func TestUrlModuleSourcePinsQualityGuards(t *testing.T) {
	src := urlModuleSource(t)
	for _, want := range []string{
		"struct UrlParts",
		"removeDotSegments(",
		"hostNeedsBrackets(",
		"keys().sorted()",
		"userinfo not supported",
		"bracket IPv6 literals in authority",
		"percentEncodeChar(",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.url source missing %q", want)
		}
	}
}

func urlModuleSource(t *testing.T) string {
	t.Helper()
	reg := LoadCached()
	mod := reg.Modules["url"]
	if mod == nil {
		t.Fatal("stdlib url module missing")
	}
	return string(mod.Source)
}
