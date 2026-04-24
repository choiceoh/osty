package llvmgen

import (
	"os"
	"strings"
	"testing"
)

// TestGenerateEnumMultiFieldFixture compiles the canonical multi-field
// payload fixture in testdata/backend/llvm_smoke/ and asserts the whole
// IR is well-formed: the layout, all three constructors, and the match
// extractvalue chain must emit together without errors.
func TestGenerateEnumMultiFieldFixture(t *testing.T) {
	src, err := os.ReadFile("../../testdata/backend/llvm_smoke/enum_multi_field_payload_print.osty")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	file := parseLLVMGenFile(t, string(src))
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/enum_multi_field_payload_print.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"%Event = type { i64, i64, i64 }",
		"insertvalue %Event undef, i64 0, 0",
		", i64 3, 1",
		", i64 4, 2",
		"insertvalue %Event undef, i64 1, 0",
		", i64 7, 1",
		"insertvalue %Event undef, i64 2, 0",
		"extractvalue %Event",
		", 1",
		", 2",
		"mul i64",
		"call void @osty_rt_io_write",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("fixture IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateEnumMultiFieldPayloadConstructs verifies that a homogeneous
// multi-field payload enum (e.g. Event.Click(Int, Int)) lowers to a
// `%Enum = type { i64, i64, i64 }` layout and that each constructor
// insertvalue reaches the correct slot.
func TestGenerateEnumMultiFieldPayloadConstructs(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Event {
    Click(Int, Int)
    Tick(Int)
    Close
}

fn main() {
    let a: Event = Click(3, 4)
    let b: Event = Tick(7)
    let c: Event = Close
    let _ = a
    let _ = b
    let _ = c
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/enum_multi_field.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%Event = type { i64, i64, i64 }",
		"insertvalue %Event undef, i64 0, 0",
		", i64 3, 1",
		", i64 4, 2",
		"insertvalue %Event undef, i64 1, 0",
		", i64 7, 1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateEnumBoxedMultiFieldScalar verifies that an enum that is
// forced onto the boxed path (different slot-0 types across variants)
// can still carry a scalar-only multi-field variant: the payload is
// heap-allocated and each field stored at an 8-byte offset via GEP.
func TestGenerateEnumBoxedMultiFieldScalar(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Mixed {
    Pair(Int, Int)
    Ratio(Float)
    Empty
}

fn main() {
    let a: Mixed = Pair(3, 4)
    let b: Mixed = Ratio(1.5)
    let c: Mixed = Empty
    let _ = a
    let _ = b
    let _ = c
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/enum_boxed_multi.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%Mixed = type { i64, ptr }",
		"call ptr @osty.gc.alloc_v1(i64 1, i64 16,",
		"getelementptr i8, ptr",
		"store i64 3, ptr",
		"store i64 4, ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateEnumBoxedMultiFieldMatch verifies that matching a boxed
// multi-field variant loads each slot via GEP+load.
func TestGenerateEnumBoxedMultiFieldMatch(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Mixed {
    Pair(Int, Int)
    Ratio(Float)
}

fn sumOf(m: Mixed) -> Int {
    match m {
        Pair(a, b) -> a + b,
        Ratio(_) -> 0,
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/enum_boxed_multi_match.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%Mixed = type { i64, ptr }",
		"extractvalue %Mixed",
		"getelementptr i8, ptr",
		"load i64, ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateEnumHeterogeneousPayloadSlotsMatch verifies that matching
// a heterogeneous-slot variant binds each slot with the correct
// extractvalue type.
func TestGenerateEnumHeterogeneousPayloadSlotsMatch(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Entry {
    Pair(String, Int)
    Empty
}

fn countOf(e: Entry) -> Int {
    match e {
        Pair(_, n) -> n,
        Empty -> 0,
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/enum_hetero_match.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%Entry = type { i64, ptr, i64 }",
		"extractvalue %Entry",
		", 2",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	// slot 1 is wildcarded and should not be extracted into a local.
	extractSlot1 := 0
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "extractvalue %Entry") && strings.HasSuffix(strings.TrimSpace(line), ", 1") {
			extractSlot1++
		}
	}
	if extractSlot1 != 0 {
		t.Fatalf("wildcarded slot 1 was extracted %d time(s):\n%s", extractSlot1, got)
	}
}

// TestGenerateEnumHeterogeneousPayloadSlots verifies that a variant with
// heterogeneous payload slot types (e.g. `Entry(String, Int)`) lowers
// inline with the correct per-slot LLVM types rather than forcing the
// boxed path.
func TestGenerateEnumHeterogeneousPayloadSlots(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Entry {
    Pair(String, Int)
    Empty
}

fn main() {
    let e: Entry = Pair("alice", 7)
    let _ = e
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/enum_hetero_slots.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%Entry = type { i64, ptr, i64 }",
		"insertvalue %Entry undef, i64 0, 0",
		", i64 7, 2",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateEnumMultiFieldPayloadMatchBindsBothSlots verifies that a
// match on Click(x, y) emits two extractvalue instructions pulling
// slots 1 and 2 into separate locals.
func TestGenerateEnumMultiFieldPayloadMatchBindsBothSlots(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Event {
    Click(Int, Int)
    Tick(Int)
    Close
}

fn dx(e: Event) -> Int {
    match e {
        Click(x, y) -> x + y,
        Tick(n) -> n,
        Close -> 0,
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/enum_multi_field_match.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%Event = type { i64, i64, i64 }",
		"extractvalue %Event %e, 1",
		"extractvalue %Event %e, 2",
		"add i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
