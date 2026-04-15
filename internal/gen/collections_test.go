package gen_test

import (
	"strings"
	"testing"
)

// TestListPure exercises the §10.6 non-mutating List methods end-to-end
// through the gen → go run pipeline.
func TestListPure(t *testing.T) {
	src := `fn main() {
    let xs: List<Int> = [3, 1, 4, 1, 5, 9, 2, 6]
    let doubled = xs.map(|x| x * 2)
    let evens = xs.filter(|x| x % 2 == 0)
    let sum = xs.fold(0, |acc, x| acc + x)
    let rev = xs.reversed()
    let sorted = xs.sorted()
    let appended = xs.appended(7)
    let concat = xs.concat([10, 20])

    println("{xs.len()} {doubled.len()} {evens.len()} {sum}")
    println("{rev.first() ?? -1} {sorted.first() ?? -1} {appended.last() ?? -1} {concat.len()}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 output lines, got %d:\n%s", len(lines), out)
	}
	if lines[0] != "8 8 3 31" {
		t.Errorf("line 1 mismatch: %q\n--- gen ---\n%s", lines[0], goSrc)
	}
	if lines[1] != "6 1 7 10" {
		t.Errorf("line 2 mismatch: %q\n--- gen ---\n%s", lines[1], goSrc)
	}
}

// TestListMutating exercises push, pop, insert, removeAt, clear,
// reverse, sort — all of which must lower to a pointer-taking runtime
// helper so the caller's slice header is updated in place.
func TestListMutating(t *testing.T) {
	src := `fn main() {
    let mut xs: List<Int> = []
    xs.push(10)
    xs.push(20)
    xs.push(30)
    let popped = xs.pop() ?? -1
    xs.insert(0, 5)
    let removed = xs.removeAt(1)
    xs.reverse()
    println("{popped} {removed} {xs.len()} {xs.first() ?? -1} {xs.last() ?? -1}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "30 10 2 20 5" {
		t.Errorf("unexpected output: %q\n--- gen ---\n%s", out, goSrc)
	}
}

// TestListContainsIndexFind exercises contains/indexOf/find.
func TestListContainsIndexFind(t *testing.T) {
	src := `fn main() {
    let xs: List<Int> = [10, 20, 30, 40]
    let hasTwenty = xs.contains(20)
    let idx = xs.indexOf(30) ?? -1
    let found = xs.find(|x| x > 25) ?? -1
    println("{hasTwenty} {idx} {found}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "true 2 30" {
		t.Errorf("unexpected output: %q\n--- gen ---\n%s", out, goSrc)
	}
}

// TestMapMethods exercises get/containsKey/insert/remove/keys/values.
func TestMapMethods(t *testing.T) {
	src := `fn main() {
    let mut m: Map<String, Int> = {"a": 1, "b": 2}
    m.insert("c", 3)
    let a = m.get("a") ?? -1
    let missing = m.get("z") ?? -1
    let hasB = m.containsKey("b")
    let removed = m.remove("a") ?? -1
    println("{m.len()} {a} {missing} {hasB} {removed}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "2 1 -1 true 1" {
		t.Errorf("unexpected output: %q\n--- gen ---\n%s", out, goSrc)
	}
}

// TestSortedBy exercises the keyed sort lowering.
func TestSortedBy(t *testing.T) {
	src := `fn main() {
    let xs: List<Int> = [3, 1, 4, 1, 5]
    let byNeg = xs.sortedBy(|x| 0 - x)
    println("{byNeg.first() ?? -1} {byNeg.last() ?? -1}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "5 1" {
		t.Errorf("unexpected output: %q\n--- gen ---\n%s", out, goSrc)
	}
}

// TestEnumerate exercises List<T>.enumerate().
func TestEnumerate(t *testing.T) {
	src := `fn main() {
    let xs: List<Int> = [10, 20, 30]
    let mut sum = 0
    for (i, v) in xs.enumerate() {
        sum = sum + i * v
    }
    println("{sum}")
}
`
	goSrc, err := transpile(t, src)
	if err != nil {
		t.Fatalf("transpile: %v\n%s", err, goSrc)
	}
	out := runGo(t, goSrc)
	if strings.TrimSpace(out) != "80" {
		t.Errorf("unexpected output: %q\n--- gen ---\n%s", out, goSrc)
	}
}
