package check

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
)

// Pin LANG_SPEC §4 / appendix A.6 semantics for `?.` field access:
//
//   - `opt?.field` on `T?` types the expression as `U?` (where U is
//     the field type), short-circuiting to `None` on a `None` receiver.
//   - `opt?.field?.inner` flattens — no `Option<Option<U>>`.
//   - `opt?.field ?? default` unwraps to the field type.
//   - `non_opt?.field` is rejected with E0719 (CodeOptionalChainOnNon).
//
// Before the elabInferField fix these fixtures all failed with E0702
// ("no field on type `T?`") because the checker ignored the parser's
// `AstNField.flags == 1` marker. The tests drive the self-host
// CheckSourceStructured path (the default since phase 1c.1), so they
// guard the Osty elab walker itself — not a Go-side fallback.

// countByCode is defined in rawptr_test.go; reused verbatim here.

func checkOptionalChainSource(t *testing.T, src string) []*diag.Diagnostic {
	t.Helper()
	result := selfhost.CheckSourceStructured([]byte(src))
	return selfhost.CheckDiagnosticsAsDiag([]byte(src), result.Diagnostics)
}

func TestOptionalChainFieldAccessOnOptionStruct(t *testing.T) {
	src := `struct User {
    pub name: String,
}

fn main() {
    let u: User? = Some(User { name: "a" })
    let n: String? = u?.name
}
`
	diags := checkOptionalChainSource(t, src)
	if n := countByCode(diags, "E0702"); n != 0 {
		t.Fatalf("E0702 must not fire on legal ?. access, got %d:\n%s", n, renderDiagList(diags))
	}
	if n := countByCode(diags, "E0700"); n != 0 {
		t.Fatalf("E0700 type-mismatch must not fire when annotation matches %q, got %d:\n%s",
			"String?", n, renderDiagList(diags))
	}
	if n := countByCode(diags, "E0719"); n != 0 {
		t.Fatalf("E0719 must not fire on Option receiver, got %d:\n%s", n, renderDiagList(diags))
	}
}

func TestOptionalChainNestedFlattens(t *testing.T) {
	src := `struct Addr {
    pub city: String,
}

struct User {
    pub addr: Addr?,
}

fn main() {
    let u: User? = Some(User { addr: Some(Addr { city: "seoul" }) })
    let c: String? = u?.addr?.city
}
`
	diags := checkOptionalChainSource(t, src)
	for _, code := range []string{"E0700", "E0702", "E0719"} {
		if n := countByCode(diags, code); n != 0 {
			t.Fatalf("%s must not fire on nested ?. chain, got %d:\n%s",
				code, n, renderDiagList(diags))
		}
	}
}

func TestOptionalChainCoalesceUnwraps(t *testing.T) {
	src := `struct User {
    pub name: String,
}

fn main() {
    let u: User? = None
    let n: String = u?.name ?? "anonymous"
}
`
	diags := checkOptionalChainSource(t, src)
	for _, code := range []string{"E0700", "E0702", "E0717", "E0719"} {
		if n := countByCode(diags, code); n != 0 {
			t.Fatalf("%s must not fire on `opt?.field ?? default` coalesce, got %d:\n%s",
				code, n, renderDiagList(diags))
		}
	}
}

func TestOptionalChainNonOptionReceiverRejected(t *testing.T) {
	src := `struct User {
    pub name: String,
}

fn main() {
    let u: User = User { name: "a" }
    let bad = u?.name
}
`
	diags := checkOptionalChainSource(t, src)
	if n := countByCode(diags, "E0719"); n != 1 {
		t.Fatalf("E0719 must fire exactly once on non-Option receiver, got %d:\n%s",
			n, renderDiagList(diags))
	}
	// The legacy symptom — E0702 on `User?` — must be gone; the gate
	// short-circuits before the field lookup runs.
	if n := countByCode(diags, "E0702"); n != 0 {
		t.Fatalf("E0702 must not fire alongside E0719 (gate short-circuits), got %d:\n%s",
			n, renderDiagList(diags))
	}
}

func TestOptionalChainE0719MessageNamesReceiverType(t *testing.T) {
	src := `fn main() {
    let n: Int = 5
    let bad = n?.absolute
}
`
	diags := checkOptionalChainSource(t, src)
	for _, d := range diags {
		if d == nil || d.Code != "E0719" {
			continue
		}
		if !strings.Contains(d.Message, "Int") {
			t.Fatalf("E0719 message must name receiver type `Int`, got %q", d.Message)
		}
		return
	}
	t.Fatalf("expected E0719 diagnostic, got:\n%s", renderDiagList(diags))
}
