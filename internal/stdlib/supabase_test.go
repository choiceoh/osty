package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestSupabaseModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := requireSupabaseModule(t, reg)

	for _, tc := range []struct {
		name string
		kind resolve.SymbolKind
	}{
		{"CountMode", resolve.SymEnum},
		{"ReturningMode", resolve.SymEnum},
		{"ResolutionMode", resolve.SymEnum},
		{"OrderDirection", resolve.SymEnum},
		{"NullOrdering", resolve.SymEnum},
		{"Prefer", resolve.SymStruct},
		{"Client", resolve.SymStruct},
		{"TableQuery", resolve.SymStruct},
		{"ApiError", resolve.SymStruct},
	} {
		sym := mod.Package.PkgScope.LookupLocal(tc.name)
		if sym == nil {
			t.Fatalf("std.supabase missing type %q", tc.name)
		}
		if sym.Kind != tc.kind {
			t.Fatalf("std.supabase.%s kind = %s, want %s", tc.name, sym.Kind, tc.kind)
		}
		if !sym.Pub {
			t.Fatalf("std.supabase.%s not public", tc.name)
		}
	}

	for _, name := range []string{
		"prefer", "preferRepresentation", "preferMinimal",
		"client", "project", "local", "from",
		"headers", "authHeaders", "storageHeaders",
		"restUrl", "tableUrl", "rpcUrl", "authUrl", "storageUrl", "functionsUrl", "graphqlUrl",
		"selectHttpRequest", "insertHttpRequest", "insertWithPreferHttpRequest",
		"upsertHttpRequest", "upsertWithPreferHttpRequest", "updateHttpRequest", "deleteHttpRequest",
		"rpcHttpRequest", "rpcWithPreferHttpRequest", "rpcValueHttpRequest", "rpcValueWithPreferHttpRequest",
		"signUpHttpRequest", "passwordTokenHttpRequest", "refreshTokenHttpRequest", "logoutHttpRequest",
		"publicObjectUrl", "authenticatedObjectUrl", "objectUrl",
		"authenticatedObjectHttpRequest", "downloadObjectHttpRequest", "uploadObjectHttpRequest",
		"updateObjectHttpRequest", "removeObjectHttpRequest",
		"invokeFunctionHttpRequest", "graphqlHttpRequest",
		"sendSelect", "sendInsert", "sendInsertWithPrefer", "sendUpdate", "sendDelete", "sendRpc", "sendRpcWithPrefer",
		"parseApiError", "requireSuccess",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.supabase missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.supabase.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.supabase.%s not public", name)
		}
	}
}

func TestSupabaseModuleMethodsAreBodied(t *testing.T) {
	reg := LoadCached()
	for _, tc := range []struct {
		typeName string
		methods  []string
	}{
		{"CountMode", []string{"toPrefer"}},
		{"ReturningMode", []string{"toPrefer"}},
		{"ResolutionMode", []string{"toPrefer"}},
		{"OrderDirection", []string{"toString"}},
		{"NullOrdering", []string{"toOrderSuffix"}},
		{"Prefer", []string{"withCount", "withReturning", "withResolution", "withMissingDefault", "headerValue"}},
		{"Client", []string{"withAccessToken", "withServiceRoleKey", "withSchema", "withClientInfo", "withHeader", "resolvedBearer"}},
		{"TableQuery", []string{
			"select", "withParam", "filter", "eq", "neq", "gt", "gte", "lt", "lte",
			"like", "ilike", "isNull", "inList", "contains", "containedBy", "overlaps",
			"textSearch", "order", "orderAsc", "orderDesc", "limit", "offset", "range", "withCount", "withPrefer",
			"returning", "resolveDuplicates", "single", "csv",
		}},
		{"ApiError", []string{"summary"}},
	} {
		for _, method := range tc.methods {
			fn := reg.LookupMethodDecl("supabase", tc.typeName, method)
			if fn == nil {
				t.Fatalf("LookupMethodDecl(supabase, %s, %s) = nil, want *ast.FnDecl", tc.typeName, method)
			}
			if fn.Body == nil {
				t.Fatalf("supabase.%s.%s body = nil, want source method body", tc.typeName, method)
			}
		}
	}
}

func TestSupabaseModulePinsEndpointAndHeaderBehavior(t *testing.T) {
	reg := LoadCached()
	mod := requireSupabaseModule(t, reg)
	src := string(mod.Source)
	for _, want := range []string{
		`joinUrl(client.baseUrl, "/rest/v1")`,
		`joinUrl(client.baseUrl, "/auth/v1/{cleanRelativePath(path)?}")`,
		`joinUrl(client.baseUrl, "/storage/v1/{cleanRelativePath(path)?}")`,
		`joinUrl(client.baseUrl, "/functions/v1/{encodePathSegment(cleanName(\"function\", functionName)?)}")`,
		`joinUrl(client.baseUrl, "/graphql/v1")`,
		`out.insert("apikey", strings.trimSpace(client.apiKey))`,
		`out.insert("Authorization", "Bearer {strings.trimSpace(bearer)}")`,
		`out.insert("Accept-Profile", strings.trimSpace(client.schema))`,
		`out.insert("Content-Profile", strings.trimSpace(client.schema))`,
		`self.filter(column, "eq", value)`,
		`self.filter(column, "fts", query)`,
		`self.params = setParam(self.params, "offset", from.toString())`,
		`"application/vnd.pgrst.object+json"`,
		`"{authUrl(client, \"token\")?}?grant_type=password"`,
		`"{authUrl(client, \"token\")?}?grant_type=refresh_token"`,
		`object/authenticated/{encodePathSegment(cleanName(\"bucket\", bucket)?)}"`,
		`"/{encodeObjectPath(path)?}"`,
		`h.insert("x-upsert", "true")`,
		`prop("prefixes", array(items))`,
		`normalizedJsonObject(bodyJson)`,
		`parseApiError(response).summary()`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.supabase source missing %q", want)
		}
	}
}

func requireSupabaseModule(t *testing.T, reg *Registry) *Module {
	t.Helper()
	mod := reg.Modules["supabase"]
	if mod == nil || mod.Package == nil || mod.Package.PkgScope == nil {
		var details []string
		for _, d := range reg.Diags {
			if d != nil && (strings.Contains(d.File, "supabase") || strings.Contains(d.Message, "native resolve")) {
				details = append(details, d.Error())
			}
		}
		t.Fatalf("std.supabase not fully loaded; diagnostics=%v", details)
	}
	return mod
}
