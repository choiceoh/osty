package gen_test

import (
	"strings"
	"testing"
)

// Methods declared inside an FFI struct forward to the Go type's real
// method set via the emitted type alias. time.Time is a stable fixture.
func TestFFIStructMethodCall(t *testing.T) {
	src := `use go "time" {
    struct Time {
        fn Year(self) -> Int
        fn Unix(self) -> Int64
    }
    fn Unix(sec: Int64, nsec: Int64) -> Time
}

fn main() {
    let t = time.Unix(1700000000, 0)
    println("year={t.Year()} unix={t.Unix()}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if !strings.Contains(out, "year=") || !strings.Contains(out, "unix=1700000000") {
		t.Errorf("got %q; want a line containing year=... unix=1700000000\n--- src ---\n%s",
			out, goSrc)
	}
}

// A Result<Struct?, Error>-returning FFI call; exercises the struct
// alias, the Optional pointer pass-through, and field access on the
// dereferenced Go type all in one path.
func TestFFIStructRoundTrip(t *testing.T) {
	src := `use go "net/url" {
    struct URL {
        Scheme: String,
        Host: String,
    }
    fn Parse(rawurl: String) -> Result<URL?, Error>
}

fn describe(u: URL?) {
    match u {
        Some(uu) -> println("scheme={uu.Scheme} host={uu.Host}"),
        None -> println("none"),
    }
}

fn main() {
    match url.Parse("https://example.com/index") {
        Ok(u) -> describe(u),
        Err(e) -> println("err: {e.message()}"),
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "scheme=https host=example.com\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// strconv.Atoi's (int, error) tuple lifts into an Osty Result; both
// the Ok and Err arms bind through the §12.4 bridge.
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
	out := runGo(t, goSrc)
	want := "ok 42\nerr\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// The bridge survives an intervening `let` — exercises the Result
// materialization in a non-match position where the call flows through
// a temporary.
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

// The BasicError wrapper must expose .message() against the Go error's
// underlying text. The "invalid syntax" fragment is loose enough to
// survive future Go reformulations of the strconv error.
func TestFFIResultErrorMessage(t *testing.T) {
	src := `use go "strconv" {
    fn Atoi(s: String) -> Result<Int, Error>
}

fn main() {
    match strconv.Atoi("not-a-number") {
        Ok(n) -> println("ok {n}"),
        Err(e) -> println("msg: {e.message()}"),
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if !strings.HasPrefix(out, "msg: ") {
		t.Errorf("expected 'msg: ...' prefix; got %q\n--- src ---\n%s", out, goSrc)
	}
	if !strings.Contains(out, "invalid syntax") {
		t.Errorf("expected strconv error message in output; got %q", out)
	}
}

// Regression guard: a `T?` FFI return must NOT be wrapped in the
// Result bridge; Osty Optional already lowers to Go `*T`.
func TestFFIOptionalPassthrough(t *testing.T) {
	src := `use go "bytes" {
    struct Buffer {}
    fn NewBufferString(s: String) -> Buffer?
}

fn main() {
    match bytes.NewBufferString("hello") {
        Some(_) -> println("got-buffer"),
        None -> println("none"),
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	if strings.Contains(string(goSrc), "Result[") {
		t.Errorf("optional-return FFI call should not produce Result wrapper:\n%s", goSrc)
	}
	out := runGo(t, goSrc)
	if out != "got-buffer\n" {
		t.Errorf("got %q\n--- src ---\n%s", out, goSrc)
	}
}

// Regression guard: a plain-value FFI return keeps the direct-emission
// path and does not route through the Result bridge.
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
