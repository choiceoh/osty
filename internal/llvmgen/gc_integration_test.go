package llvmgen

import (
	"strings"
	"testing"
)

func TestGenerateVoidFunctionReleasesManagedRoots(t *testing.T) {
	file := parseLLVMGenFile(t, `fn touch() {
    let mut values: List<Int> = []
    values.push(1)
}
`)

	ir, err := Generate(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/void_gc_roots.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	release := "call void @osty.gc.root_release_v1"
	ret := "ret void"
	if !strings.Contains(got, "call void @osty.gc.root_bind_v1") {
		t.Fatalf("generated IR missing root bind:\n%s", got)
	}
	releaseIndex := strings.Index(got, release)
	retIndex := strings.Index(got, ret)
	if releaseIndex < 0 {
		t.Fatalf("generated IR missing root release:\n%s", got)
	}
	if retIndex < 0 {
		t.Fatalf("generated IR missing ret void:\n%s", got)
	}
	if releaseIndex > retIndex {
		t.Fatalf("root release appears after ret void:\n%s", got)
	}
}
