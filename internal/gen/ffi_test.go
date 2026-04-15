package gen_test

import (
	"strings"
	"testing"
)

// TestFFIStructRoundTrip — §12.2: a struct declared inside a
// `use go { ... }` block is emitted as a Go type alias pointing at the
// package's real type. A Result<Struct?, Error>-returning FFI call can
// therefore be matched, the inner pointer dereferenced, and exported
// fields read directly.
//
// net/url.Parse is the test fixture because:
//   - It follows Go's idiomatic (*T, error) return convention; the
//     pointer flows through Osty's `URL?` Optional without extra
//     wrapping.
//   - `url.URL` has exported scalar fields (`Scheme`, `Host`) whose
//     Osty-side field access can be verified without touching methods.
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
	if !strings.Contains(string(goSrc), "type URL = url.URL") {
		t.Errorf("expected FFI struct emitted as type alias:\n%s", goSrc)
	}
	out := runGo(t, goSrc)
	want := "scheme=https host=example.com\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

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
	if !strings.Contains(string(goSrc), "Result[int, ostyError]") {
		t.Errorf("expected Result[int, ostyError] bridge in output:\n%s", goSrc)
	}
	if !strings.Contains(string(goSrc), "basicFFIError{") {
		t.Errorf("expected Go error wrapped in basicFFIError:\n%s", goSrc)
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

// TestFFIResultErrorMessage — §12.4 BasicError wrapping. Matching on
// an Err-arm of an FFI-sourced Result must bind `e` to a value whose
// `.message()` returns the underlying Go error's message string.
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
	// The underlying strconv.Atoi error spells "invalid syntax"; the
	// assertion stays loose because Go could reword it in a future
	// release, but the distinctive fragment should remain.
	if !strings.Contains(out, "invalid syntax") {
		t.Errorf("expected strconv error message in output; got %q", out)
	}
}

// TestFFIOptionalPassthrough — §12.3: Osty `T?` lowers to `*T`, which
// is exactly Go's nullable convention. The transpiler must therefore
// emit the call site unchanged, with no IIFE wrapper. Runtime check
// uses `bytes.NewBuffer` which returns a `*bytes.Buffer`; wrapping it
// in `Buffer?` lets the Osty side receive a non-nil Optional and the
// match-arm discriminator should walk the nil check without extra
// bridge logic.
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
	// No IIFE / Result wrapper should be introduced for the plain
	// optional return — the Go call flows straight through.
	if strings.Contains(string(goSrc), "Result[") {
		t.Errorf("optional-return FFI call should not produce Result wrapper:\n%s", goSrc)
	}
	if !strings.Contains(string(goSrc), "bytes.NewBufferString(") {
		t.Errorf("expected a direct bytes.NewBufferString call:\n%s", goSrc)
	}
	out := runGo(t, goSrc)
	if out != "got-buffer\n" {
		t.Errorf("got %q\n--- src ---\n%s", out, goSrc)
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
