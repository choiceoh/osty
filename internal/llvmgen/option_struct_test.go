package llvmgen

import (
	"strings"
	"testing"
)

// Option<Struct> — `Some(StructLit { ... })` with an aggregate payload
// used to wall LLVM011 "Some payload type %T requires boxed Option;
// only ptr-backed Some(...) is lowered". The toolchain's
// crossCompileGuard in runner.osty hit this on the native probe.
//
// The backend now boxes struct-typed payloads into a GC-managed heap
// cell (osty.gc.alloc_v1 + store) so None stays null-ptr and Some(s)
// lowers to a managed ptr. None and `?`-based presence checks keep
// working unchanged.
func TestOptionStructSomeLowersToGcAlloc(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Diag {
    severity: String,
    message: String,
}

fn make(ok: Bool) -> Diag? {
    if ok {
        None
    } else {
        Some(Diag { severity: "error", message: "boom" })
    }
}

fn main() {
    let _ = make(true)
    let _ = make(false)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/option_struct.osty"})
	if err != nil {
		t.Fatalf("Some<struct> errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call ptr @osty.gc.alloc_v1",
		"declare ptr @osty.gc.alloc_v1(i64, i64, ptr)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Some<struct> did not invoke osty.gc.alloc_v1: missing %q:\n%s", want, got)
		}
	}
	// The box store for the struct value — one insertion per field
	// goes through `insertvalue` first, then the final struct value is
	// stored into the box via `store %Diag ...`.
	if !strings.Contains(got, "store %Diag ") {
		t.Fatalf("Some<struct> did not store struct bytes into box:\n%s", got)
	}
}

// None on an Option<Struct> context stays a null ptr; the aggregate
// path shouldn't fire on the None branch. Otherwise presence checks
// `x != None` / `?` propagation would see a bogus allocation.
func TestOptionStructNoneStaysNull(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Diag {
    message: String,
}

fn empty() -> Diag? {
    None
}

fn main() {
    let _ = empty()
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/option_struct_none.osty"})
	if err != nil {
		t.Fatalf("None<struct> errored: %v", err)
	}
	got := string(ir)
	// No actual gc.alloc CALL for the None path — the module still
	// declares the symbol (declare ptr @...) but must not invoke it.
	if strings.Contains(got, "call ptr @osty.gc.alloc_v1") {
		t.Fatalf("None<struct> unexpectedly emitted gc.alloc_v1 call:\n%s", got)
	}
	if !strings.Contains(got, "ret ptr null") {
		t.Fatalf("None<struct> did not return null ptr:\n%s", got)
	}
}
