package llvmgen

import (
	"strings"
	"testing"
)

// Nested field assignment — the toolchain uses this pattern extensively
// (`cx.env.returnTy = sig.retTy`, `module.semantic.invalidScopes = …`)
// and it previously hit `LLVM012 field assignment base *ast.FieldExpr`.
// The lowering walks the chain inside-out, extracting each intermediate
// struct and rebuilding with insertvalue from innermost outward.
func TestNestedFieldAssignTwoLevels(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Inner {
    value: Int,
}

struct Outer {
    inner: Inner,
    flag: Int,
}

fn main() {
    let mut o: Outer = Outer { inner: Inner { value: 0 }, flag: 0 }
    o.inner.value = 42
    println(o.inner.value)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/nested_field_assign.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	// The rebuild should do: extractvalue to get inner, insertvalue to
	// update value, insertvalue to repack outer, then store.
	for _, want := range []string{
		"extractvalue",
		"insertvalue",
		"store %Outer",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// Three-level nesting exercises the general recursion: the inside-out
// rebuild loop has to handle every intermediate level, not just one.
func TestNestedFieldAssignThreeLevels(t *testing.T) {
	file := parseLLVMGenFile(t, `struct C {
    x: Int,
}

struct B {
    c: C,
}

struct A {
    b: B,
}

fn main() {
    let mut a: A = A { b: B { c: C { x: 0 } } }
    a.b.c.x = 7
    println(a.b.c.x)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/nested_three.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	// Need at least two extractvalue (for two intermediate levels) and
	// three insertvalue (innermost + two rebuilds) from the chain
	// rewrite, though the counting could change with any insertvalue/
	// extractvalue the broader codegen adds. Just check the expected
	// `store %A` lands.
	if !strings.Contains(got, "store %A") {
		t.Fatalf("expected final store %%A after rebuild chain:\n%s", got)
	}
	if strings.Count(got, "extractvalue") < 2 {
		t.Fatalf("expected ≥2 extractvalue for two intermediate levels:\n%s", got)
	}
}

// Single-level field assignment (the previous surface) must keep
// working — the rewrite generalised the path so the old behaviour
// should be preserved bit-for-bit at the observable IR level.
func TestSingleLevelFieldAssignStillWorks(t *testing.T) {
	file := parseLLVMGenFile(t, `struct P {
    n: Int,
}

fn main() {
    let mut p: P = P { n: 0 }
    p.n = 99
    println(p.n)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/single_field_assign.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, "insertvalue %P") {
		t.Fatalf("expected insertvalue %%P for single-level assign:\n%s", got)
	}
}

// Mirrors the canonical toolchain shape that first provoked the
// `LLVM012 field assignment base *ast.FieldExpr` wall — a context
// struct carrying a mutable environment holding a scalar slot, written
// from a sibling struct's field. `toolchain/check.osty:547` is the
// real site (`cx.env.returnTy = sig.retTy`); this test uses the same
// shape with just enough scaffolding for the LLVM backend to lower it.
func TestNestedFieldAssignToolchainEnvPattern(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Env {
    returnTy: Int,
    fnName: Int,
}

struct Ctx {
    env: Env,
}

struct Sig {
    retTy: Int,
    name: Int,
}

fn main() {
    let mut cx: Ctx = Ctx { env: Env { returnTy: 0, fnName: 0 } }
    let sig: Sig = Sig { retTy: 7, name: 1 }
    cx.env.returnTy = sig.retTy
    println(cx.env.returnTy)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/toolchain_env_pattern.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, "store %Ctx") {
		t.Fatalf("expected final store %%Ctx after rebuild chain:\n%s", got)
	}
	if !strings.Contains(got, "insertvalue %Ctx") {
		t.Fatalf("expected outer insertvalue %%Ctx in rebuild chain:\n%s", got)
	}
	if !strings.Contains(got, "insertvalue %Env") {
		t.Fatalf("expected inner insertvalue %%Env in rebuild chain:\n%s", got)
	}
}
