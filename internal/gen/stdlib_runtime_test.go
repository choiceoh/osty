package gen_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestStdErrorRuntimeBridge(t *testing.T) {
	src := `use std.error

fn fail() -> Result<Int, Error> {
    Err(error.new("boom"))
}

fn main() {
	let direct = Error.new("direct")
	println(direct.message())
	let qualified = error.Error.new("qualified")
	println(qualified.message())
	match fail() {
		Ok(n) -> println("ok {n}"),
		Err(e) -> println(e.message()),
	}
    let literal = error.BasicError { message: "literal" }
    println(literal.message())
}
`
	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := strings.Join([]string{"direct", "qualified", "boom", "literal"}, "\n")
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
	for _, want := range []string{"ostyErrorNew", "ostyErrorMessage", "type ostyBasicError struct"} {
		if !strings.Contains(string(goSrc), want) {
			t.Errorf("generated std.error bridge missing %s:\n%s", want, goSrc)
		}
	}
}

func TestStdErrorDowncastRuntimeBridge(t *testing.T) {
	src := `use std.error

pub enum FsError {
    NotFound(String),
    PermissionDenied(String),

    pub fn message(self) -> String {
        match self {
            NotFound(p) -> "not found: {p}",
            PermissionDenied(p) -> "permission denied: {p}",
        }
    }
}

fn read() -> Result<Int, FsError> {
    Err(NotFound("cfg"))
}

fn load() -> Result<Int, Error> {
    let value = read()?
    Ok(value)
}

fn direct() -> Result<Int, Error> {
    Err(NotFound("direct"))
}

fn main() {
    match load() {
        Ok(_) -> println("ok"),
        Err(e) -> {
            println(e.message())
            match e.downcast::<FsError>() {
                Some(fe) -> println(fe.message()),
                None -> println("missing"),
            }
        },
    }
    match direct() {
        Ok(_) -> println("direct ok"),
        Err(e) -> println(e.message()),
    }
    let basic = Error.new("basic")
    match basic.downcast::<error.BasicError>() {
        Some(be) -> println(be.message()),
        None -> println("missing basic"),
    }
}
`
	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := strings.Join([]string{
		"not found: cfg",
		"not found: cfg",
		"not found: direct",
		"basic",
	}, "\n")
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
	for _, want := range []string{"ostyErrorDowncast[FsError]", "func (self *FsError_NotFound) message() string"} {
		if !strings.Contains(string(goSrc), want) {
			t.Errorf("generated std.error downcast bridge missing %s:\n%s", want, goSrc)
		}
	}
}

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

func TestStdRefSameRuntimeBridge(t *testing.T) {
	src := `use std.ref

struct Box {
    value: Int,

    fn sameAs(self, other: Self) -> Bool {
        ref.same(self, other)
    }
}

enum Token {
    Word(String),
    End,
}

fn main() {
    let xs: List<Int> = [1, 2, 3]
    let alias = xs
    let other: List<Int> = [1, 2, 3]
    let empty: List<Int> = []
    let sameEmpty = empty
    let otherEmpty: List<Int> = []

    let names: Map<String, Int> = {"a": 1}
    let sameNames = names
    let otherNames: Map<String, Int> = {"a": 1}

    let box = Box { value: 7 }
    let sameBox = box
    let otherBox = Box { value: 7 }
    let spreadBox = Box { value: 8, ..box }

    let tok = Word("osty")
    let sameTok = tok
    let otherTok = Word("osty")
    let end = End
    let sameEnd = end
    let otherEnd = End

    let fnA = || 1
    let fnAlias = fnA
    let fnOther = || 1

    let ok: Result<Int, String> = Ok(1)
    let sameOk = ok
    let otherOk: Result<Int, String> = Ok(1)
    let err: Result<Int, String> = Err("bad")
    let sameErr = err
    let otherErr: Result<Int, String> = Err("bad")

    println("{ref.same(xs, alias)}")
    println("{ref.same(xs, other)}")
    println("{ref.same(empty, sameEmpty)}")
    println("{ref.same(empty, otherEmpty)}")
    println("{ref.same(names, sameNames)}")
    println("{ref.same(names, otherNames)}")
    println("{ref.same(box, sameBox)}")
    println("{ref.same(box, otherBox)}")
    println("{ref.same(box, spreadBox)}")
    println("{box.sameAs(sameBox)}")
    println("{ref.same(tok, sameTok)}")
    println("{ref.same(tok, otherTok)}")
    println("{ref.same(end, sameEnd)}")
    println("{ref.same(end, otherEnd)}")
    println("{ref.same(fnA, fnAlias)}")
    println("{ref.same(fnA, fnOther)}")
    println("{ref.same(ok, sameOk)}")
    println("{ref.same(ok, otherOk)}")
    println("{ref.same(err, sameErr)}")
	println("{ref.same(err, otherErr)}")
	println("{ref.same(1, 1)}")
	println("{ref.same("osty", "osty")}")
	println("{box == otherBox}")
	println("{box != spreadBox}")
	println("{tok == otherTok}")
	println("{end == otherEnd}")
	println("{ok == otherOk}")
	println("{err == otherErr}")
}
`
	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := strings.Join([]string{
		"true",
		"false",
		"true",
		"false",
		"true",
		"false",
		"true",
		"false",
		"false",
		"true",
		"true",
		"false",
		"true",
		"false",
		"true",
		"false",
		"true",
		"false",
		"true",
		"false",
		"false",
		"false",
		"true",
		"true",
		"true",
		"true",
		"true",
		"true",
	}, "\n")
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
	for _, want := range []string{"func refSame", "reflect.ValueOf"} {
		if !strings.Contains(string(goSrc), want) {
			t.Errorf("generated std.ref bridge missing %s:\n%s", want, goSrc)
		}
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
