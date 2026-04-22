package llvmgen

import (
	"strings"
	"testing"
)

// TestTryGenerateNativeOwnedModuleCoversLetStructDestructureShorthand
// locks the common `{ field }` shorthand shape: each field binds
// to a local with the same name via extractvalue at the field's
// declared index.
func TestTryGenerateNativeOwnedModuleCoversLetStructDestructureShorthand(t *testing.T) {
	src := `struct Pair {
    first: Int,
    second: Int,
}

fn main() {
    let p = Pair { first: 10, second: 20 }
    let Pair { first, second } = p
    println(first + second)
}
`
	mod := lowerNativeEntryModule(t, src)
	out, ok, err := TryGenerateNativeOwnedModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_struct_destructure_shorthand.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule errored: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported uncovered for let-struct-destructure shorthand")
	}
	got := string(out)
	for _, want := range []string{
		"%Pair = type { i64, i64 }",
		"extractvalue %Pair",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("native-owned IR missing %q:\n%s", want, got)
		}
	}
}

// TestTryGenerateNativeOwnedModuleCoversLetStructDestructureRename
// locks the `{ field: local }` rename shape: the struct field name
// resolves the field slot, the ident pattern on the right side
// provides the binder name.
func TestTryGenerateNativeOwnedModuleCoversLetStructDestructureRename(t *testing.T) {
	src := `struct Point {
    x: Int,
    y: Int,
}

fn main() {
    let p = Point { x: 3, y: 4 }
    let Point { x: a, y: b } = p
    println(a + b)
}
`
	mod := lowerNativeEntryModule(t, src)
	out, ok, err := TryGenerateNativeOwnedModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_struct_destructure_rename.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule errored: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported uncovered for let-struct-destructure rename")
	}
	got := string(out)
	// Both field extracts should appear; exact SSA names are
	// implementation detail so we only assert on the shape.
	if !strings.Contains(got, "extractvalue %Point") {
		t.Fatalf("native-owned IR missing `extractvalue %%Point`:\n%s", got)
	}
}

// TestTryGenerateNativeOwnedModuleCoversLetStructDestructureWithRest
// locks `let Foo { name, .. }` — trailing `..` means the struct
// has more fields but the pattern only names a subset. The native
// path must still accept (no field-count mismatch rejection).
func TestTryGenerateNativeOwnedModuleCoversLetStructDestructureWithRest(t *testing.T) {
	src := `struct Big {
    a: Int,
    b: Int,
    c: Int,
}

fn main() {
    let big = Big { a: 1, b: 2, c: 3 }
    let Big { a, .. } = big
    println(a)
}
`
	mod := lowerNativeEntryModule(t, src)
	out, ok, err := TryGenerateNativeOwnedModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_struct_destructure_rest.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule errored: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported uncovered for let-struct-destructure with ..")
	}
	got := string(out)
	if !strings.Contains(got, "extractvalue %Big") {
		t.Fatalf("native-owned IR missing `extractvalue %%Big`:\n%s", got)
	}
}

// TestNativeLetStructDestructureRejectsNestedPattern — stage-1
// coverage only handles plain-ident sub-patterns. Nested struct
// pattern bindings (`let Foo { inner: Bar { x } } = ...`) must
// defer to the legacy bridge.
func TestNativeLetStructDestructureRejectsNestedPattern(t *testing.T) {
	src := `struct Inner {
    x: Int,
}

struct Outer {
    inner: Inner,
}

fn main() {
    let o = Outer { inner: Inner { x: 7 } }
    let Outer { inner: Inner { x } } = o
    println(x)
}
`
	mod := lowerNativeEntryModule(t, src)
	_, ok, err := TryGenerateNativeOwnedModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_struct_destructure_nested.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule errored: %v", err)
	}
	if ok {
		t.Fatal("TryGenerateNativeOwnedModule unexpectedly covered nested struct destructure — stage-1 should defer")
	}
}
