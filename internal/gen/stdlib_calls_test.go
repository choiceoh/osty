package gen_test

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/gen"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// transpileWithStdlib runs the pipeline with the stdlib registry attached
// so `use std.math` et al. resolve to real module signatures.
func transpileWithStdlib(t *testing.T, src string) ([]byte, error) {
	t.Helper()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	for _, d := range parseDiags {
		if d.Severity.String() == "error" {
			t.Fatalf("parse error: %s", d.Message)
		}
	}
	reg := stdlib.LoadCached()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	chk := check.File(file, res, check.Opts{Primitives: reg.Primitives})
	return gen.Generate("main", file, res, chk)
}

// TestStdMathBasic exercises the std.math call-site rewrites: simple
// trig, sqrt, and the constant PI all route to Go's math package with
// capitalised function names and math.Pi for the constant.
func TestStdMathBasic(t *testing.T) {
	src := `use std.math

fn main() {
    let s = math.sqrt(16.0)
    let two = math.cos(0.0) + math.sin(0.0)
    let pi = math.PI
    println("{s} {two} {pi}")
}
`
	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	str := string(goSrc)
	for _, want := range []string{"math.Sqrt(", "math.Cos(", "math.Sin(", "math.Pi"} {
		if !strings.Contains(str, want) {
			t.Errorf("output missing %q:\n%s", want, str)
		}
	}
	out := runGo(t, goSrc)
	if !strings.HasPrefix(strings.TrimSpace(out), "4 1 3.14159") {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestStdMathLog covers the two-argument `log(x, base)` that divides
// natural logs.
func TestStdMathLog(t *testing.T) {
	src := `use std.math

fn main() {
    let a = math.log(100.0, 10.0)
    let b = math.log(2.718281828459045)
    println("{a} {b}")
}
`
	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	parts := strings.Fields(strings.TrimSpace(out))
	if len(parts) != 2 {
		t.Fatalf("expected two numbers, got %q", out)
	}
	if !strings.HasPrefix(parts[0], "2") {
		t.Errorf("log(100,10) = %q, want ~2", parts[0])
	}
	if !strings.HasPrefix(parts[1], "1") {
		t.Errorf("log(e) = %q, want ~1", parts[1])
	}
}

// TestStdEnv covers env.get / env.args rewrites.
func TestStdEnv(t *testing.T) {
	src := `use std.env

fn main() {
    let args = env.args()
    env.set("OSTY_GEN_TEST_VAR", "hello")
    let v = env.get("OSTY_GEN_TEST_VAR")
    println("{v}")
}
`
	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	str := string(goSrc)
	for _, want := range []string{"os.Args", "os.Setenv(", "os.Getenv("} {
		if !strings.Contains(str, want) {
			t.Errorf("output missing %q:\n%s", want, str)
		}
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "hello" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestStdStrings exercises the strings.* rewrites — every function in
// the Tier 1 stub gets one call site so a regression in any entry
// surfaces here.
func TestStdStrings(t *testing.T) {
	src := `use std.strings

fn main() {
    let parts = strings.split("a,b,c", ",")
    let joined = strings.join(parts, "-")
    let up = strings.toUpper("hi")
    let down = strings.toLower("HI")
    let trimmed = strings.trim("  padded  ")
    let ls = strings.trimStart("  left")
    let rs = strings.trimEnd("right  ")
    let has = strings.contains("foobar", "oba")
    let sw = strings.startsWith("foobar", "foo")
    let ew = strings.endsWith("foobar", "bar")
    let rep = strings.repeat("ab", 3)
    let replaced = strings.replace("a-b-c", "-", "_")
    println("{joined} {up} {down} {trimmed}|{ls}|{rs}| {has} {sw} {ew} {rep} {replaced}")
}
`
	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "a-b-c HI hi padded|left|right| true true true ababab a_b_c"
	if strings.TrimSpace(out) != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestStdFsRoundTrip covers the fs.writeString → fs.readToString →
// fs.exists → fs.remove cycle end-to-end, including `?` propagation
// through a Result-returning helper.
func TestStdFsRoundTrip(t *testing.T) {
	src := `use std.fs

fn run(path: String) -> Result<String, Error> {
    fs.writeString(path, "payload")?
    let data = fs.readToString(path)?
    Ok(data)
}

fn main() {
    let path = "/tmp/osty_std_fs_test.txt"
    let r = run(path)
    match r {
        Ok(s) -> println("read={s}"),
        Err(_) -> println("err"),
    }
    let present = fs.exists(path)
    println("exists={present}")
    match fs.remove(path) {
        Ok(_) -> println("removed"),
        Err(_) -> println("remove-err"),
    }
    let stillThere = fs.exists(path)
    println("exists={stillThere}")
}
`
	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "read=payload\nexists=true\nremoved\nexists=false\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}
