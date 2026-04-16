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

func TestGenerateMutReceiverMethodsLowerToReceiverSlots(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Counter {
    value: Int,

    fn add(mut self, delta: Int) -> Int {
        self.value = self.value + delta
        self.value
    }

    fn get(self) -> Int {
        self.value
    }
}

fn main() {
    let mut counter = Counter { value: 1 }
    println(counter.add(2))
    println(counter.get())
}
`)

	ir, err := Generate(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/method_receivers.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define i64 @Counter__add(ptr %self, i64 %delta)",
		"define i64 @Counter__get(%Counter %self)",
		"call i64 @Counter__add(ptr %t",
		"call i64 @Counter__get(%Counter",
		"insertvalue %Counter",
		"alloca %Counter",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateStructFieldsCanUseEnumTypes(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Kind {
    Ready,
    Done,
}

struct Box {
    kind: Kind,
}

fn tag(box: Box) -> Int {
    match box.kind {
        Ready -> 1,
        Done -> 2,
    }
}

fn main() {
    let box = Box { kind: Ready }
    println(tag(box))
}
`)

	ir, err := Generate(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/enum_struct_field.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%Box = type { i64 }",
		"insertvalue %Box undef, i64 0, 0",
		"extractvalue %Box",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateManagedAggregateListsTraceNestedRoots(t *testing.T) {
	file := parseLLVMGenFile(t, `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

struct Bucket {
    items: List<String>
}

fn main() {
    let buckets = [
        Bucket { items: strings.Split("gc,llvm", ",") },
    ]
    for bucket in buckets {
        println(bucket.items.len())
    }
}
`)

	ir, err := Generate(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/managed_aggregate_list_gc.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare void @osty_rt_list_push_bytes_roots_v1(ptr, ptr, i64, ptr, i64)",
		"call void @osty_rt_list_push_bytes_roots_v1(",
		"getelementptr inbounds %Bucket, ptr null, i32 0, i32 0",
		"call void @osty_rt_list_get_bytes_v1(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
