package gen_test

import (
	"strings"
	"testing"
)

// TestMonomorph_RecursiveGeneric covers a generic fn that calls
// itself. The self-call's instantiation args record [T] — under the
// current monomorph's substEnv this must resolve to the same
// specialization already being emitted (the worklist entry is seeded
// before its body is visited, so requestInstance dedupes to the
// in-flight mangled name rather than re-enqueueing).
func TestMonomorph_RecursiveGeneric(t *testing.T) {
	src := `fn countDown<T>(x: Int, seed: T) -> T {
    if x <= 0 {
        return seed
    }
    countDown(x - 1, seed)
}

fn main() {
    let a = countDown(3, 42)
    println("{a}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := string(goSrc)
	// Exactly one specialization, not two.
	if n := strings.Count(out, "func countDown_int("); n != 1 {
		t.Errorf("expected exactly 1 `countDown_int` definition, got %d:\n%s", n, out)
	}
	// The recursive call must land on the same mangled name.
	if strings.Count(out, "countDown_int(") < 2 {
		t.Errorf("expected recursive call site to also use mangled name:\n%s", out)
	}
	if got := strings.TrimSpace(runGo(t, goSrc)); got != "42" {
		t.Errorf("runtime output = %q; want %q", got, "42")
	}
}

// TestMonomorph_MutualRecursion covers a pair of generic fns that
// call each other. Both halves of the cycle must reach a fixed point
// — requestInstance must handle the second fn's entry being added
// while the first is still mid-emission.
func TestMonomorph_MutualRecursion(t *testing.T) {
	src := `fn pingGeneric<T>(n: Int, tag: T) -> T {
    if n <= 0 { return tag }
    pongGeneric(n - 1, tag)
}
fn pongGeneric<T>(n: Int, tag: T) -> T {
    if n <= 0 { return tag }
    pingGeneric(n - 1, tag)
}

fn main() {
    let a = pingGeneric(3, "hi")
    println("{a}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := string(goSrc)
	for _, want := range []string{"func pingGeneric_string(", "func pongGeneric_string("} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
	if got := strings.TrimSpace(runGo(t, goSrc)); got != "hi" {
		t.Errorf("runtime output = %q; want %q", got, "hi")
	}
}

// TestMonomorph_GenericInMethodBody covers a non-generic struct
// method whose body happens to call a generic fn. The enclosing
// context has no substEnv, but the checker still records the
// instantiation on the inner call — which must materialize as a
// normal root specialization.
func TestMonomorph_GenericInMethodBody(t *testing.T) {
	src := `fn id<T>(x: T) -> T { x }

struct Holder {
    value: Int,

    fn doubled(self) -> Int {
        id(self.value) * 2
    }
}

fn main() {
    let h = Holder { value: 21 }
    println("{h.doubled()}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := string(goSrc)
	if !strings.Contains(out, "func id_int(") {
		t.Errorf("expected `id_int` specialization reached through method body:\n%s", out)
	}
	if strings.Contains(out, "id(self") && !strings.Contains(out, "id_int(self") {
		t.Errorf("call site in method wasn't rewritten:\n%s", out)
	}
	if got := strings.TrimSpace(runGo(t, goSrc)); got != "42" {
		t.Errorf("runtime output = %q; want %q", got, "42")
	}
}

// TestMonomorph_Turbofish covers an explicit type-argument call
// `f::<Int>(x)`. The checker takes the explicit path which populates
// Instantiations identically; the calleeSymbol resolver must
// unwrap the TurbofishExpr.
func TestMonomorph_Turbofish(t *testing.T) {
	src := `fn id<T>(x: T) -> T { x }

fn main() {
    let a = id::<Int>(99)
    println("{a}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := string(goSrc)
	if !strings.Contains(out, "func id_int(") {
		t.Errorf("turbofish call didn't produce `id_int` specialization:\n%s", out)
	}
	if !strings.Contains(out, "id_int(99)") {
		t.Errorf("turbofish call site not rewritten:\n%s", out)
	}
	if got := strings.TrimSpace(runGo(t, goSrc)); got != "99" {
		t.Errorf("runtime output = %q; want %q", got, "99")
	}
}

// TestMonomorph_FnParamInGeneric covers a generic fn whose parameter
// list itself mentions the type variable inside a function type —
// `fn apply<T>(f: fn(T) -> T, x: T) -> T`. Every occurrence of T in
// the nested fn-type must be substituted when emitting the body.
func TestMonomorph_FnParamInGeneric(t *testing.T) {
	src := `fn apply<T>(f: fn(T) -> T, x: T) -> T {
    f(x)
}

fn main() {
    let inc = |n: Int| -> Int { n + 1 }
    let a = apply(inc, 41)
    println("{a}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := string(goSrc)
	if !strings.Contains(out, "func apply_int(") {
		t.Errorf("expected `apply_int` specialization:\n%s", out)
	}
	// The closure-parameter type inside the fn signature must itself
	// be `func(int) int`, not `func(T) T`.
	if !strings.Contains(out, "func(int) int") {
		t.Errorf("expected substituted closure param type `func(int) int`:\n%s", out)
	}
	if got := strings.TrimSpace(runGo(t, goSrc)); got != "42" {
		t.Errorf("runtime output = %q; want %q", got, "42")
	}
}

// TestMonomorph_OptionalReturn verifies an Optional type-var return
// (`T?`) substitutes into the correct Go shape (`*int`). Optional
// is pure sugar in Osty but it flows through a separate AST node
// from Named, so the substEnv lookup must kick in through the nested
// `Optional.Inner` path too.
func TestMonomorph_OptionalReturn(t *testing.T) {
	src := `fn wrapOpt<T>(x: T) -> T? {
    Some(x)
}

fn main() {
    match wrapOpt(7) {
        Some(x) -> println("{x}"),
        None -> println("empty"),
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := string(goSrc)
	if !strings.Contains(out, "func wrapOpt_int(") {
		t.Errorf("expected `wrapOpt_int` specialization:\n%s", out)
	}
	// Return type must be substituted to *int (Optional → pointer).
	if !strings.Contains(out, ") *int {") {
		t.Errorf("expected substituted return `*int`:\n%s", out)
	}
	if got := strings.TrimSpace(runGo(t, goSrc)); got != "7" {
		t.Errorf("runtime output = %q; want %q", got, "7")
	}
}

// TestMonomorph_ListTypeArg confirms structured type args (here
// `List<Int>` → Go `[]int`) survive the mangling / substitution path.
// Brackets are sanitized to `Of` so the mangled name is still a
// legal Go identifier.
func TestMonomorph_ListTypeArg(t *testing.T) {
	src := `fn first<T>(xs: List<T>) -> T {
    xs[0]
}

fn main() {
    let xs: List<Int> = [10, 20, 30]
    let a = first(xs)
    println("{a}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := string(goSrc)
	if !strings.Contains(out, "func first_int(") {
		t.Errorf("expected `first_int` specialization for List<Int>:\n%s", out)
	}
	// The parameter's Osty List<T> must have been rendered with T
	// substituted, i.e. `[]int` in the signature.
	if !strings.Contains(out, "xs []int") {
		t.Errorf("expected substituted param `xs []int`:\n%s", out)
	}
	if got := strings.TrimSpace(runGo(t, goSrc)); got != "10" {
		t.Errorf("runtime output = %q; want %q", got, "10")
	}
}
