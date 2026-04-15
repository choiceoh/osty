package gen_test

import (
	"strings"
	"testing"
)

// TestFFIResultAtoi — `strconv.Atoi` returns Go's `(int, error)` tuple;
// the §12.4 bridge wraps it into the Osty Result runtime so pattern
// matching on Ok/Err works at the call site.
func TestFFIResultAtoi(t *testing.T) {
	src := `use go "strconv" {
    fn Atoi(s: String) -> Result<Int, Error>
}

fn main() {
    match strconv.Atoi("42") {
        Ok(n) -> println("ok {n}"),
        Err(e) -> println("err"),
    }
    match strconv.Atoi("xx") {
        Ok(n) -> println("ok {n}"),
        Err(e) -> println("err"),
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	if !strings.Contains(string(goSrc), "Result[int, any]") {
		t.Errorf("expected Result[int, any] bridge in output:\n%s", goSrc)
	}
	out := runGo(t, goSrc)
	want := "ok 42\nerr\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestFFIResultLetBinding — a Result-returning FFI call can be let-bound
// and subsequently matched. This exercises the bridge in a non-match
// position where the call flows through a temporary.
func TestFFIResultLetBinding(t *testing.T) {
	src := `use go "strconv" {
    fn Atoi(s: String) -> Result<Int, Error>
}

fn main() {
    let r = strconv.Atoi("100")
    match r {
        Ok(n) -> println("doubled: {n * 2}"),
        Err(e) -> println("fail"),
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "doubled: 200\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestFFINonResultUnchanged — an FFI call whose declared return is a
// plain value type keeps the direct-emission path. Guards against the
// Result bridge over-reaching.
func TestFFINonResultUnchanged(t *testing.T) {
	src := `use go "strings" {
    fn ToUpper(s: String) -> String
}

fn main() {
    println(strings.ToUpper("osty"))
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	if strings.Contains(string(goSrc), "Result[") {
		t.Errorf("non-Result FFI call should not wrap in Result:\n%s", goSrc)
	}
	out := runGo(t, goSrc)
	if out != "OSTY\n" {
		t.Errorf("got %q\n--- src ---\n%s", out, goSrc)
	}
}
