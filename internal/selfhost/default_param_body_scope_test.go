package selfhost

import (
	"strings"
	"testing"
)

// TestDefaultArgParamBindsToBodyScope pins the SPEC_GAPS
// `default-arg-resolve` fix: a parameter declared with a default
// value (`fn fetch(url: String, timeout: Int = 30) -> Int { ... }`)
// must be visible by its source name inside the function body. The
// checker prefixes `paramNames` entries with `?` for arity-tracking
// (see paramDefaultCount), so historically the body-scope binder
// registered `?timeout` and a body reference to `timeout` fired
// E0745 with a leaking `did you mean ?timeout?` hint.
func TestDefaultArgParamBindsToBodyScope(t *testing.T) {
	src := `fn fetch(url: String, timeout: Int = 30) -> Int {
    timeout + 1
}

fn main() {
    println(fetch("hi"))
}
`
	file := astParse(src)
	cx := newElabCx(file, nil)
	elabFile(cx)
	for _, d := range cx.env.local.diagnostics {
		if d.code == "E0745" {
			t.Fatalf("default-arg param should not trigger E0745: %s", d.message)
		}
		if strings.Contains(d.message, "?timeout") {
			t.Fatalf("internal `?` prefix leaked into diagnostic: %s", d.message)
		}
	}
}
