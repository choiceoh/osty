package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestGraphqlModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["graphql"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.graphql not loaded")
	}
	for _, name := range []string{"arg", "stringValue", "intValue", "floatValue", "boolValue", "nullValue", "variable", "listValue", "objectValue", "field", "fieldArgs", "selection", "operation", "query", "mutation", "subscription", "request", "namedRequest", "withVariablesJson", "encode", "isName"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.graphql missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.graphql.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.graphql.%s not public", name)
		}
	}
	for _, name := range []string{"Argument", "Request"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.graphql missing type %q", name)
		}
		if sym.Kind != resolve.SymStruct {
			t.Fatalf("std.graphql.%s kind = %s, want struct", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.graphql.%s not public", name)
		}
	}
}

func TestGraphqlModuleSourcePinsBuilderBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["graphql"]
	if mod == nil {
		t.Fatal("std.graphql module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub fn fieldArgs(name: String, args: List<Argument>, selectionSet: String) -> Result<String, Error>`,
		`pub fn operation(kind: String, name: String, selectionSet: String) -> Result<String, Error>`,
		`pub fn encode(request: Request) -> String`,
		`out = "{out},{quote}variables{quote}:{strings.trimSpace(request.variablesJson)}"`,
		`fn jsonString(value: String) -> String`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.graphql source missing %q", want)
		}
	}
}
