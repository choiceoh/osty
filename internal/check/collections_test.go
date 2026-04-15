package check_test

import (
	"testing"
)

// TestCheck_ListMethodsAccept verifies the expanded §10.6 method
// dispatch: every method name declared on List<T> must type-check
// (no `method not found` errors) when called with spec-compatible
// argument shapes.
func TestCheck_ListMethodsAccept(t *testing.T) {
	src := `
fn main() {
    let mut xs: List<Int> = [1, 2, 3]
    let _ = xs.len()
    let _ = xs.isEmpty()
    let _ = xs.first()
    let _ = xs.last()
    let _ = xs.get(0)
    let _ = xs.contains(2)
    let _ = xs.indexOf(2)
    let _ = xs.find(|x| x > 1)
    let _ = xs.map(|x| x + 1)
    let _ = xs.filter(|x| x > 0)
    let _ = xs.fold(0, |a, x| a + x)
    let _ = xs.sorted()
    let _ = xs.sortedBy(|x| x)
    let _ = xs.reversed()
    let _ = xs.appended(9)
    let _ = xs.concat([4, 5])
    let _ = xs.zip([10, 20, 30])
    let _ = xs.enumerate()
    xs.push(7)
    let _ = xs.pop()
    xs.insert(0, 99)
    let _ = xs.removeAt(1)
    xs.sort()
    xs.reverse()
    xs.clear()
}
`
	assertOK(t, runCheck(t, src))
}

// TestCheck_MapMethodsAccept verifies Map<K,V> method surface.
func TestCheck_MapMethodsAccept(t *testing.T) {
	src := `
fn main() {
    let mut m: Map<String, Int> = {"a": 1}
    let _ = m.len()
    let _ = m.isEmpty()
    let _ = m.get("a")
    let _ = m.containsKey("a")
    let _ = m.keys()
    let _ = m.values()
    let _ = m.entries()
    m.insert("b", 2)
    let _ = m.remove("a")
    m.clear()
}
`
	assertOK(t, runCheck(t, src))
}

// TestCheck_SetMethodsAccept verifies Set<T> method surface.
func TestCheck_SetMethodsAccept(t *testing.T) {
	src := `
fn main() {
    let mut a: Set<Int> = [1, 2, 3].toSet()
    let b: Set<Int> = [2, 3, 4].toSet()
    let _ = a.len()
    let _ = a.isEmpty()
    let _ = a.contains(1)
    let _ = a.union(b)
    let _ = a.intersect(b)
    let _ = a.difference(b)
    a.insert(4)
    let _ = a.remove(1)
    a.clear()
}
`
	assertOK(t, runCheck(t, src))
}
