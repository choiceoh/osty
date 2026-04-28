package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
)

func TestUrlModuleSurface(t *testing.T) {
	reg := LoadCached()

	mod := reg.Modules["url"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.url not loaded")
	}

	for _, name := range []string{"parse", "join"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Errorf("std.url missing export %q", name)
			continue
		}
		if sym.Kind != resolve.SymFn {
			t.Errorf("std.url.%s kind = %s, want SymFn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Errorf("std.url.%s not public", name)
		}
		fn, ok := sym.Decl.(*ast.FnDecl)
		if !ok || fn.Body == nil {
			t.Errorf("std.url.%s has no bodied implementation", name)
		}
	}

	sym := mod.Package.PkgScope.LookupLocal("Url")
	if sym == nil {
		t.Fatalf("std.url missing Url")
	}
	if sym.Kind != resolve.SymStruct {
		t.Fatalf("std.url.Url kind = %s, want SymStruct", sym.Kind)
	}
	urlDecl, ok := sym.Decl.(*ast.StructDecl)
	if !ok {
		t.Fatalf("std.url.Url decl = %T, want *ast.StructDecl", sym.Decl)
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
		"decodePercentComponent(",
		"removeDotSegments(",
		"hostNeedsBrackets(",
		"keys().sorted()",
		"userinfo not supported",
		"invalid percent-escape",
		"bracket IPv6 literals in authority",
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
