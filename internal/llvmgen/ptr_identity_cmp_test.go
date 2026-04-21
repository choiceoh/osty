package llvmgen

import (
	"strings"
	"testing"
)

// Identity comparison — `==` / `!=` on two non-String managed pointers
// (lists, maps, structs held via ptr). Previously the backend rejected
// every non-String ptr compare with LLVM011, which is what the native
// toolchain probe first walled on after the List.insert / String method
// dispatch landed. Identity semantics match every GC language's
// pointer-equality convention and are the only sane meaning without
// structural equality wired in.
func TestPtrIdentityEqList(t *testing.T) {
	file := parseLLVMGenFile(t, `fn sameList(a: List<Int>, b: List<Int>) -> Bool {
    a == b
}

fn main() {
    let xs: List<Int> = [1, 2, 3]
    println(sameList(xs, xs))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/ptr_cmp_eq.osty"})
	if err != nil {
		t.Fatalf("ptr == errored: %v", err)
	}
	if got := string(ir); !strings.Contains(got, "icmp eq ptr") {
		t.Fatalf("ptr == did not lower to icmp eq ptr:\n%s", got)
	}
}

func TestPtrIdentityNeList(t *testing.T) {
	file := parseLLVMGenFile(t, `fn diff(a: List<Int>, b: List<Int>) -> Bool {
    a != b
}

fn main() {
    let xs: List<Int> = [1]
    let ys: List<Int> = [2]
    println(diff(xs, ys))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/ptr_cmp_ne.osty"})
	if err != nil {
		t.Fatalf("ptr != errored: %v", err)
	}
	if got := string(ir); !strings.Contains(got, "icmp ne ptr") {
		t.Fatalf("ptr != did not lower to icmp ne ptr:\n%s", got)
	}
}

// Ordering ops still reject — identity doesn't have a <-ordering, and
// structural ordering isn't wired in yet. Lock this in so a future
// refactor doesn't accidentally silently allow nonsense compares.
func TestPtrOrderingStillRejected(t *testing.T) {
	file := parseLLVMGenFile(t, `fn less(a: List<Int>, b: List<Int>) -> Bool {
    a < b
}

fn main() {
    let xs: List<Int> = []
    println(less(xs, xs))
}
`)
	_, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/ptr_cmp_lt.osty"})
	if err == nil {
		t.Fatalf("expected LLVM011 wall for `<` on non-String ptr; got nil")
	}
	if !strings.Contains(err.Error(), "LLVM011") {
		t.Fatalf("expected LLVM011 wall; got: %v", err)
	}
}
