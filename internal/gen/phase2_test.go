package gen_test

import (
	"strings"
	"testing"
)

// TestStructFieldsAndLit verifies a struct with typed fields and the
// `Point { ... }` literal form.
func TestStructFieldsAndLit(t *testing.T) {
	src := `struct Point {
    x: Int,
    y: Int,
}

fn main() {
    let p = Point { x: 3, y: 4 }
    println("{p.x} {p.y}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "3 4" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestStructMethod verifies an instance method with `self` and a
// non-unit return value.
func TestStructMethod(t *testing.T) {
	src := `struct Point {
    x: Int,
    y: Int,

    fn sum(self) -> Int {
        self.x + self.y
    }
}

fn main() {
    let p = Point { x: 5, y: 7 }
    println("{p.sum()}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "12" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestStructStaticMethod verifies an associated function (no receiver)
// called as TypeName.name(...).
func TestStructStaticMethod(t *testing.T) {
	src := `struct User {
    name: String,

    fn new(name: String) -> Self {
        Self { name: name }
    }
}

fn main() {
    let u = User.new("alice")
    println("{u.name}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "alice" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestTypeAlias verifies a non-generic type alias.
func TestTypeAlias(t *testing.T) {
	src := `type Pair = (Int, Int)

fn main() {
    let a: Int = 3
    println("{a}")
}
`
	// Tuples aren't fully supported yet so we don't use one here — we
	// just verify that the alias itself compiles.
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	if !strings.Contains(string(goSrc), "type Pair") {
		t.Errorf("expected type alias in output:\n%s", goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "3" {
		t.Errorf("unexpected output: %q", out)
	}
}

// TestEnumBareVariant verifies unit-variant construction and equality.
func TestEnumBareVariant(t *testing.T) {
	src := `enum Status {
    Active,
    Inactive,
}

fn main() {
    let s = Active
    match s {
        Active -> println("on"),
        Inactive -> println("off"),
    }
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "on" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestEnumTupleVariant verifies variants with payload fields, match
// binding via VariantPat, and an arm body that uses the bindings.
func TestEnumTupleVariant(t *testing.T) {
	src := `enum Shape {
    Circle(Float),
    Rect(Float, Float),
    Empty,
}

fn area(s: Shape) -> Float {
    match s {
        Circle(r) -> 3.14 * r * r,
        Rect(w, h) -> w * h,
        Empty -> 0.0,
    }
}

fn main() {
    let c = Circle(2.0)
    let r = Rect(3.0, 4.0)
    println("{area(c)} {area(r)} {area(Empty)}")
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
	if !strings.Contains(out, " 12 ") {
		t.Errorf("expected rect area 12 in output: %q", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), " 0") {
		t.Errorf("expected Empty area 0 in output: %q", out)
	}
}

// TestMatchGuard verifies `if guard` on a match arm.
func TestMatchGuard(t *testing.T) {
	src := `fn classify(n: Int) -> String {
    match n {
        x if x < 0 -> "negative",
        0 -> "zero",
        x if x < 10 -> "single-digit",
        _ -> "large",
    }
}

fn main() {
    println(classify(-5))
    println(classify(0))
    println(classify(7))
    println(classify(42))
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "negative\nzero\nsingle-digit\nlarge\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestIfAsExpression verifies if/else used in value position.
func TestIfAsExpression(t *testing.T) {
	src := `fn sign(n: Int) -> Int {
    if n > 0 {
        1
    } else if n < 0 {
        -1
    } else {
        0
    }
}

fn main() {
    println("{sign(-5)} {sign(0)} {sign(7)}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "-1 0 1" {
		t.Errorf("unexpected output: %q\n--- src ---\n%s", out, goSrc)
	}
}

// TestOrPattern verifies `Pat1 | Pat2` arms.
func TestOrPattern(t *testing.T) {
	src := `fn is_vowel(c: Char) -> Bool {
    match c {
        'a' | 'e' | 'i' | 'o' | 'u' -> true,
        _ -> false,
    }
}

fn main() {
    if is_vowel('e') && !is_vowel('z') {
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

// TestRangePattern verifies `x @ 1..=9 -> ...` style arms.
func TestRangePattern(t *testing.T) {
	src := `fn describe(n: Int) -> String {
    match n {
        0 -> "zero",
        x @ 1..=9 -> "single",
        x @ 10..=99 -> "double",
        _ -> "big",
    }
}

fn main() {
    println(describe(0))
    println(describe(5))
    println(describe(55))
    println(describe(555))
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	want := "zero\nsingle\ndouble\nbig\n"
	if out != want {
		t.Errorf("got %q, want %q\n--- src ---\n%s", out, want, goSrc)
	}
}

// TestMutSelf verifies a pointer-receiver method (`mut self`) that
// modifies the struct in place.
func TestMutSelf(t *testing.T) {
	src := `struct Counter {
    n: Int,

    fn bump(mut self) {
        self.n = self.n + 1
    }
}

fn main() {
    let mut c = Counter { n: 0 }
    c.bump()
    c.bump()
    c.bump()
    println("{c.n}")
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
