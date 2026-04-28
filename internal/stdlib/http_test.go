package stdlib

import (
	"strings"
	"testing"
)

func TestHttpPrimitiveAndWrapperSurface(t *testing.T) {
	reg := Load()

	for _, name := range []string{"request", "get", "serve"} {
		fn := reg.LookupFnDecl("http", name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(http, %s) = nil, want *ast.FnDecl", name)
		}
		if fn.Body != nil {
			t.Fatalf("http.%s has a source body, want declaration-only runtime primitive", name)
		}
	}

	for _, name := range []string{
		"parseMethod", "parseSameSite", "parseMediaType", "statusText",
		"allMethods", "newRequest", "newRouter", "dispatch", "cookie",
		"parseQuery", "formatQuery", "parseForm", "formatForm", "parseSetCookie",
		"send", "head", "post", "put", "patch", "delete", "options", "trace", "connect",
		"sendText", "sendJson", "sendForm",
		"postText", "putText", "patchText",
		"postJson", "putJson", "patchJson",
		"postForm", "putForm", "patchForm",
		"response", "textResponse", "jsonResponse", "problem",
		"ok", "okText", "okJson",
		"created", "createdAt", "accepted", "noContent",
		"badRequest", "unauthorized", "forbidden", "notFound", "methodNotAllowed",
		"conflict", "gone", "unprocessableContent", "tooManyRequests",
		"internalServerError", "serviceUnavailable",
		"redirect", "movedPermanently", "found", "seeOther", "temporaryRedirect", "permanentRedirect",
	} {
		fn := reg.LookupFnDecl("http", name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(http, %s) = nil, want *ast.FnDecl", name)
		}
		if fn.Body == nil {
			t.Fatalf("http.%s body = nil, want source wrapper body", name)
		}
	}
}

func TestHttpRequestAndResponseMethodsAreBodied(t *testing.T) {
	reg := LoadCached()

	for _, tc := range []struct {
		typeName string
		methods  []string
	}{
		{
			typeName: "MediaType",
			methods:  []string{"toString", "parameter", "is"},
		},
		{
			typeName: "BasicAuth",
			methods:  []string{"toHeaderValue"},
		},
		{
			typeName: "Cookie",
			methods:  []string{"toString"},
		},
		{
			typeName: "Problem",
			methods:  []string{"toResponse"},
		},
		{
			typeName: "Request",
			methods: []string{
				"header", "headerOr", "hasHeader",
				"contentType", "mediaType", "contentTypeIs", "accepts", "contentLength",
				"path", "queryString", "fragment", "query", "queryValue", "queryValues",
				"text", "json", "form", "cookies", "cookie", "bearerToken", "basicAuth",
				"withMethod", "withHeader", "withoutHeader", "withHeaders", "withBody",
				"withContentType", "withText", "withJson", "withForm",
				"withQueryValue", "withQuery",
				"withCookie", "withCookies", "withBearerToken", "withBasicAuth",
				"withAccept", "withUserAgent",
			},
		},
		{
			typeName: "Response",
			methods: []string{
				"header", "headerOr", "hasHeader",
				"contentType", "mediaType", "contentTypeIs", "isJson", "contentLength", "statusText",
				"isInformational", "isSuccess", "isRedirect",
				"isClientError", "isServerError", "isError",
				"location", "etag", "setCookie",
				"requireSuccess", "requireStatus", "text", "json",
				"withStatus", "withHeader", "withoutHeader", "withHeaders", "withBody",
				"withContentType", "withText", "withJson", "withCookie", "withLocation", "withEtag",
			},
		},
		{
			typeName: "Method",
			methods:  []string{"toString", "isSafe", "isIdempotent", "allowsRequestBody"},
		},
		{
			typeName: "SameSite",
			methods:  []string{"toString"},
		},
		{
			typeName: "Router",
			methods: []string{
				"route", "handle", "handleAny",
				"get", "post", "put", "patch", "delete", "head", "options", "trace", "connect",
				"find",
			},
		},
	} {
		for _, method := range tc.methods {
			fn := reg.LookupMethodDecl("http", tc.typeName, method)
			if fn == nil {
				t.Fatalf("LookupMethodDecl(http, %s, %s) = nil, want *ast.FnDecl", tc.typeName, method)
			}
			if fn.Body == nil {
				t.Fatalf("http.%s.%s body = nil, want source method body", tc.typeName, method)
			}
		}
	}
}

func TestHttpModuleSourcePinsQualityGuards(t *testing.T) {
	reg := Load()
	mod := reg.Modules["http"]
	if mod == nil {
		t.Fatal("std.http module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub type QueryValues = Map<String, List<String>>`,
		`pub struct Cookie {`,
		`pub struct Router {`,
		`pub struct Problem {`,
		`encoding.base64.encode(bytes.fromString("{self.username}:{self.password}"))`,
		`parseQuery(self.queryString() ?? "")`,
		`parseForm(self.text()?)`,
		`parseCookieJar(header)`,
		`parseSetCookie(value)?`,
		`matchRoutePattern(route.pattern, path)`,
		`response(status, b"", withHeaderValue(headers, "Location", location))`,
		`jsonResponse(self.status, self, withHeaderValue(headers, "Content-Type", "application/problem+json"))`,
		"strings.toUpper(strings.trimSpace(text))",
		`if !self.isSuccess() {`,
		`statusText(self.status)`,
		`withDefaultHeader(headers, "Content-Type", "application/x-www-form-urlencoded")`,
		`withDefaultHeader(self.headers, "Content-Type", "application/json")`,
		`withDefaultHeader(self.headers, "Content-Type", "text/plain; charset=utf-8")`,
		`process.abort("http: header name must not be empty")`,
		`canonicalHeaderName(key) == canonical`,
		`bytes.fromString(json.encode(value))`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.http source missing %q", want)
		}
	}
}
