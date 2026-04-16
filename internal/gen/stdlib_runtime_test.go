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

func TestStdStringsInlineOstyRuntime(t *testing.T) {
	src := `use std.strings

fn main() {
    println(strings.join(strings.split("a,b,c", ","), "|"))
    println(strings.join(strings.splitN("a,b,c", ",", 2), "|"))
    println(strings.toLower(strings.trimSpace("  HI  ")))
    println(strings.replaceAll("aaaa", "aa", "b"))
    println("{strings.len("é")} {strings.charCount("é")}")
}
`
	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := strings.Join([]string{
		"a|b|c",
		"a|b,c",
		"hi",
		"bb",
		"2 1",
	}, "\n")
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
	for _, want := range []string{"func _ostyStdStrings_split", "func _ostyStdStrings_replaceN"} {
		if !strings.Contains(string(goSrc), want) {
			t.Errorf("generated std.strings inline body missing %s:\n%s", want, goSrc)
		}
	}
	for _, absent := range []string{"var strings =", "import \"strings\""} {
		if strings.Contains(string(goSrc), absent) {
			t.Errorf("std.strings should not lower through Go bridge/stub %q:\n%s", absent, goSrc)
		}
	}
}

func TestStdFmtInlineOstyRuntime(t *testing.T) {
	src := `use std.fmt

fn main() {
    println(fmt.padLeft("7", 3, '0'))
    println(fmt.hex(255))
    println(fmt.hex(255, true))
    println(fmt.truncate("abcdef", 4))
    println(fmt.fixed(12.345, 2))
    println(fmt.thousands(-1234567))
    println(fmt.format(r"{0} + {1} = {2}", ["1", "2", "3"]))
    println(fmt.join(["a", "b", "c"], ", ", " and "))
}
`
	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := strings.Join([]string{
		"007",
		"ff",
		"FF",
		"a...",
		"12.35",
		"-1,234,567",
		"1 + 2 = 3",
		"a, b and c",
	}, "\n")
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
	for _, want := range []string{"func _ostyStdFmt_padLeft", "func _ostyStdFmt_format", "func _ostyStdStrings_repeat"} {
		if !strings.Contains(string(goSrc), want) {
			t.Errorf("generated std.fmt inline body missing %s:\n%s", want, goSrc)
		}
	}
	for _, absent := range []string{"var fmt =", "var strings =", "import \"strings\""} {
		if strings.Contains(string(goSrc), absent) {
			t.Errorf("std.fmt should not lower through Go bridge/stub %q:\n%s", absent, goSrc)
		}
	}
}

func TestStdStringsGraphemesRuntime(t *testing.T) {
	src := `use std.strings

fn main() {
    println(strings.join(strings.graphemes("éo"), "|"))
    println(strings.graphemes("👨‍👩‍👧‍👦").len())
    println(strings.join(strings.graphemes("🇰🇷🇺🇸"), "|"))
    println(strings.join(strings.graphemes("👍🏽!"), "|"))
    println(strings.graphemes("a\r\nb").len())
    println(strings.graphemes("각").len())
    println(strings.graphemes("क्षि").len())
    println("é".graphemes().len())
}
`
	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := strings.Join([]string{
		"é|o",
		"1",
		"🇰🇷|🇺🇸",
		"👍🏽|!",
		"3",
		"1",
		"1",
		"1",
	}, "\n")
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
	if !strings.Contains(string(goSrc), "func _ostyStdStrings_graphemes") {
		t.Errorf("generated std.strings graphemes body missing:\n%s", goSrc)
	}
	if strings.Contains(string(goSrc), "func ostyStringGraphemes") {
		t.Errorf("std.strings.graphemes should be emitted from Osty source, not Go runtime helper:\n%s", goSrc)
	}
}

func TestStdStringsGraphemesUnicodeBreakTest(t *testing.T) {
	if testing.Short() {
		t.Skip("Unicode GraphemeBreakTest generated-Go execution test (slow)")
	}
	cases := loadGraphemeBreakCases(t)
	var src strings.Builder
	src.WriteString(`use std.strings

fn check(got: List<String>, want: List<String>, label: String) -> Int {
    if got.len() != want.len() {
        println(label)
        return 1
    }
    for i in 0..got.len() {
        if got[i] != want[i] {
            println(label)
            return 1
        }
    }
    0
}

fn main() {
    let mut fails = 0
`)
	for _, c := range cases {
		src.WriteString("    fails = fails + check(strings.graphemes(")
		src.WriteString(ostyRuneString(c.input))
		src.WriteString("), ")
		src.WriteString(ostyStringList(c.clusters))
		src.WriteString(", ")
		src.WriteString(strconv.Quote(c.label))
		src.WriteString(")\n")
	}
	src.WriteString(`    println(fails)
}
`)
	goSrc, err := transpileWithStdlib(t, src.String())
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	if out != "0" {
		t.Fatalf("Unicode GraphemeBreakTest failures: %s\n--- osty src ---\n%s\n--- go src ---\n%s", out, src.String(), goSrc)
	}
}

type graphemeBreakCase struct {
	label    string
	input    []rune
	clusters [][]rune
}

func loadGraphemeBreakCases(t *testing.T) []graphemeBreakCase {
	t.Helper()
	path := filepath.Join("..", "stdlib", "unicode", "17.0.0", "GraphemeBreakTest.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []graphemeBreakCase
	for lineNo, line := range strings.Split(string(data), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var input []rune
		var clusters [][]rune
		var current []rune
		for _, field := range strings.Fields(line) {
			switch field {
			case "÷":
				if len(current) > 0 {
					clusters = append(clusters, current)
					current = nil
				}
			case "×":
				continue
			default:
				v, err := strconv.ParseInt(field, 16, 32)
				if err != nil {
					t.Fatalf("line %d: parse %q: %v", lineNo+1, field, err)
				}
				r := rune(v)
				input = append(input, r)
				current = append(current, r)
			}
		}
		if len(input) > 0 {
			out = append(out, graphemeBreakCase{
				label:    "GraphemeBreakTest.txt:" + strconv.Itoa(lineNo+1),
				input:    input,
				clusters: clusters,
			})
		}
	}
	return out
}

func ostyStringList(clusters [][]rune) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, cluster := range clusters {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(ostyRuneString(cluster))
	}
	b.WriteByte(']')
	return b.String()
}

func ostyRuneString(rs []rune) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range rs {
		b.WriteString(`\u{`)
		b.WriteString(strings.ToUpper(strconv.FormatInt(int64(r), 16)))
		b.WriteByte('}')
	}
	b.WriteByte('"')
	return b.String()
}
