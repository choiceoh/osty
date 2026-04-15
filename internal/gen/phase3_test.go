package gen_test

import (
	"strings"
	"testing"
)

// TestClosureInferredParam verifies closures with a body expression
// (no explicit annotation) compile and run.
func TestClosureInferredParam(t *testing.T) {
	src := `fn apply(f: fn(Int) -> Int, x: Int) -> Int {
    f(x)
}

fn main() {
    let double = |x: Int| -> Int { x * 2 }
    println("{apply(double, 5)}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "10" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestClosureBlockBody verifies a closure whose body is a block.
func TestClosureBlockBody(t *testing.T) {
	src := `fn main() {
    let square_plus_one = |x: Int| -> Int {
        let y = x * x
        y + 1
    }
    println("{square_plus_one(5)}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "26" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestGenericFunction verifies a generic identity function compiles
// with multiple instantiations.
func TestGenericFunction(t *testing.T) {
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
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "42 hello" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestGenericTypeAlias verifies generic aliases emit real Go aliases,
// not TODO comments, and remain usable in annotations.
func TestGenericTypeAlias(t *testing.T) {
	src := `type Pair<T> = (T, T)

fn main() {
    let p: Pair<Int> = (3, 4)
    println("{p.F0} {p.F1}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := string(goSrc)
	if strings.Contains(out, "TODO(phase3): generic type alias") {
		t.Errorf("generic alias TODO leaked into output:\n%s", out)
	}
	if !strings.Contains(out, "type Pair[T any] =") {
		t.Errorf("expected generic type alias in output:\n%s", out)
	}
	got := runGo(t, goSrc)
	if strings.TrimSpace(got) != "3 4" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", got, goSrc)
	}
}

// TestMapIteration verifies `for (k, v) in m` pattern.
func TestMapIteration(t *testing.T) {
	src := `fn main() {
    let m = {"a": 1, "b": 2, "c": 3}
    let mut total = 0
    for (k, v) in m {
        total = total + v
    }
    println("{total}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "6" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestUnitClosure_VoidBody verifies a closure whose inferred return
// is unit emits as `func()` rather than the legacy `func() int {
// return … }` shape — the latter would refuse to compile when the
// body is a void method call (e.g. assertion helpers).
//
// Regression for the bug found while wiring `osty test` to actually
// execute discovered tests: `testing.context("…", || { … })` fed gen
// a closure whose body's checker type degraded to ErrorType, causing
// the default `int` return path to fire and emit `return
// testing.assertEq(…)` — invalid Go because assertEq returns void.
func TestUnitClosure_VoidBody(t *testing.T) {
	src := `fn run(f: fn() -> ()) {
    f()
}

fn main() {
    let mut calls = 0
    run(|| {
        calls = calls + 1
    })
    run(|| {
        calls = calls + 10
    })
    println("{calls}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	if strings.Contains(string(goSrc), "func() int {") {
		t.Errorf("unit closure was emitted with int return:\n%s", goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "11" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestLetWildcardDiscard verifies `let _ = expr` lowers to plain
// blank-identifier assignment (`_ = expr`), not the invalid Go
// `_ := expr`. Regression for a gen bug surfaced by benchmark
// bodies of the form `let _ = add(1, 2)`.
func TestLetWildcardDiscard(t *testing.T) {
	src := `fn side() -> Int {
    42
}

fn main() {
    let _ = side()
    println("ok")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	if strings.Contains(string(goSrc), "_ :=") {
		t.Errorf("`_ :=` emitted (invalid Go):\n%s", goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "ok" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestHigherOrderFn verifies passing a closure through a higher-order
// function type-checks and runs.
func TestHigherOrderFn(t *testing.T) {
	src := `fn twice(f: fn(Int) -> Int, x: Int) -> Int {
    f(f(x))
}

fn main() {
    let inc = |n: Int| -> Int { n + 1 }
    println("{twice(inc, 5)}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "7" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestNestedLists verifies `List<List<Int>>` transpiles to `[][]int`.
func TestNestedLists(t *testing.T) {
	src := `fn main() {
    let grid = [[1, 2], [3, 4], [5, 6]]
    let mut total = 0
    for row in grid {
        for x in row {
            total = total + x
        }
    }
    println("{total}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "21" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestListPush verifies the checker-approved List.push stdlib surface lowers
// to append-backed mutation instead of a non-existent Go method call.
func TestListPush(t *testing.T) {
	src := `fn main() {
    let mut xs: List<Int> = []
    xs.push(10)
    xs.push(32)
    let mut total = 0
    for x in xs {
        total = total + x
    }
    println("{total}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "42" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestMatchOnList verifies matching on literal primitives inside a
// function that processes a list.
func TestMatchOnList(t *testing.T) {
	src := `fn classify(n: Int) -> String {
    match n {
        0 -> "zero",
        1 | 2 | 3 -> "small",
        _ -> "big",
    }
}

fn main() {
    let xs = [0, 1, 2, 5, 10]
    for x in xs {
        println(classify(x))
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "zero\nsmall\nsmall\nbig\nbig\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestFibRecursive verifies a more complex recursive function with
// multiple returns.
func TestFibRecursive(t *testing.T) {
	src := `fn fib(n: Int) -> Int {
    if n < 2 {
        return n
    }
    fib(n - 1) + fib(n - 2)
}

fn main() {
    println("{fib(10)}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "55" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}
