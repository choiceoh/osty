package llvmgen

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestGenerateVoidFunctionReleasesManagedRoots(t *testing.T) {
	file := parseLLVMGenFile(t, `fn touch() {
    let mut values: List<Int> = []
    values.push(1)
}
`)

	ir, err := generateFromAST(file, Options{
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

	ir, err := generateFromAST(file, Options{
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

	ir, err := generateFromAST(file, Options{
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

	ir, err := generateFromAST(file, Options{
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

	ir, err := generateFromAST(file, Options{
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

// safepointKindMix decodes every `safepoint_v1(i64 N, ...)` call in the
// given IR and returns the count per kind. The high byte of N (bits
// 56..63) holds the kind tag; the remaining 56 bits are the serial. See
// encodeSafepointID in generator.go and OSTY_GC_SAFEPOINT_KIND_* in the
// runtime's osty_runtime.c.
func safepointKindMix(t *testing.T, ir string) map[safepointKind]int {
	t.Helper()
	re := regexp.MustCompile(`safepoint_v1\(i64 (-?\d+),`)
	matches := re.FindAllStringSubmatch(ir, -1)
	mix := map[safepointKind]int{}
	for _, m := range matches {
		v, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			t.Fatalf("safepoint id %q not int: %v", m[1], err)
		}
		kind := safepointKind(uint64(v) >> 56 & 0xff)
		mix[kind]++
	}
	return mix
}

// TestGenerateFromASTSafepointSplitsLargeFrames covers the A6 depth
// follow-up — when a frame has more visible managed roots than
// `safepointRootChunkSize`, a single safepoint site splits into
// multiple `osty.gc.safepoint_v1` calls so the per-call `alloca ptr,
// i64 N` stays bounded. We lower the chunk size for the test so we can
// trip the path without constructing a function with thousands of
// roots.
func TestGenerateFromASTSafepointSplitsLargeFrames(t *testing.T) {
	prev := safepointRootChunkSize
	safepointRootChunkSize = 3
	defer func() { safepointRootChunkSize = prev }()

	// Seven managed List locals visible at the pre-call site. Chunk
	// size 3 → ceil(7/3) = 3 safepoint calls for that final poll.
	// Lists are heap-allocated so each let binds a managed root.
	file := parseLLVMGenFile(t, `fn sink(xs: List<Int>) {}

fn main() {
    let mut a: List<Int> = []
    let mut b: List<Int> = []
    let mut c: List<Int> = []
    let mut d: List<Int> = []
    let mut e: List<Int> = []
    let mut f: List<Int> = []
    let mut g: List<Int> = []
    sink(a)
    sink(b)
    sink(c)
    sink(d)
    sink(e)
    sink(f)
    sink(g)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/safepoint_split.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	// The last pre-call site (for sink(g)) sees all seven roots and
	// must emit three safepoint calls with alloca sizes 3, 3, 1.
	allocas := regexp.MustCompile(`alloca ptr, i64 (\d+)`).FindAllStringSubmatch(got, -1)
	sawChunkedPoll := false
	sizes := make([]int, 0, len(allocas))
	for _, m := range allocas {
		n, _ := strconv.Atoi(m[1])
		sizes = append(sizes, n)
		if n == 3 {
			sawChunkedPoll = true
		}
	}
	if !sawChunkedPoll {
		t.Fatalf("expected at least one alloca with chunk size 3, got sizes=%v IR:\n%s", sizes, got)
	}
	// No single alloca exceeds the chunk size — the whole point of the
	// split. Extra roots would allocate as a separate call.
	for _, n := range sizes {
		if n > 3 {
			t.Fatalf("alloca size %d exceeds chunk cap 3, sizes=%v IR:\n%s", n, sizes, got)
		}
	}
}

// TestGenerateFromASTSafepointKindMixCallAndLoop covers the A5 depth
// follow-up for the legacy emitter — lowering a function whose body
// is a `while` loop with an indirect user-level call inside must
// produce both CALL and LOOP safepoint kinds, and zero UNSPECIFIED
// (meaning every emit site has been classified).
func TestGenerateFromASTSafepointKindMixCallAndLoop(t *testing.T) {
	file := parseLLVMGenFile(t, `fn work() -> Int {
    1
}

fn main() {
    let values: List<Int> = [1]
    let f = work
    let mut i = 0
    while i < 3 {
        i = i + f()
    }
    println(values.len())
    println(i)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/safepoint_kind_mix.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	mix := safepointKindMix(t, string(ir))
	if mix[safepointKindCall] == 0 {
		t.Fatalf("expected ≥1 CALL-kind safepoint, mix=%v, IR:\n%s", mix, ir)
	}
	if mix[safepointKindLoop] == 0 {
		t.Fatalf("expected ≥1 LOOP-kind safepoint, mix=%v, IR:\n%s", mix, ir)
	}
	if mix[safepointKindUnspecified] != 0 {
		t.Fatalf("UNSPECIFIED safepoint leaked through a classified path, mix=%v, IR:\n%s", mix, ir)
	}
}

func TestGenerateFromASTDirectUserCallSkipsEmptyCallSafepoint(t *testing.T) {
	file := parseLLVMGenFile(t, `fn work() -> Int {
    1
}

fn main() {
    let mut i = 0
    while i < 3 {
        i = i + work()
    }
    println(i)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/direct_call_entry_safepoint.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	mix := safepointKindMix(t, string(ir))
	if mix[safepointKindCall] != 0 {
		t.Fatalf("expected no CALL-kind safepoint for direct user call, mix=%v, IR:\n%s", mix, ir)
	}
	if mix[safepointKindLoop] == 0 {
		t.Fatalf("expected ≥1 LOOP-kind safepoint, mix=%v, IR:\n%s", mix, ir)
	}
}

func TestGenerateFromASTRootlessIndirectCallSkipsCallSafepoint(t *testing.T) {
	file := parseLLVMGenFile(t, `fn work() -> Int {
    1
}

fn main() {
    let f = work
    let mut i = 0
    while i < 3 {
        i = i + f()
    }
    println(i)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/rootless_indirect_call_safepoint.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	mix := safepointKindMix(t, string(ir))
	if mix[safepointKindCall] != 0 {
		t.Fatalf("expected no CALL-kind safepoint for rootless indirect call, mix=%v, IR:\n%s", mix, ir)
	}
	if mix[safepointKindLoop] == 0 {
		t.Fatalf("expected ≥1 LOOP-kind safepoint, mix=%v, IR:\n%s", mix, ir)
	}
}
