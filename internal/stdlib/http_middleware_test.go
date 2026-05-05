package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestHttpMiddlewareModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["http_middleware"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.http.middleware not loaded")
	}
	for _, name := range []string{
		"defaultRetryPolicy", "retry",
		"newCircuitBreaker", "defaultCircuitBreaker", "circuitBreaker",
		"newTokenBucket", "defaultTokenBucket", "rateLimit",
		"chain",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.http.middleware missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.http.middleware.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.http.middleware.%s not public", name)
		}
	}
	for _, name := range []string{
		"RetryPolicy", "CircuitState", "CircuitBreaker", "TokenBucket",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.http.middleware missing type %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.http.middleware.%s not public", name)
		}
	}
}

func TestHttpMiddlewareModuleSourcePinsNonExecutingBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["http_middleware"]
	if mod == nil {
		t.Fatal("std.http.middleware module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`RetryPolicy`,
		`maxAttempts: 3`,
		`baseDelayMs: 200`,
		`CircuitBreaker`,
		`newCircuitBreaker(5, 30_000)`,
		`TokenBucket`,
		`newTokenBucket(100, 10, 1_000)`,
		`pub fn circuitBreaker(cb: CircuitBreaker, next: http.Handler) -> http.Handler`,
		`pub fn rateLimit(bucket: TokenBucket, next: http.Handler) -> http.Handler`,
		`pub fn chain(middlewares: List<fn(http.Handler) -> http.Handler>, finalHandler: http.Handler) -> http.Handler`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.http.middleware source missing %q", want)
		}
	}
}
