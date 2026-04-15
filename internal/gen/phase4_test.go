package gen_test

import (
	"strings"
	"testing"
)

// TestOptionSomeNone verifies Some/None + `??` coalescing.
func TestOptionSomeNone(t *testing.T) {
	src := `fn main() {
    let a: Int? = Some(42)
    let b: Int? = None
    let x = a ?? 0
    let y = b ?? 99
    println("{x} {y}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "42 99" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestOptionMatch verifies match arms over Option<T>.
func TestOptionMatch(t *testing.T) {
	src := `fn describe(n: Int?) -> String {
    match n {
        Some(v) -> "some({v})",
        None -> "none",
    }
}

fn main() {
    println(describe(Some(7)))
    println(describe(None))
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "some(7)\nnone\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestOptionOfStruct verifies wrapping a struct in Option.
func TestOptionOfStruct(t *testing.T) {
	src := `struct User {
    name: String,
}

fn main() {
    let maybe: User? = Some(User { name: "alice" })
    let nothing: User? = None
    match maybe {
        Some(u) -> println("hi {u.name}"),
        None -> println("no user"),
    }
    match nothing {
        Some(u) -> println("hi {u.name}"),
        None -> println("no user"),
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "hi alice\nno user\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestOptionGuard verifies guard arms on an Option match.
func TestOptionGuard(t *testing.T) {
	src := `fn classify(n: Int?) -> String {
    match n {
        Some(v) if v > 0 -> "pos",
        Some(v) if v < 0 -> "neg",
        Some(_) -> "zero",
        None -> "missing",
    }
}

fn main() {
    println(classify(Some(5)))
    println(classify(Some(-3)))
    println(classify(Some(0)))
    println(classify(None))
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "pos\nneg\nzero\nmissing\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestDefer verifies `defer expr` runs on function exit.
func TestDefer(t *testing.T) {
	src := `fn main() {
    defer println("end")
    println("start")
    println("middle")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "start\nmiddle\nend\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestQuestionOp verifies that `expr?` inside an Option-returning
// function propagates None and unwraps Some, when used at the
// supported statement-position `let x = expr?`.
func TestQuestionOp(t *testing.T) {
	src := `fn parse(s: String) -> Int? {
    if s == "yes" {
        Some(42)
    } else {
        None
    }
}

fn double(s: String) -> Int? {
    let x = parse(s)?
    Some(x * 2)
}

fn main() {
    match double("yes") {
        Some(n) -> println("double yes = {n}"),
        None -> println("fail yes"),
    }
    match double("no") {
        Some(n) -> println("double no = {n}"),
        None -> println("fail no"),
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "double yes = 84\nfail no\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestDeferLIFO verifies multiple defers execute in LIFO order.
func TestDeferLIFO(t *testing.T) {
	src := `fn main() {
    defer println("3")
    defer println("2")
    defer println("1")
    println("0")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "0\n1\n2\n3\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}
