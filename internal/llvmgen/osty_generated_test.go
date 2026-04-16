package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/parser"
)

func assertGeneratedIRContains(t *testing.T, ir, want string) {
	t.Helper()
	if !strings.Contains(ir, want) {
		t.Fatalf("generated IR missing %q\nIR:\n%s", want, ir)
	}
}

func generateLLVMForTest(t *testing.T, src string) string {
	t.Helper()
	file, diags := parser.ParseDiagnostics([]byte(src))
	if len(diags) > 0 {
		t.Fatalf("parse returned %d diagnostics: %v", len(diags), diags[0])
	}
	ir, err := Generate(file, Options{SourcePath: "/tmp/managed.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	return string(ir)
}

func TestGeneratedRenderSkeletonInterpolatesQuotedFields(t *testing.T) {
	ir := llvmRenderSkeleton(
		"main",
		"/tmp/unsupported.osty",
		"llvm-ir",
		"x86_64-unknown-linux-gnu",
		"",
	)

	assertGeneratedIRContains(t, ir, "source_filename = \"/tmp/unsupported.osty\"")
	assertGeneratedIRContains(t, ir, "target triple = \"x86_64-unknown-linux-gnu\"")
}

func TestGeneratedGcRuntimeAbiSmokeIR(t *testing.T) {
	ir := llvmSmokeGcRuntimeAbiIR("/tmp/gc_runtime_abi.osty")

	assertGeneratedIRContains(t, ir, "source_filename = \"/tmp/gc_runtime_abi.osty\"")
	assertGeneratedIRContains(t, ir, "declare ptr @osty.gc.alloc_v1(i64, i64, ptr)")
	assertGeneratedIRContains(t, ir, "declare void @osty.gc.post_write_v1(ptr, ptr, i64)")
	assertGeneratedIRContains(t, ir, "declare void @osty.gc.root_bind_v1(ptr)")
	assertGeneratedIRContains(t, ir, "declare void @osty.gc.root_release_v1(ptr)")
	assertGeneratedIRContains(t, ir, "@.str0 = private unnamed_addr constant [15 x i8] c\"llvm.gc.object\\00\"")
	assertGeneratedIRContains(t, ir, "%t0 = call ptr @osty.gc.alloc_v1(i64 1, i64 32, ptr @.str0)")
	assertGeneratedIRContains(t, ir, "call void @osty.gc.post_write_v1(ptr %t0, ptr %t1, i64 0)")
	assertGeneratedIRContains(t, ir, "call void @osty.gc.root_release_v1(ptr %t0)")
}

func TestGenerateLeavesRawStringGlobalsUnmanagedUntilHeapLowering(t *testing.T) {
	ir := generateLLVMForTest(t, `
fn main() {
    let mut msg = "before"
    msg = "after"
    println(msg)
}
`)

	assertGeneratedIRContains(t, ir, "@.str0 = private unnamed_addr constant [7 x i8] c\"before\\00\"")
	assertGeneratedIRContains(t, ir, "@.str1 = private unnamed_addr constant [6 x i8] c\"after\\00\"")
	assertGeneratedIRContains(t, ir, "store ptr @.str1")
	assertGeneratedIRContains(t, ir, "  ret i32 0")
	if strings.Contains(ir, "@osty.gc.") {
		t.Fatalf("raw string globals should not opt into GC runtime yet\nIR:\n%s", ir)
	}
}
