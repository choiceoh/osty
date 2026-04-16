package gen_test

import "testing"

// TestResultOkErr covers the basic Ok/Err construction and match-arm
// decomposition over a Result<T, E> value.
func TestResultOkErr(t *testing.T) {
	src := `fn parse(s: String) -> Result<Int, String> {
    if s == "ok" {
        Ok(42)
    } else {
        Err("bad input")
    }
}

fn main() {
    match parse("ok") {
        Ok(n) -> println("got {n}"),
        Err(e) -> println("fail: {e}"),
    }
    match parse("bad") {
        Ok(n) -> println("got {n}"),
        Err(e) -> println("fail: {e}"),
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "got 42\nfail: bad input\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestResultQuestionLift — `?` at the canonical `let x = expr?` site.
func TestResultQuestionLift(t *testing.T) {
	src := `fn parse(s: String) -> Result<Int, String> {
    if s == "ok" {
        Ok(42)
    } else {
        Err("bad")
    }
}

fn doubled(s: String) -> Result<Int, String> {
    let x = parse(s)?
    Ok(x * 2)
}

fn main() {
    match doubled("ok") {
        Ok(n) -> println("got {n}"),
        Err(e) -> println("fail: {e}"),
    }
    match doubled("no") {
        Ok(n) -> println("got {n}"),
        Err(e) -> println("fail: {e}"),
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "got 84\nfail: bad\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestResultGuard — guards on Result variants should see the bound
// value and discriminate on it.
func TestResultGuard(t *testing.T) {
	src := `fn classify(r: Result<Int, String>) -> String {
    match r {
        Ok(n) if n > 0 -> "positive",
        Ok(n) if n < 0 -> "negative",
        Ok(_) -> "zero",
        Err(e) -> "error: {e}",
    }
}

fn main() {
    println(classify(Ok(5)))
    println(classify(Ok(-3)))
    println(classify(Ok(0)))
    println(classify(Err("boom")))
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "positive\nnegative\nzero\nerror: boom\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

func TestResultIntrinsicMethodsRuntime(t *testing.T) {
	src := `fn main() {
    let ok: Result<Int, String> = Ok(5)
    println(ok.isOk())
    println(ok.isErr())
    println(ok.unwrap())
    println(ok.expect("ok expected"))
    println(ok.unwrapOr(9))
    println(ok.toString())
    ok.inspect(|n| println("seen ok {n}"))
    let nested: Result<Result<Int, String>, String> = Ok(ok)
    println(nested.toString())
    println("{nested}")
    match ok.ok() {
        Some(n) -> println("ok value: {n}"),
        None -> println("missing"),
    }

    let err: Result<Int, String> = Err("bad")
    println(err.isOk())
    println(err.isErr())
    println(err.unwrapOr(9))
    println(err.unwrapOrElse(|e| e.len()))
    println(err.unwrapErr())
    println(err.expectErr("err expected"))
    println(err.toString())
    err.inspectErr(|e| println("seen err {e}"))
    match err.err() {
        Some(e) -> println("err value: {e}"),
        None -> println("missing"),
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "true\nfalse\n5\n5\n5\nOk(5)\nseen ok 5\nOk(Ok(5))\nOk(Ok(5))\nok value: 5\nfalse\ntrue\n9\n3\nbad\nbad\nErr(bad)\nseen err bad\nerr value: bad\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

func TestResultMapAndMapErr(t *testing.T) {
	src := `fn parse(s: String) -> Result<Int, Int> {
    if s == "ok" { Ok(2) } else { Err(40) }
}

fn main() {
    let mapped: Result<String, Int> = parse("ok").map(|n| "v={n}")
    match mapped {
        Ok(s) -> println("mapped {s}"),
        Err(e) -> println("err {e}"),
    }

    let stillErr: Result<String, Int> = parse("bad").map(|n| "v={n}")
    match stillErr {
        Ok(s) -> println("mapped {s}"),
        Err(e) -> println("still err {e}"),
    }

    let errMapped: Result<Int, String> = parse("bad").mapErr(|e| "e={e}")
    match errMapped {
        Ok(n) -> println("ok {n}"),
        Err(e) -> println("mapped err {e}"),
    }

    let next: Result<String, Int> = Ok("next")
    let anded = parse("ok").and(next)
    match anded {
        Ok(s) -> println("and {s}"),
        Err(e) -> println("and err {e}"),
    }

    let nextErr: Result<String, Int> = Ok("skip")
    let stopped = parse("bad").and(nextErr)
    match stopped {
        Ok(s) -> println("and {s}"),
        Err(e) -> println("and err {e}"),
    }

    let chained = parse("ok").andThen(|n| Ok("n={n}"))
    match chained {
        Ok(s) -> println("chain {s}"),
        Err(e) -> println("chain err {e}"),
    }

    let fallback: Result<Int, String> = Ok(7)
    let recovered = parse("bad").or(fallback)
    match recovered {
        Ok(n) -> println("or {n}"),
        Err(e) -> println("or err {e}"),
    }

    let changedErr = parse("bad").orElse(|e| Err("e={e}"))
    match changedErr {
        Ok(n) -> println("orElse ok {n}"),
        Err(e) -> println("orElse err {e}"),
    }

    let inspected = parse("ok").inspect(|n| println("inspect {n}"))
    println(inspected.unwrap())
    let inspectedErr = parse("bad").inspectErr(|e| println("inspect err {e}"))
    println(inspectedErr.unwrapErr())
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "mapped v=2\nstill err 40\nmapped err e=40\nand next\nand err 40\nchain n=2\nor 7\norElse err e=40\ninspect 2\n2\ninspect err 40\n40\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestResultChainedQuestion — two `?` lifts in a row, both in a
// Result-returning function.
func TestResultChainedQuestion(t *testing.T) {
	src := `fn parse(s: String) -> Result<Int, String> {
    if s == "a" { Ok(10) } else if s == "b" { Ok(20) } else { Err("bad {s}") }
}

fn sum(a: String, b: String) -> Result<Int, String> {
    let x = parse(a)?
    let y = parse(b)?
    Ok(x + y)
}

fn main() {
    match sum("a", "b") {
        Ok(n) -> println("sum: {n}"),
        Err(e) -> println("err: {e}"),
    }
    match sum("a", "c") {
        Ok(n) -> println("sum: {n}"),
        Err(e) -> println("err: {e}"),
    }
    match sum("z", "b") {
        Ok(n) -> println("sum: {n}"),
        Err(e) -> println("err: {e}"),
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "sum: 30\nerr: bad c\nerr: bad z\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestQuestionInCallArgument — `?` nested inside a function call
// argument. Lift hoists the temp above the call; the call itself sees
// the unwrapped value.
func TestQuestionInCallArgument(t *testing.T) {
	src := `fn parse(s: String) -> Result<Int, String> {
    if s == "good" { Ok(7) } else { Err("bad") }
}

fn double(n: Int) -> Int { n * 2 }

fn work(s: String) -> Result<Int, String> {
    Ok(double(parse(s)?))
}

fn main() {
    match work("good") {
        Ok(n) -> println("ok: {n}"),
        Err(e) -> println("err: {e}"),
    }
    match work("nope") {
        Ok(n) -> println("ok: {n}"),
        Err(e) -> println("err: {e}"),
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "ok: 14\nerr: bad\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestQuestionInBinary — `?` on both sides of a binary operator.
func TestQuestionInBinary(t *testing.T) {
	src := `fn parse(s: String) -> Result<Int, String> {
    if s == "a" { Ok(10) } else if s == "b" { Ok(20) } else { Err("bad {s}") }
}

fn add(a: String, b: String) -> Result<Int, String> {
    Ok(parse(a)? + parse(b)?)
}

fn main() {
    match add("a", "b") {
        Ok(n) -> println("sum: {n}"),
        Err(e) -> println("err: {e}"),
    }
    match add("x", "b") {
        Ok(n) -> println("sum: {n}"),
        Err(e) -> println("err: {e}"),
    }
    match add("a", "y") {
        Ok(n) -> println("sum: {n}"),
        Err(e) -> println("err: {e}"),
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "sum: 30\nerr: bad x\nerr: bad y\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestQuestionInReturn — `return expr?` form.
func TestQuestionInReturn(t *testing.T) {
	src := `fn parse(s: String) -> Result<Int, String> {
    if s == "x" { Ok(1) } else { Err("bad") }
}

fn wrap(s: String) -> Result<Int, String> {
    return Ok(parse(s)?)
}

fn main() {
    match wrap("x") {
        Ok(n) -> println("got {n}"),
        Err(e) -> println("err: {e}"),
    }
    match wrap("y") {
        Ok(n) -> println("got {n}"),
        Err(e) -> println("err: {e}"),
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "got 1\nerr: bad\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestOptionQuestionInCall — `?` on Option propagates None up.
func TestOptionQuestionInCall(t *testing.T) {
	src := `fn lookup(k: Int) -> Int? {
    if k > 0 { Some(k * 10) } else { None }
}

fn pairSum(a: Int, b: Int) -> Int? {
    Some(lookup(a)? + lookup(b)?)
}

fn main() {
    match pairSum(1, 2) { Some(n) -> println("ok: {n}"), None -> println("miss") }
    match pairSum(0, 2) { Some(n) -> println("ok: {n}"), None -> println("miss") }
    match pairSum(1, -1) { Some(n) -> println("ok: {n}"), None -> println("miss") }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "ok: 30\nmiss\nmiss\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestResultExprStmtQuestion — `expr?` as a statement: propagate on
// failure, discard the unwrapped value on success.
func TestResultExprStmtQuestion(t *testing.T) {
	src := `fn check(s: String) -> Result<Int, String> {
    if s == "ok" { Ok(1) } else { Err("bad {s}") }
}

fn pipeline(a: String, b: String) -> Result<String, String> {
    check(a)?
    check(b)?
    Ok("both ok")
}

fn main() {
    match pipeline("ok", "ok") { Ok(s) -> println(s), Err(e) -> println("fail: {e}") }
    match pipeline("ok", "no") { Ok(s) -> println(s), Err(e) -> println("fail: {e}") }
    match pipeline("no", "ok") { Ok(s) -> println(s), Err(e) -> println("fail: {e}") }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "both ok\nfail: bad no\nfail: bad no\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}
