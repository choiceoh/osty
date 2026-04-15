package gen_test

import (
	"strings"
	"testing"
)

// TestMonomorph_SpecializedCopies verifies that a generic fn with
// instantiations at two distinct types emits two separate specialized
// functions — no Go-generic `[T any]` header — and that each call
// site resolves to its mangled specialization. This is the §2.7.3
// contract: "one specialized copy per distinct instantiation".
func TestMonomorph_SpecializedCopies(t *testing.T) {
	src := `fn id<T>(x: T) -> T {
    x
}

fn main() {
    let a = id(42)
    let b = id("hello")
    println("{a} {b}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := string(goSrc)

	// The Go-generic shape (`func id[T any](x T) T`) must not appear —
	// monomorphization replaces it with concrete specializations.
	if strings.Contains(out, "func id[") || strings.Contains(out, "func id(x T)") {
		t.Errorf("generic header still present in output (expected monomorphized):\n%s", out)
	}
	// Both specializations must be emitted.
	if !strings.Contains(out, "func id_int(") {
		t.Errorf("expected specialized `id_int` in output:\n%s", out)
	}
	if !strings.Contains(out, "func id_string(") {
		t.Errorf("expected specialized `id_string` in output:\n%s", out)
	}
	// Call sites must reference the mangled names.
	if !strings.Contains(out, "id_int(42)") {
		t.Errorf("expected `id_int(42)` call site:\n%s", out)
	}
	if !strings.Contains(out, "id_string(\"hello\")") {
		t.Errorf("expected `id_string(\"hello\")` call site:\n%s", out)
	}
	// And the program must still run correctly.
	if got := strings.TrimSpace(runGo(t, goSrc)); got != "42 hello" {
		t.Errorf("runtime output = %q; want %q", got, "42 hello")
	}
}

// TestMonomorph_UnusedGenericOmitted verifies a generic function with
// no instantiations is dropped entirely from the output — consistent
// with §2.7.3's "demand-driven" lowering.
func TestMonomorph_UnusedGenericOmitted(t *testing.T) {
	src := `fn unused<T>(x: T) -> T {
    x
}

fn main() {
    println("hi")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := string(goSrc)
	if strings.Contains(out, "unused") {
		t.Errorf("unused generic fn should be omitted, got:\n%s", out)
	}
}

// TestMonomorph_DuplicateInstantiationCoalesced verifies two call
// sites at the same type share one specialized copy (keyed on the
// Go-type tuple, not the AST node).
func TestMonomorph_DuplicateInstantiationCoalesced(t *testing.T) {
	src := `fn id<T>(x: T) -> T {
    x
}

fn main() {
    let a = id(1)
    let b = id(2)
    println("{a} {b}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := string(goSrc)
	if n := strings.Count(out, "func id_int("); n != 1 {
		t.Errorf("expected exactly one `func id_int(` definition, got %d:\n%s", n, out)
	}
	if got := strings.TrimSpace(runGo(t, goSrc)); got != "1 2" {
		t.Errorf("runtime output = %q; want %q", got, "1 2")
	}
}
