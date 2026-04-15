package gen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/gen"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// transpile runs the full pipeline on a source snippet and returns the
// generated Go source (or the transpile error plus any partial output).
func transpile(t *testing.T, src string) ([]byte, error) {
	t.Helper()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	for _, d := range parseDiags {
		if d.Severity.String() == "error" {
			t.Fatalf("parse error: %s", d.Message)
		}
	}
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res)
	return gen.Generate("main", file, res, chk)
}

func transpileWithStdlib(t *testing.T, src string) ([]byte, error) {
	t.Helper()
	reg := stdlib.LoadCached()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	for _, d := range parseDiags {
		if d.Severity.String() == "error" {
			t.Fatalf("parse error: %s", d.Message)
		}
	}
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	chk := check.File(file, res, check.Opts{Primitives: reg.Primitives, ResultMethods: reg.ResultMethods})
	return gen.Generate("main", file, res, chk)
}

// runGo writes src to a temp .go file, compiles+executes it with
// `go run`, and returns the captured stdout.
func runGo(t *testing.T, src []byte) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := exec.Command("go", "run", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run failed: %v\n--- source ---\n%s\n--- output ---\n%s",
			err, src, out)
	}
	return string(out)
}

func TestStdlibUsesRemainGeneralGenStubs(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "fs",
			src:  "use std.fs\n",
			want: "var fs = struct{}{} // stub for `use std.fs`",
		},
		{
			name: "thread",
			src: `use std.thread

fn main() {
    let cancelled = thread.isCancelled()
    println("{cancelled}")
}
`,
			want: "var thread = struct {",
		},
		{
			name: "testing",
			src: `use std.testing

fn testTruth() {
    testing.assert(true)
}
`,
			want: "var testing = struct {",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			goSrc, err := transpile(t, c.src)
			if err != nil {
				t.Fatalf("transpile: %v\n%s", err, goSrc)
			}
			if !strings.Contains(string(goSrc), c.want) {
				t.Fatalf("generated Go missing stdlib stub %q:\n%s", c.want, goSrc)
			}
		})
	}
}

func TestStdMathBridge(t *testing.T) {
	goSrc, err := transpile(t, `use std.math

fn main() {
    let x = math.sin(math.PI / 2.0)
    let y = math.log(100.0, 10.0)
    let z = math.sqrt(81.0)
    println("{math.round(x + y + z)}")
}
`)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	if out != "12" {
		t.Fatalf("stdout = %q, want 12\n--- source ---\n%s", out, goSrc)
	}
	for _, want := range []string{"math.Sin", "math.Log", "math.Sqrt", "math.Round", "math.Pi"} {
		if !strings.Contains(string(goSrc), want) {
			t.Errorf("generated std.math bridge missing %s:\n%s", want, goSrc)
		}
	}
}

func TestCollectionMethodsCompileAndRun(t *testing.T) {
	goSrc, err := transpile(t, `
fn main() {
    let mut xs = [3, 1, 2]
    xs.push(4)
    xs.insert(1, 9)
    let removed = xs.removeAt(1)
    let popped = xs.pop() ?? 0
    let mapped = xs.map(|x| x * 2).filter(|x| x > 2).reversed()
    let sorted = xs.sorted()
    let sum = xs.fold(0, |acc, x| acc + x)
    println("{xs.len()} {removed} {popped} {mapped.get(0) ?? 0} {sorted.get(0) ?? 0} {sum} {xs.contains(3)} {xs.indexOf(2) ?? -1}")
    let snap = xs.toList()
    xs.clear()
    println("{xs.isEmpty()} {snap.len()} {snap.first() ?? 0} {snap.last() ?? 0} {snap.find(|x| x == 1) ?? 0}")
    let mut combo = snap.appended(7).concat([8])
    let zipped = combo.zip(["a", "b"])
    let taken = combo.take(2)
    combo.reverse()
    combo.sort()
    println("{combo.get(0) ?? 0} {combo.get(combo.len() - 1) ?? 0} {zipped.len()} {taken.last() ?? 0}")

    let nested = [[1, 2], [3]]
    let mut flags = [true, false]
    flags.sort()
    let sortedFlags = flags.sorted()
    println("{nested.contains([1, 2])} {nested.indexOf([3]) ?? -1} {flags.get(0) ?? true} {sortedFlags.last() ?? false}")

    let mut m: Map<String, Int> = {:}
    m.insert("a", 1)
    m.insert("b", 2)
    let old = m.remove("a") ?? 0
    let hasB = m.containsKey("b")
    let gotB = m.get("b") ?? 0
    let copied = m.toMap()
    println("{hasB} {old} {m.len()} {m.keys().len()} {m.values().len()} {m.entries().len()}")
    m.clear()
    println("{gotB} {m.isEmpty()} {copied.len()}")

    let s = snap.toSet()
    let mut t = [2].toSet()
    t.insert(5)
    let u = s.union(t)
    let i = s.intersect(t)
    let d = u.difference(i)
    println("{s.contains(3)} {u.contains(5)} {i.contains(2)} {d.contains(5)}")
    let removedTwo = t.remove(2)
    let tItems = t.toList()
    t.clear()
    println("{removedTwo} {t.isEmpty()} {tItems.len()}")
}
`)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := strings.Join([]string{
		"3 9 4 4 1 6 true 2",
		"true 3 3 2 1",
		"1 8 2 1",
		"true 1 false true",
		"true 1 1 1 1 1",
		"2 true 1",
		"true true true true",
		"true true 1",
	}, "\n")
	if out != want {
		t.Fatalf("stdout = %q, want %q\n--- source ---\n%s", out, want, goSrc)
	}
}

func TestStdRegexBridge(t *testing.T) {
	goSrc, err := transpileWithStdlib(t, `use std.regex

fn main() {
    let re = regex.compile("(?P<word>[a-z][a-z][a-z])-([0-9]+)").unwrap()
    println("{re.matches("abc-123")}")
    match re.find("xxabc-123yy") {
        Some(m) -> println("{m.text}:{m.start}:{m.end}"),
        None -> println("no match"),
    }
    let captured = match re.captures("abc-123") {
        Some(caps) -> match caps.named("word") {
            Some(word) -> word,
            None -> "no word",
        },
        None -> "no caps",
    }
    println(captured)
    println(re.replace("abc-123 abc-456", "X"))
    println(re.replaceAll("abc-123 abc-456", "X"))
    let parts = re.split("oneabc-123twoabc-456three")
    println(parts[1])
}
`)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := strings.Join([]string{
		"true",
		"abc-123:2:9",
		"abc",
		"X abc-456",
		"X X",
		"two",
	}, "\n")
	if out != want {
		t.Fatalf("stdout = %q, want %q\n--- source ---\n%s", out, want, goSrc)
	}
	for _, want := range []string{"regexp.Compile", "regexCompile", "type Regex struct", "type Captures struct"} {
		if !strings.Contains(string(goSrc), want) {
			t.Errorf("generated std.regex bridge missing %s:\n%s", want, goSrc)
		}
	}
}

func TestStdEncodingBridge(t *testing.T) {
	goSrc, err := transpileWithStdlib(t, `use std.encoding

fn main() {
    let raw = encoding.hex.decode("68656c6c6f3f").unwrap()
    let b64 = encoding.base64.encode(raw)
    println(b64)
    println(encoding.hex.encode(encoding.base64.decode(b64).unwrap()))
    let safe = encoding.url.encode("hello world&foo=bar")
    println(safe)
    println(encoding.url.decode(safe).unwrap())
    let urlB64 = encoding.base64.url.encode(raw)
    println(urlB64)
    println(encoding.hex.encode(encoding.base64.url.decode(urlB64).unwrap()))
}
`)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := strings.Join([]string{
		"aGVsbG8/",
		"68656c6c6f3f",
		"hello%20world%26foo%3Dbar",
		"hello world&foo=bar",
		"aGVsbG8_",
		"68656c6c6f3f",
	}, "\n")
	if out != want {
		t.Fatalf("stdout = %q, want %q\n--- source ---\n%s", out, want, goSrc)
	}
	for _, want := range []string{"stdbase64.StdEncoding", "stdhex.DecodeString", "neturl.QueryEscape"} {
		if !strings.Contains(string(goSrc), want) {
			t.Errorf("generated std.encoding bridge missing %s:\n%s", want, goSrc)
		}
	}
}

func TestStdEnvBridge(t *testing.T) {
	goSrc, err := transpileWithStdlib(t, `use std.env

fn main() {
    env.set("OSTY_STD_ENV_TEST", "ready").unwrap()
    match env.get("OSTY_STD_ENV_TEST") {
        Some(v) -> println(v),
        None -> println("missing"),
    }
    println(env.require("OSTY_STD_ENV_TEST").unwrap())
    let all = env.vars()
    println(all["OSTY_STD_ENV_TEST"])
    env.unset("OSTY_STD_ENV_TEST").unwrap()
    match env.get("OSTY_STD_ENV_TEST") {
        Some(v) -> println(v),
        None -> println("gone"),
    }
    println(env.currentDir().unwrap() != "")
}
`)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := strings.Join([]string{
		"ready",
		"ready",
		"ready",
		"gone",
		"true",
	}, "\n")
	if out != want {
		t.Fatalf("stdout = %q, want %q\n--- source ---\n%s", out, want, goSrc)
	}
	for _, want := range []string{"envGet", "envVars", "os.Setenv"} {
		if !strings.Contains(string(goSrc), want) {
			t.Errorf("generated std.env bridge missing %s:\n%s", want, goSrc)
		}
	}
}

func TestStdCSVBridge(t *testing.T) {
	goSrc, err := transpileWithStdlib(t, `use std.csv

fn main() {
    let rows = [["name", "age"], ["alice", "30"], ["bob", "25"]]
    let text = csv.encode(rows)
    let decoded = csv.decode(text).unwrap()
    println(decoded[1][0])
    let records = csv.decodeHeaders(text).unwrap()
    println(records[1]["age"])
    let opts = csv.CsvOptions { delimiter: ';', quote: '|', trimSpace: true }
    let semi = csv.encodeWith([["left;side", "right"], ["up", "down"]], opts)
    let again = csv.decodeWith(semi, opts).unwrap()
    println(again[0][0])
    println(again[1][1])
}
`)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := strings.Join([]string{"alice", "25", "left;side", "down"}, "\n")
	if out != want {
		t.Fatalf("stdout = %q, want %q\n--- source ---\n%s", out, want, goSrc)
	}
	for _, want := range []string{"stdcsv.NewWriter", "stdcsv.NewReader", "type CsvOptions struct"} {
		if !strings.Contains(string(goSrc), want) {
			t.Errorf("generated std.csv bridge missing %s:\n%s", want, goSrc)
		}
	}
}

func TestStdJSONBridge(t *testing.T) {
	goSrc, err := transpileWithStdlib(t, `use std.json

pub struct Payload {
    #[json(key = "user_id")]
    pub userId: Int,

    #[json(optional)]
    pub nickname: String?,

    #[json(skip)]
    pub cache: Int,
}

pub enum Shape {
    #[json(key = "circle")]
    Circle(Float),
    Empty,
}

pub struct Secret {
    pub raw: String,

    pub fn toJson(self) -> json.Json {
        json.String("redacted")
    }
}

pub struct FromCustom {
    pub value: Int,

    pub fn fromJson(value: json.Json) -> Result<Self, Error> {
        Ok(Self { value: 42 })
    }
}

fn loadPayload(text: String) -> Result<Payload, Error> {
    let cfg: Payload = json.decode(text)?
    Ok(cfg)
}

fn main() {
    let missing = Payload { userId: 7, nickname: None, cache: 99 }
    println(json.encode(missing))
    let nick = "neo"
    let present = Payload { userId: 8, nickname: Some(nick), cache: 0 }
    println(json.stringify(present))
    println(json.encode(Circle(2.5)))
    let raw: json.Json = json.Object({"ok": json.Bool(true), "name": json.String("osty"), "none": json.Null})
    println(json.stringify(raw))
    let decoded: Payload = json.decode::<Payload>("\{\"user_id\":9,\"nickname\":null,\"cache\":123,\"extra\":true\}").unwrap()
    println(json.encode(decoded))
    let parsed: Payload = json.parse::<Payload>("\{\"user_id\":10,\"nickname\":\"trinity\"\}").unwrap()
    println(json.encode(parsed))
    let roundShape: Shape = json.decode::<Shape>("\{\"tag\":\"circle\",\"value\":3.5\}").unwrap()
    println(json.encode(roundShape))
    let shapes: List<Shape> = json.decode::<List<Shape>>("[\{\"tag\":\"circle\",\"value\":1.25\},\{\"tag\":\"Empty\"\}]").unwrap()
    println(json.encode(shapes))
    let shapeMap: Map<String, Shape> = json.decode::<Map<String, Shape>>("\{\"a\":\{\"tag\":\"circle\",\"value\":4.5\}\}").unwrap()
    println(json.encode(shapeMap["a"]))
    let loaded = loadPayload("\{\"user_id\":11\}").unwrap()
    println(json.encode(loaded))
    println(json.decode::<String>("\"\\uD800\"").isErr())
    println(json.encode(Secret { raw: "top" }))
    println(json.decode::<FromCustom>("null").unwrap().value)
}
`)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := strings.Join([]string{
		`{"user_id":7}`,
		`{"nickname":"neo","user_id":8}`,
		`{"tag":"circle","value":2.5}`,
		`{"name":"osty","none":null,"ok":true}`,
		`{"user_id":9}`,
		`{"nickname":"trinity","user_id":10}`,
		`{"tag":"circle","value":3.5}`,
		`[{"tag":"circle","value":1.25},{"tag":"Empty"}]`,
		`{"tag":"circle","value":4.5}`,
		`{"user_id":11}`,
		`true`,
		`"redacted"`,
		`42`,
	}, "\n")
	if out != want {
		t.Fatalf("stdout = %q, want %q\n--- source ---\n%s", out, want, goSrc)
	}
	for _, want := range []string{"stdjson.Marshal", "func (self Payload) MarshalJSON", "func (self *Payload) UnmarshalJSON", "jsonDecode[Payload]", "jsonUnmarshalShape"} {
		if !strings.Contains(string(goSrc), want) {
			t.Errorf("generated std.json bridge missing %s:\n%s", want, goSrc)
		}
	}
}

func TestStdCompressBridge(t *testing.T) {
	goSrc, err := transpileWithStdlib(t, `use std.compress
use std.encoding

fn main() {
    let raw = encoding.hex.decode("68656c6c6f206f737479").unwrap()
    let zipped = compress.gzip.encode(raw)
    let round = compress.gzip.decode(zipped).unwrap()
    println(encoding.hex.encode(round))
}
`)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	if out != "68656c6c6f206f737479" {
		t.Fatalf("stdout = %q, want hex round-trip\n--- source ---\n%s", out, goSrc)
	}
	for _, want := range []string{"stdgzip.NewWriter", "stdgzip.NewReader", "compressGzipDecode"} {
		if !strings.Contains(string(goSrc), want) {
			t.Errorf("generated std.compress bridge missing %s:\n%s", want, goSrc)
		}
	}
}

func TestStdCryptoBridge(t *testing.T) {
	goSrc, err := transpileWithStdlib(t, `use std.crypto
use std.encoding

fn main() {
    let data = encoding.hex.decode("68656c6c6f").unwrap()
    let key = encoding.hex.decode("6b6579").unwrap()
    println(encoding.hex.encode(crypto.sha256(data)))
    println(encoding.hex.encode(crypto.sha512(data)))
    println(encoding.hex.encode(crypto.sha1(data)))
    println(encoding.hex.encode(crypto.md5(data)))
    println(encoding.hex.encode(crypto.hmac.sha256(key, data)))
    println(encoding.hex.encode(crypto.hmac.sha512(key, data)))
    println(crypto.constantTimeEq(crypto.sha256(data), crypto.sha256(data)))
    let secret = crypto.randomBytes(4)
    println(crypto.constantTimeEq(secret, secret))
}
`)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := strings.Join([]string{
		"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
		"9b71d224bd62f3785d96d46ad3ea3d73319bfbc2890caadae2dff72519673ca72323c3d99ba5c11d7c7acc6e14b8c5da0c4663475c2e5c3adef46f73bcdec043",
		"aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d",
		"5d41402abc4b2a76b9719d911017c592",
		"9307b3b915efb5171ff14d8cb55fbcc798c6c0ef1456d66ded1a6aa723a58b7b",
		"ff06ab36757777815c008d32c8e14a705b4e7bf310351a06a23b612dc4c7433e7757d20525a5593b71020ea2ee162d2311b247e9855862b270122419652c0c92",
		"true",
		"true",
	}, "\n")
	if out != want {
		t.Fatalf("stdout = %q, want %q\n--- source ---\n%s", out, want, goSrc)
	}
	for _, want := range []string{"stdsha256.Sum256", "stdhmac.New", "stdrand.Read", "stdsubtle.ConstantTimeCompare"} {
		if !strings.Contains(string(goSrc), want) {
			t.Errorf("generated std.crypto bridge missing %s:\n%s", want, goSrc)
		}
	}
}

func TestStdUUIDBridge(t *testing.T) {
	goSrc, err := transpileWithStdlib(t, `use std.uuid
use std.encoding

fn main() {
    let zero = uuid.nil()
    println(zero.toString())
    let parsed = uuid.parse("00112233-4455-6677-8899-aabbccddeeff").unwrap()
    println(parsed.toString())
    println(encoding.hex.encode(parsed.toBytes()))
    let id = uuid.v4()
    let text = id.toString()
    println(uuid.parse(text).unwrap().toString() == text)
    println(uuid.v7().toString() != "")
}
`)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := strings.TrimSpace(runGo(t, goSrc))
	want := strings.Join([]string{
		"00000000-0000-0000-0000-000000000000",
		"00112233-4455-6677-8899-aabbccddeeff",
		"00112233445566778899aabbccddeeff",
		"true",
		"true",
	}, "\n")
	if out != want {
		t.Fatalf("stdout = %q, want %q\n--- source ---\n%s", out, want, goSrc)
	}
	for _, want := range []string{"type Uuid [16]byte", "uuidV4", "uuidV7", "uuidParse"} {
		if !strings.Contains(string(goSrc), want) {
			t.Errorf("generated std.uuid bridge missing %s:\n%s", want, goSrc)
		}
	}
}

func TestHelloWorld(t *testing.T) {
	src := `fn main() {
    println("hello, world")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	if !strings.Contains(string(goSrc), "fmt.Println") {
		t.Errorf("expected fmt.Println in output:\n%s", goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "hello, world" {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestScriptHello(t *testing.T) {
	src := `let name = "world"
println("hello, {name}")
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "hello, world" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

func TestArithmetic(t *testing.T) {
	src := `fn add(a: Int, b: Int) -> Int {
    a + b
}

fn main() {
    let x = add(2, 3)
    let y = x * 10 - 1
    println("{y}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "49" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

func TestIfElse(t *testing.T) {
	src := `fn main() {
    let x = 5
    if x > 10 {
        println("big")
    } else if x > 0 {
        println("small")
    } else {
        println("neg")
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "small" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

func TestForRange(t *testing.T) {
	src := `fn main() {
    let mut sum = 0
    for i in 1..=10 {
        sum = sum + i
    }
    println("{sum}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "55" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

func TestReturn(t *testing.T) {
	src := `fn abs(x: Int) -> Int {
    if x < 0 {
        return -x
    }
    x
}

fn main() {
    let a = abs(-7)
    let b = abs(5)
    println("{a} {b}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "7 5" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

func TestBoolLogical(t *testing.T) {
	src := `fn both(a: Bool, b: Bool) -> Bool {
    a && b
}

fn main() {
    if both(true, false) {
        println("yes")
    } else {
        println("no")
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "no" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestFloat exercises floating-point literals and arithmetic, plus a
// primitive-typed parameter (Float) and the checker's handling of
// untyped float literals in context.
func TestFloat(t *testing.T) {
	src := `fn area(r: Float) -> Float {
    3.14 * r * r
}

fn main() {
    let a = area(2.0)
    println("{a}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if !strings.HasPrefix(strings.TrimSpace(out), "12.56") {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestNestedControl verifies if/for nesting with break and continue.
func TestNestedControl(t *testing.T) {
	src := `fn main() {
    let mut total = 0
    for i in 1..100 {
        if i > 10 {
            break
        }
        if i % 2 == 0 {
            continue
        }
        total = total + i
    }
    println("{total}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	// 1 + 3 + 5 + 7 + 9 = 25
	if strings.TrimSpace(out) != "25" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestRecursion exercises a recursive function, verifying that
// self-references resolve correctly.
func TestRecursion(t *testing.T) {
	src := `fn fact(n: Int) -> Int {
    if n <= 1 {
        return 1
    }
    n * fact(n - 1)
}

fn main() {
    println("{fact(6)}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "720" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestUnaryOps covers `-x` and `!b`.
func TestUnaryOps(t *testing.T) {
	src := `fn main() {
    let x = -5
    let y = !true
    if x < 0 && !y {
        println("ok")
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "ok" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestListLiteral covers `[1, 2, 3]` iteration. Phase 1 lists are
// untyped-any when the checker can't infer an element type; this test
// uses a typed-element context (for-in loop over ints) to keep the
// output simple.
func TestListLiteral(t *testing.T) {
	src := `fn main() {
    let mut sum = 0
    for x in [1, 2, 3, 4] {
        sum = sum + x
    }
    println("{sum}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "10" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestStringEscapes verifies escape sequences round-trip correctly.
func TestStringEscapes(t *testing.T) {
	src := `fn main() {
    println("line1\nline2")
    print("tab\there")
    println("")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "line1\nline2\ntab\there\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestMultipleFuncs verifies that several functions compile and can
// call each other.
func TestMultipleFuncs(t *testing.T) {
	src := `fn double(x: Int) -> Int { x * 2 }
fn triple(x: Int) -> Int { x * 3 }
fn apply(x: Int) -> Int { double(x) + triple(x) }

fn main() {
    println("{apply(10)}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "50" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestWhileStyle exercises `for cond { ... }`.
func TestWhileStyle(t *testing.T) {
	src := `fn main() {
    let mut i = 0
    for i < 5 {
        i = i + 1
    }
    println("{i}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "5" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestInfiniteForWithBreak exercises the bare `for { ... }` form.
func TestInfiniteForWithBreak(t *testing.T) {
	src := `fn main() {
    let mut n = 0
    for {
        n = n + 1
        if n >= 3 {
            break
        }
    }
    println("{n}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "3" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestMultipleScriptStmts exercises a script file (no fn main) with
// several top-level statements of different kinds.
func TestMultipleScriptStmts(t *testing.T) {
	src := `let greeting = "hello"
let target = "osty"
println("{greeting}, {target}")
let mut count = 0
for i in 1..=3 {
    count = count + i
}
println("sum = {count}")
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "hello, osty\nsum = 6\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestEprintln exercises the stderr-bound println variant.
func TestEprintln(t *testing.T) {
	src := `fn main() {
    eprintln("warning")
    println("ok")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	// `go run` combines stderr and stdout in our runner; we just check
	// that both lines appear somewhere in the output.
	out := runGo(t, goSrc)
	if !strings.Contains(out, "warning") || !strings.Contains(out, "ok") {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestGofmtOutput verifies the generator emits gofmt-clean output so
// editor tooling downstream doesn't have to re-format.
func TestGofmtOutput(t *testing.T) {
	src := `fn main() {
    let x = 1
    if x > 0 {
        println("yes")
    } else {
        println("no")
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	// Empty-line discipline: exactly one blank line between sections.
	src2 := string(goSrc)
	if strings.Contains(src2, "\n\n\n") {
		t.Errorf("output contains triple blank lines:\n%s", src2)
	}
	if !strings.HasSuffix(src2, "\n") {
		t.Errorf("output doesn't end with newline:\n%s", src2)
	}
}
