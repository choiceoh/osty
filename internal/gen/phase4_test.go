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

// TestOptionMethods verifies the std.option method surface over the
// backend's pointer representation for Option<T>.
func TestOptionMethods(t *testing.T) {
	src := `use std.option

fn fallback() -> Int? {
    Some(9)
}

fn main() {
    let a: Int? = Some(4)
    let b: Int? = None
    let c: Int? = option.Some(6)
    let d: Int? = option.None
    println("{a.isSome()} {b.isNone()}")
    println("{a.unwrap()} {b.unwrapOr(7)}")
    println("{c.unwrap()} {d.unwrapOr(10)}")
    match b.orElse(fallback) {
        Some(v) -> println("{v}"),
        None -> println("none"),
    }
    let mapped = a.map(|x| x + 1)
    println("{mapped.unwrap()}")
    println("{a.expect("present")} {b.unwrapOrElse(|| 8)}")
    let chained = a.andThen(|x| Some("n={x}"))
    println(chained.unwrap())
    let anded = a.and(Some("and"))
    let noneAnd = b.and(Some("skip"))
    println("{anded.unwrap()} {noneAnd.isNone()}")
    let ored = b.or(Some(12))
    let kept = a.or(Some(99))
    println("{ored.unwrap()} {kept.unwrap()}")
    let xored = a.xor(None)
    let xoredNone = a.xor(Some(5))
    println("{xored.unwrap()} {xoredNone.isNone()}")
    let filtered = a.filter(|x| x > 3)
    let filteredOut = a.filter(|x| x < 0)
    println("{filtered.unwrap()} {filteredOut.isNone()}")
    let inspected = a.inspect(|x| println("inspect {x}"))
    println(inspected.unwrap())
    let result = b.orError("missing")
    println("{result.isErr()}")
    println(result.unwrapErr().message())
    match result.err() {
        Some(e) -> println(e.message()),
        None -> println("no error"),
    }
    let mappedResult = result.map(|x| "{x}")
    println("{mappedResult.isErr()}")
    let mappedErr = result.mapErr(|e| e.message())
    println(mappedErr.unwrapErr())
    let alias = b.okOr("alias")
    println(alias.unwrapErr().message())
    println(a.toString())
    println(b.toString())
}
`
	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "true true\n4 7\n6 10\n9\n5\n4 8\nn=4\nand true\n12 4\n4 true\n4 true\ninspect 4\n4\ntrue\nmissing\nmissing\ntrue\nmissing\nalias\nSome(4)\nNone\n"
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
