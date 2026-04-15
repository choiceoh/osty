package gen_test

import (
	"os"
	"path/filepath"
	"strconv"
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

func TestStdFSRuntimeBridge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	src := `use std.fs as os

fn main() {
    let path = ` + strconv.Quote(path) + `
    println("{os.exists(path)}")
    os.writeString(path, "hello osty").unwrap()
    println("{os.exists(path)}")
    println(os.readToString(path).unwrap())
    os.remove(path).unwrap()
    println("{os.exists(path)}")
    println("{os.readToString(path).isErr()}")
}
`
	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := strings.Join([]string{
		"false",
		"true",
		"hello osty",
		"false",
		"true",
	}, "\n")
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
	for _, want := range []string{"_ostyStdFSOS.ReadFile", "_ostyStdFSOS.WriteFile", "_ostyStdFSOS.Stat", "_ostyStdFSOS.Remove"} {
		if !strings.Contains(string(goSrc), want) {
			t.Errorf("generated std.fs bridge missing %s:\n%s", want, goSrc)
		}
	}
}

func TestStdFSReadToStringRejectsInvalidUTF8(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.bin")
	if err := os.WriteFile(path, []byte{0xff, 0xfe, 0xfd}, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	src := `use std.fs

fn main() {
    let path = ` + strconv.Quote(path) + `
    println("{fs.readToString(path).isErr()}")
}
`
	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	if out != "true" {
		t.Errorf("got %q, want true\n--- src ---\n%s", out, goSrc)
	}
	if !strings.Contains(string(goSrc), "_ostyStdFSUTF8.Valid") {
		t.Errorf("generated std.fs bridge missing UTF-8 validation:\n%s", goSrc)
	}
}

func TestStdFSBridgeDoesNotReserveHelperNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	src := `use std.fs

fn fsReadToString() -> String { "user-read" }
fn fsWriteString() -> String { "user-write" }
fn fsExists() -> String { "user-exists" }
fn fsRemove() -> String { "user-remove" }

fn main() {
    let path = ` + strconv.Quote(path) + `
    fs.writeString(path, fsReadToString()).unwrap()
    println(fs.readToString(path).unwrap())
    println(fsWriteString())
    println(fsExists())
    fs.remove(path).unwrap()
    println(fsRemove())
}
`
	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := strings.Join([]string{
		"user-read",
		"user-write",
		"user-exists",
		"user-remove",
	}, "\n")
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

func TestStdFSAvoidsInternalImportAliasCollisions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	src := `use std.fs as _ostyStdFSOS

fn _ostyStdFSUTF8() -> String { "user-utf8-name" }

fn main() {
    let path = ` + strconv.Quote(path) + `
    _ostyStdFSOS.writeString(path, _ostyStdFSUTF8()).unwrap()
    println(_ostyStdFSOS.readToString(path).unwrap())
    _ostyStdFSOS.remove(path).unwrap()
}
`
	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	if out != "user-utf8-name" {
		t.Errorf("got %q, want user-utf8-name\n--- src ---\n%s", out, goSrc)
	}
	for _, want := range []string{"_ostyStdFSOS2.ReadFile", "_ostyStdFSUTF82.Valid"} {
		if !strings.Contains(string(goSrc), want) {
			t.Errorf("generated std.fs bridge did not avoid internal alias collision %s:\n%s", want, goSrc)
		}
	}
}
