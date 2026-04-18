package llvmgen

import (
	"strings"
	"testing"
)

// TestGenerateHeterogeneousEnumDeclarationSucceeds confirms that a
// heterogeneous-payload enum can be declared and registered in the LLVM
// backend's type environment without erroring at collection time.
// The enum lowers with a boxed `{i64 tag, ptr payload}` storage layout.
func TestGenerateHeterogeneousEnumDeclarationSucceeds(t *testing.T) {
	file := parseLLVMGenFile(t, `enum SemPreIdent {
    PreNone,
    PreNumber(Int),
    PreText(String),
}

fn main() {
    println(0)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/enum_heterogeneous_decl.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error for unused boxed enum: %v", err)
	}
	if !strings.Contains(string(ir), "%SemPreIdent = type { i64, ptr }") {
		t.Fatalf("generated IR missing boxed storage typedef:\n%s", ir)
	}
}

// TestGenerateHeterogeneousEnumBoxedScalarConstruction verifies that
// `SemPreIdent.PreNumber(42)` lowers to a GC heap allocation for the
// payload, a store of the Int into that slot, and assembly of the
// outer `{i64 tag, ptr}` aggregate.
func TestGenerateHeterogeneousEnumBoxedScalarConstruction(t *testing.T) {
	file := parseLLVMGenFile(t, `enum SemPreIdent {
    PreNone,
    PreNumber(Int),
    PreText(String),
}

fn main() {
    let x = SemPreIdent.PreNumber(42)
    let _ = x
    println(0)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/enum_heterogeneous_ctor_scalar.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error for boxed scalar construction: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty.rt.enum_alloc_scalar_v1(ptr)",
		"call ptr @osty.rt.enum_alloc_scalar_v1(ptr",
		"store i64 42, ptr",
		"insertvalue %SemPreIdent undef, i64 1, 0",
		"insertvalue %SemPreIdent",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateHeterogeneousEnumBoxedBareVariant verifies that
// `SemPreIdent.PreNone`, a payload-free variant of a boxed enum, lowers
// to `{i64 0, ptr null}` with no GC allocation.
func TestGenerateHeterogeneousEnumBoxedBareVariant(t *testing.T) {
	file := parseLLVMGenFile(t, `enum SemPreIdent {
    PreNone,
    PreNumber(Int),
    PreText(String),
}

fn main() {
    let x = SemPreIdent.PreNone
    let _ = x
    println(0)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/enum_heterogeneous_ctor_bare.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error for boxed bare variant: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"insertvalue %SemPreIdent undef, i64 0, 0",
		"insertvalue %SemPreIdent",
		"ptr null, 1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	// Bare variant must NOT allocate.
	for _, symbol := range []string{"osty.rt.enum_alloc_ptr_v1", "osty.rt.enum_alloc_scalar_v1"} {
		callMarker := "call ptr @" + symbol + "("
		if strings.Contains(got, callMarker) {
			t.Fatalf("bare variant unexpectedly emitted %s call:\n%s", symbol, got)
		}
	}
}

// TestGenerateHeterogeneousEnumMatchTagOnly verifies that matching a boxed
// enum without binding any payload lowers to a tag extract + icmp chain.
// No extractvalue/load of the heap payload is needed because no arm
// binds it.
func TestGenerateHeterogeneousEnumMatchTagOnly(t *testing.T) {
	file := parseLLVMGenFile(t, `enum SemPreIdent {
    PreNone,
    PreNumber(Int),
    PreText(String),
}

fn describe(x: SemPreIdent) -> Int {
    match x {
        SemPreIdent.PreNone -> 0,
        SemPreIdent.PreNumber(_) -> 1,
        _ -> 2,
    }
}

fn main() {
    println(0)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/enum_heterogeneous_match_tag.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error for boxed tag match: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"extractvalue %SemPreIdent",
		"icmp eq i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateHeterogeneousEnumSemverShape mirrors the real toolchain
// SemPreIdent usage: 3-arm exhaustive match returning a String, with one
// arm binding a String payload and another a bare variant fallback.
// This is the end-to-end shape the toolchain port needs.
func TestGenerateHeterogeneousEnumSemverShape(t *testing.T) {
	file := parseLLVMGenFile(t, `enum SemPreIdent {
    PreNone,
    PreNumber(Int),
    PreText(String),
}

fn toText(x: SemPreIdent) -> String {
    match x {
        SemPreIdent.PreNone -> "none",
        SemPreIdent.PreNumber(_) -> "num",
        SemPreIdent.PreText(t) -> t,
    }
}

fn main() {
    let a = SemPreIdent.PreNone
    let b = SemPreIdent.PreNumber(42)
    let c = SemPreIdent.PreText("alpha")
    let _ = toText(a)
    let _ = toText(b)
    let _ = toText(c)
    println(0)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/enum_heterogeneous_semver.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error for semver-shape boxed enum: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"%SemPreIdent = type { i64, ptr }",
		"call ptr @osty.rt.enum_alloc_scalar_v1(ptr",
		"call ptr @osty.rt.enum_alloc_ptr_v1(ptr",
		"store i64 42, ptr",
		"store ptr",
		"insertvalue %SemPreIdent undef, i64 0, 0",
		"insertvalue %SemPreIdent undef, i64 1, 0",
		"insertvalue %SemPreIdent undef, i64 2, 0",
		"load ptr, ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateHeterogeneousEnumConstRejectsWithActionableHint verifies that
// using a boxed-payload enum variant in a constant expression (e.g. a top-level
// `pub let X: T = EnumName.Variant`) is rejected with a diagnostic that points
// the user at the runtime-constructor workaround.
func TestGenerateHeterogeneousEnumConstRejectsWithActionableHint(t *testing.T) {
	file := parseLLVMGenFile(t, `enum SemPreIdent {
    PreNone,
    PreNumber(Int),
    PreText(String),
}

pub let DefaultIdent: SemPreIdent = SemPreIdent.PreNone

fn main() {
    println(0)
}
`)

	_, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/enum_heterogeneous_const.osty",
	})
	if err == nil {
		t.Fatalf("Generate succeeded unexpectedly for const boxed variant")
	}
	diag := UnsupportedDiagnosticForError(err)
	for _, want := range []string{
		`enum "SemPreIdent"`,
		"heterogeneous",
		"constant expression",
		"runtime GC allocation",
		"constructor function",
	} {
		if !strings.Contains(diag.Message, want) {
			t.Fatalf("diag.Message = %q, missing %q", diag.Message, want)
		}
	}
}

// TestGenerateHeterogeneousEnumMatchBindsBoxedPayload verifies that an arm
// binding the payload of a boxed variant emits extractvalue for the heap
// ptr at index 1 followed by a typed load of the payload from that ptr.
func TestGenerateHeterogeneousEnumMatchBindsBoxedPayload(t *testing.T) {
	file := parseLLVMGenFile(t, `enum SemPreIdent {
    PreNone,
    PreNumber(Int),
    PreText(String),
}

fn numberOrZero(x: SemPreIdent) -> Int {
    match x {
        SemPreIdent.PreNumber(n) -> n,
        _ -> 0,
    }
}

fn main() {
    println(0)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/enum_heterogeneous_match_bind.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error for boxed payload bind: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"extractvalue %SemPreIdent",
		"load i64, ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
