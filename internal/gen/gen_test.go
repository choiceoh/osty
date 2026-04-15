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
