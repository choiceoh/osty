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

func TestGenerateSafepointKeepsImmutableManagedLocalsAndAggregateFields(t *testing.T) {
	file := parseLLVMGenFile(t, `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

struct Bucket {
    items: List<String>
}

fn touch() {}

fn bucketCount(bucket: Bucket) -> Int {
    touch()
    bucket.items.len()
}

fn main() {
    let parts = strings.Split("osty,llvm", ",")
    touch()
    println(parts.len())
    let bucket = Bucket { items: strings.Split("gc,llvm", ",") }
    println(bucketCount(bucket))
}
`)

	ir, err := Generate(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/gc_safepoint_roots.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare void @osty.gc.safepoint_v1(i64, ptr, i64)",
		"call void @osty.gc.safepoint_v1(",
		"alloca %Bucket",
		"getelementptr inbounds %Bucket",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
