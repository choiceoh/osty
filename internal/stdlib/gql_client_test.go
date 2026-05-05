package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestGqlClientModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["gql_client"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.gql.client not loaded")
	}
	for _, name := range []string{
		"execute", "executeWithHeaders", "executeJson",
		"query", "mutate",
		"subscribe", "subscribeWithVars", "nextEvent", "unsubscribe",
		"parseData",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.gql.client missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.gql.client.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.gql.client.%s not public", name)
		}
	}
	for _, name := range []string{
		"Subscription",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.gql.client missing type %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.gql.client.%s not public", name)
		}
	}
}

func TestGqlClientModuleSourcePinsNonExecutingBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["gql_client"]
	if mod == nil {
		t.Fatal("std.gql.client module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub fn execute(endpoint: String, request: graphql.Request) -> Result<http.Response, Error>`,
		`pub fn executeWithHeaders(endpoint: String, request: graphql.Request, headers: http.Headers) -> Result<http.Response, Error>`,
		`pub fn subscribe(endpoint: String, queryDoc: String) -> Result<Subscription, Error>`,
		`pub fn nextEvent(sub: Subscription) -> Result<String?, Error>`,
		`pub fn parseData(responseBody: String) -> Result<String, Error>`,
		`Subscription`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.gql.client source missing %q", want)
		}
	}
}
