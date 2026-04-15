package gen_test

import (
	"strings"
	"testing"
)

func TestStdRandomRuntimeBridge(t *testing.T) {
	src := `use std.random

fn main() {
    let a = random.seeded(7)
    let b = random.seeded(7)
    let x = a.int(0, 100000)
    let y = b.int(0, 100000)
    println("{x == y}")
}
`
	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "true" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

func TestStdURLRuntimeBridge(t *testing.T) {
	src := `use std.url

fn main() {
    match url.parse("https://example.com:8080/path?q=1#top") {
        Ok(u) -> {
            let port = u.port ?? 0
            let fragment = u.fragment ?? ""
            println("{u.scheme} {u.host} {port} {u.path} {fragment} {u.toString()}")
        },
        Err(e) -> println("err: {e}"),
    }
}
`
	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "https example.com 8080 /path top https://example.com:8080/path?q=1#top"
	if strings.TrimSpace(out) != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", strings.TrimSpace(out), want, goSrc)
	}
}
