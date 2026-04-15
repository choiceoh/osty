package gen_test

import (
	"strings"
	"testing"
)

// TestMonomorph_TransitiveChain drives a three-level generic call
// chain (outer<T> → middle<U> → inner<V>) with the concrete type
// only pinned at the outermost boundary. Every intermediate
// specialization must pick up the concrete type as the outer
// monomorph substitutes downward.
func TestMonomorph_TransitiveChain(t *testing.T) {
	src := `fn inner<V>(x: V) -> V { x }
fn middle<U>(x: U) -> U { inner(x) }
fn outer<T>(x: T) -> T { middle(x) }

fn main() {
    let a: Int = outer(7)
    println("{a}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := string(goSrc)
	for _, want := range []string{"func outer_int(", "func middle_int(", "func inner_int("} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if got := strings.TrimSpace(runGo(t, goSrc)); got != "7" {
		t.Errorf("runtime output = %q; want %q", got, "7")
	}
}

// TestMonomorph_MultiTypeParam exercises a fn with two type
// parameters, verifying the mangled name combines both and a single
// call site produces a single specialization.
func TestMonomorph_MultiTypeParam(t *testing.T) {
	src := `fn pairFirst<A, B>(a: A, b: B) -> A { a }

fn main() {
    let x = pairFirst(1, "two")
    println("{x}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := string(goSrc)
	if !strings.Contains(out, "func pairFirst_int_string(") {
		t.Errorf("expected `pairFirst_int_string` specialization:\n%s", out)
	}
	if got := strings.TrimSpace(runGo(t, goSrc)); got != "1" {
		t.Errorf("runtime output = %q; want %q", got, "1")
	}
}

// TestMonomorph_NestedGeneric stresses transitive instantiation: a
// generic fn whose body calls another generic fn with the enclosing
// type-parameter as argument. A complete monomorphizer must propagate
// the outer instantiation inward and emit `id_int` (not `id_U`) for
// the inner call when `wrap<U=Int>` is requested.
func TestMonomorph_NestedGeneric(t *testing.T) {
	src := `fn id<T>(x: T) -> T { x }
fn wrap<U>(y: U) -> U { id(y) }

fn main() {
    let a: Int = wrap(5)
    println("{a}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := string(goSrc)
	if strings.Contains(out, "id_U(") || strings.Contains(out, "func id_U(") {
		t.Errorf("id specialized on a type variable U (should be int):\n%s", out)
	}
	if !strings.Contains(out, "func id_int(") {
		t.Errorf("expected transitive `id_int` specialization:\n%s", out)
	}
	if got := strings.TrimSpace(runGo(t, goSrc)); got != "5" {
		t.Errorf("runtime output = %q; want %q", got, "5")
	}
}
