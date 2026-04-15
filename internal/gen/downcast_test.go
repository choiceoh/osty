package gen_test

import (
	"strings"
	"testing"
)

// TestErrorDowncast_Success — a value upcast to Error and then
// `downcast::<FsError>()` round-trips to Some(...) and the match arm
// fires.
func TestErrorDowncast_Success(t *testing.T) {
	src := `
pub enum FsError {
    NotFound,

    pub fn message(self) -> String {
        match self {
            NotFound -> "nf",
        }
    }
}

fn classify(err: Error) -> String {
    match err.downcast::<FsError>() {
        Some(_) -> "fs",
        None -> "other",
    }
}

fn main() {
    let e: Error = NotFound
    println(classify(e))
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	if !strings.Contains(string(goSrc), "if v, ok := ") {
		t.Errorf("expected type-assertion thunk in output:\n%s", goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "fs" {
		t.Errorf("got %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestErrorDowncast_Miss — when the stored concrete type doesn't
// match, downcast yields None and the fallback arm fires.
func TestErrorDowncast_Miss(t *testing.T) {
	src := `
pub enum FsError {
    NotFound,

    pub fn message(self) -> String { "nf" }
}

pub enum NetError {
    Down,

    pub fn message(self) -> String { "down" }
}

fn classify(err: Error) -> String {
    match err.downcast::<FsError>() {
        Some(_) -> "fs",
        None -> "other",
    }
}

fn main() {
    let e: Error = Down
    println(classify(e))
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "other" {
		t.Errorf("got %q\n--- src ---\n%s", out, goSrc)
	}
}
