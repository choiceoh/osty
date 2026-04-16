package llvmgen

import (
	"strings"
	"testing"
)

func assertGeneratedIRContains(t *testing.T, ir, want string) {
	t.Helper()
	if !strings.Contains(ir, want) {
		t.Fatalf("generated IR missing %q\nIR:\n%s", want, ir)
	}
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
