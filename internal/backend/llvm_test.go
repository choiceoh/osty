package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

type compileCall struct {
	sourcePath string
	objectPath string
	target     string
}

type linkCall struct {
	objectPaths []string
	binaryPath  string
	target      string
}

type fakeLLVMToolchain struct {
	irCompiles []compileCall
	cCompiles  []compileCall
	links      []linkCall
}

func (f *fakeLLVMToolchain) CompileObject(_ context.Context, irPath, objectPath, target string) error {
	f.irCompiles = append(f.irCompiles, compileCall{
		sourcePath: irPath,
		objectPath: objectPath,
		target:     target,
	})
	return nil
}

func (f *fakeLLVMToolchain) CompileCObject(_ context.Context, sourcePath, objectPath, target string) error {
	f.cCompiles = append(f.cCompiles, compileCall{
		sourcePath: sourcePath,
		objectPath: objectPath,
		target:     target,
	})
	return nil
}

func (f *fakeLLVMToolchain) LinkBinary(_ context.Context, objectPaths []string, binaryPath, target string) error {
	f.links = append(f.links, linkCall{
		objectPaths: append([]string(nil), objectPaths...),
		binaryPath:  binaryPath,
		target:      target,
	})
	return nil
}

func parseBackendFile(t *testing.T, src string) (*ast.File, *resolve.Result, *check.Result) {
	t.Helper()

	file, diags := parser.ParseDiagnostics([]byte(src))
	if len(diags) != 0 {
		t.Fatalf("ParseDiagnostics returned %d diagnostics: %v", len(diags), diags)
	}
	if file == nil {
		t.Fatal("ParseDiagnostics returned nil file")
	}
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.File(file, res, check.Opts{
		UseGolegacy:   true,
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
	})
	return file, res, chk
}

func newBackendRequest(t *testing.T, emit EmitMode, src string) Request {
	t.Helper()

	root := t.TempDir()
	file, res, chk := parseBackendFile(t, src)
	entry, err := PrepareEntry(
		"main",
		filepath.Join(root, "main.osty"),
		file,
		res,
		chk,
	)
	if err != nil {
		t.Fatalf("PrepareEntry returned error: %v", err)
	}
	return Request{
		Layout: Layout{
			Root:    root,
			Profile: "debug",
		},
		Emit:       emit,
		Entry:      entry,
		BinaryName: "app",
	}
}

func requireClangForBackendTest(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}
}

func parallelClangBackendTest(t *testing.T) {
	t.Helper()
	requireClangForBackendTest(t)
	t.Parallel()
}

func TestLLVMBackendEmitBinaryBuildsBundledRuntime(t *testing.T) {
	t.Parallel()

	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let mut values: List<Int> = []
    values.push(1)
    println(values.len())
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	if result == nil {
		t.Fatal("Emit returned nil result")
	}
	if got := len(tc.irCompiles); got != 1 {
		t.Fatalf("IR compile count = %d, want 1", got)
	}
	if got := len(tc.cCompiles); got != 1 {
		t.Fatalf("runtime compile count = %d, want 1", got)
	}
	if got := len(tc.links); got != 1 {
		t.Fatalf("link count = %d, want 1", got)
	}
	runtimeSource := filepath.Join(result.Artifacts.RuntimeDir, bundledRuntimeSourceName)
	runtimeObject := filepath.Join(result.Artifacts.RuntimeDir, bundledRuntimeObjectName)
	if got := tc.cCompiles[0].sourcePath; got != runtimeSource {
		t.Fatalf("runtime source path = %q, want %q", got, runtimeSource)
	}
	if got := tc.cCompiles[0].objectPath; got != runtimeObject {
		t.Fatalf("runtime object path = %q, want %q", got, runtimeObject)
	}
	content, err := os.ReadFile(runtimeSource)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", runtimeSource, err)
	}
	for _, want := range []string{
		"osty_rt_list_new",
		"osty_rt_map_new",
		"osty_rt_set_new",
		"osty_rt_list_push_bytes_v1",
		"osty_rt_list_push_bytes_roots_v1",
		"osty_rt_list_get_bytes_v1",
		"osty_rt_strings_Equal",
		"osty.gc.pre_write_v1",
		"osty.gc.load_v1",
		"osty.gc.root_bind_v1",
		"osty.gc.safepoint_v1",
		"osty_gc_debug_collect",
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("bundled runtime source missing %q", want)
		}
	}
	link := tc.links[0]
	if len(link.objectPaths) != 2 {
		t.Fatalf("link object count = %d, want 2 (%v)", len(link.objectPaths), link.objectPaths)
	}
	if got := link.objectPaths[0]; got != result.Artifacts.Object {
		t.Fatalf("link object[0] = %q, want %q", got, result.Artifacts.Object)
	}
	if got := link.objectPaths[1]; got != runtimeObject {
		t.Fatalf("link object[1] = %q, want %q", got, runtimeObject)
	}
}

func TestLLVMBackendEmitLLVMIRSkipsToolchain(t *testing.T) {
	t.Parallel()

	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    println(1)
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	if result == nil {
		t.Fatal("Emit returned nil result")
	}
	if got := len(tc.irCompiles); got != 0 {
		t.Fatalf("IR compile count = %d, want 0", got)
	}
	if got := len(tc.cCompiles); got != 0 {
		t.Fatalf("runtime compile count = %d, want 0", got)
	}
	if got := len(tc.links); got != 0 {
		t.Fatalf("link count = %d, want 0", got)
	}
	if _, err := os.Stat(result.Artifacts.RuntimeDir); err != nil {
		t.Fatalf("runtime dir %q missing: %v", result.Artifacts.RuntimeDir, err)
	}
}

func TestLLVMBackendEmitLLVMIRFromIRWithoutASTFallback(t *testing.T) {
	t.Parallel()

	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    let mut i = 0
    for i < 2 {
        println(i)
        i = i + 1
    }
}
`)
	req.Entry.File = nil

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error with IR-only entry: %v", err)
	}
	if result == nil {
		t.Fatal("Emit returned nil result")
	}
	data, readErr := os.ReadFile(result.Artifacts.LLVMIR)
	if readErr != nil {
		t.Fatalf("ReadFile(%q): %v", result.Artifacts.LLVMIR, readErr)
	}
	got := string(data)
	if !strings.Contains(got, "@printf") {
		t.Fatalf("IR-only backend output missing %q:\n%s", "@printf", got)
	}
	if !strings.Contains(got, "for.cond") && !strings.Contains(got, "bb1:") {
		t.Fatalf("IR-only backend output missing loop label from either legacy or MIR path:\n%s", got)
	}
}

func TestEmitLLVMIRTextMatchesBackendArtifactOutput(t *testing.T) {
	t.Parallel()

	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    println(1)
}
`)

	got, warnings, err := EmitLLVMIRText(req.Entry, "", nil)
	if err != nil {
		t.Fatalf("EmitLLVMIRText returned error: %v", err)
	}
	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	want, readErr := os.ReadFile(result.Artifacts.LLVMIR)
	if readErr != nil {
		t.Fatalf("ReadFile(%q): %v", result.Artifacts.LLVMIR, readErr)
	}
	if string(got) != string(want) {
		t.Fatalf("direct llvm-ir text did not match artifact output\n--- direct ---\n%s\n--- artifact ---\n%s", got, want)
	}
	if len(warnings) != len(result.Warnings) {
		t.Fatalf("warning count = %d, want %d", len(warnings), len(result.Warnings))
	}
}

func TestUseMIRBackendDefaultsEnabled(t *testing.T) {
	t.Parallel()

	if !useMIRBackend(nil, EmitLLVMIR) {
		t.Fatal("useMIRBackend(nil, EmitLLVMIR) = false, want true")
	}
	if useMIRBackend(nil, EmitBinary) {
		t.Fatal("useMIRBackend(nil, EmitBinary) = true, want false")
	}
	if !useMIRBackend([]string{"mir-backend"}, EmitBinary) {
		t.Fatal("useMIRBackend(mir-backend, EmitBinary) = false, want true")
	}
}

func TestUseMIRBackendLegacyFeatureDisables(t *testing.T) {
	t.Parallel()

	if useMIRBackend([]string{"legacy-llvmgen"}, EmitLLVMIR) {
		t.Fatal("useMIRBackend(legacy-llvmgen, EmitLLVMIR) = true, want false")
	}
	if useMIRBackend([]string{"mir-backend", "legacy-llvmgen"}, EmitBinary) {
		t.Fatal("legacy-llvmgen should win when both features are present")
	}
}

// TestLLVMBackendRefusesNilIR confirms the dispatcher's IR-only
// contract: without req.Entry.IR the backend must reject the request
// rather than silently fall through to any AST-based path (no such
// path exists any more).
func TestLLVMBackendRefusesNilIR(t *testing.T) {
	t.Parallel()

	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitLLVMIR, `fn main() { println(1) }`)
	req.Entry.IR = nil
	// File is still populated; if the dispatcher secretly consulted it
	// the test would pass by accident — we want a hard reject instead.
	_, err := backend.Emit(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when IR is nil, got none")
	}
	if !strings.Contains(err.Error(), "missing lowered IR entry") {
		t.Fatalf("expected IR-missing diagnostic, got: %v", err)
	}
}

// TestLLVMBackendEmitLLVMIRMIRBackendStringIntrinsics — Stage 5 prep
// IR-only parity check that doesn't require clang. On `mir-backend`,
// a program using `.chars()` / `.bytes()` / `.len()` / `.isEmpty()` on
// a String must reach the MIR-direct emitter: the backend header tag
// is the stable tell (`osty LLVM MIR backend`), and the runtime
// symbols must all land in the emitted text.
//
// Paired with TestLLVMBackendBinaryMIRBackendStringCharsBytes: that
// one locks in the actual runtime behavior through a linked binary
// but needs clang and may be skipped. This one always runs and
// catches silent fallback to the legacy bridge.
func TestLLVMBackendEmitLLVMIRMIRBackendStringIntrinsics(t *testing.T) {
	t.Parallel()

	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    let s = "abc"
    println(s.chars().len())
    println(s.bytes().len())
    println(s.len())
    if s.isEmpty() {
        println(1)
    } else {
        println(0)
    }
}
`)
	req.Features = []string{"mir-backend"}

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	irBytes, readErr := os.ReadFile(result.Artifacts.LLVMIR)
	if readErr != nil {
		t.Fatalf("ReadFile(%q): %v", result.Artifacts.LLVMIR, readErr)
	}
	ir := string(irBytes)
	if !strings.Contains(ir, "osty LLVM MIR backend") {
		t.Fatalf("mir-backend feature did not reach MIR emitter (header missing):\n%s", ir)
	}
	for _, want := range []string{
		"@osty_rt_strings_Chars",
		"@osty_rt_strings_Bytes",
		"@osty_rt_strings_ByteLen",
	} {
		if !strings.Contains(ir, want) {
			t.Fatalf("expected IR to contain %q, got:\n%s", want, ir)
		}
	}
}

// TestLLVMBackendBinaryMIRBackendStringCharsBytes — Stage 5 prep
// parity check. With `mir-backend` opted in on object/binary emission,
// `.chars()` / `.bytes()` / `.len()` / `.isEmpty()` on a String must
// lower through the MIR-direct emitter (no silent fallback to the
// legacy AST bridge). The emitted IR header is the only stable tell of
// which path ran; this test locks in both that signal AND the linked
// binary's output so a regression on either side surfaces immediately.
func TestLLVMBackendBinaryMIRBackendStringCharsBytes(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let s = "abc"
    println(s.chars().len())
    println(s.bytes().len())
    println(s.len())
    if s.isEmpty() {
        println(1)
    } else {
        println(0)
    }
}
`)
	req.Features = []string{"mir-backend"}

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	irBytes, readErr := os.ReadFile(result.Artifacts.LLVMIR)
	if readErr != nil {
		t.Fatalf("ReadFile(%q): %v", result.Artifacts.LLVMIR, readErr)
	}
	ir := string(irBytes)
	if !strings.Contains(ir, "osty LLVM MIR backend") {
		t.Fatalf("mir-backend feature did not reach MIR emitter (header missing):\n%s", ir)
	}
	for _, want := range []string{
		"@osty_rt_strings_Chars",
		"@osty_rt_strings_Bytes",
		"@osty_rt_strings_ByteLen",
	} {
		if !strings.Contains(ir, want) {
			t.Fatalf("expected IR to contain %q, got:\n%s", want, ir)
		}
	}

	cmd := exec.Command(result.Artifacts.Binary)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "3\n3\n3\n0\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryRunsBundledRuntime(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn touch() {
    let mut values: List<Int> = []
    values.push(41)
    values.push(1)
    println(values.len())
}

fn main() {
    touch()
    if "osty" == "osty" {
        println(1)
    } else {
        println(0)
    }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "2\n1\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinarySafepointsKeepManagedRootsAlive(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

struct Bucket {
    items: List<String>
}

fn touch() {}

fn localCount() -> Int {
    let parts = strings.Split("gc,llvm", ",")
    touch()
    parts.len()
}

fn bucketCount(bucket: Bucket) -> Int {
    touch()
    bucket.items.len()
}

fn main() {
    println(localCount())
    let bucket = Bucket { items: strings.Split("one,two", ",") }
    println(bucketCount(bucket))
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	cmd.Env = append(os.Environ(), "OSTY_GC_STRESS=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "2\n2\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryAutoCollectsOnPressure(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

fn touch() {}

fn localCount() -> Int {
    let parts = strings.Split("gc,llvm", ",")
    touch()
    parts.len()
}

fn main() {
    println(localCount())
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	cmd.Env = append(os.Environ(), "OSTY_GC_THRESHOLD_BYTES=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "2\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryForInOverTemporaryManagedListSurvivesPressure(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

fn main() {
    for item in strings.Split("a,b", ",") {
        println(item)
    }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	cmd.Env = append(os.Environ(), "OSTY_GC_THRESHOLD_BYTES=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "a\nb\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryManagedTemporaryCallArgSurvivesPressure(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

fn count() -> Int {
    1
}

fn take(items: List<String>, n: Int) -> Int {
    items.len() + n
}

fn main() {
    println(take(strings.Split("a,b", ","), count()))
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	cmd.Env = append(os.Environ(), "OSTY_GC_THRESHOLD_BYTES=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "3\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryRunsBitwiseIntOps(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    println(~-43)
    println((1 << 5) | (1 << 3) | 2)
    println((255 >> 2) ^ 21)
    println(58 & 43)
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "42\n42\n42\n42\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryMutReceiverMethodWritesBackToCaller(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `struct Counter {
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

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "3\n3\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryCollectionsUseRuntimeContainers(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `struct Pair {
    left: Int
    right: Int
}

fn main() {
    let mut pairs: Map<Int, Pair> = {:}
    pairs.insert(1, Pair { left: 2, right: 3 })
    if pairs.containsKey(1) {
        println(1)
    } else {
        println(0)
    }
    let pair = pairs[1]
    println(pair.left + pair.right)

    let keys = pairs.keys().sorted()
    println(keys[0])

    let mut values: List<Pair> = []
    values.push(Pair { left: 4, right: 6 })
    let value = values[0]
    println(value.left + value.right)

    let mut nums: List<Int> = [1]
    nums[0] = 9
    println(nums[0])

    let empty: List<Int> = []
    let mut seen = empty.toSet()
    seen.insert(7)
    seen.insert(7)
    println(seen.len())
    let ids = seen.toList()
    println(ids[0])
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "1\n5\n1\n10\n9\n1\n7\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryManagedAggregateContainersSurvivePressureGC(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `struct Bucket {
    ids: List<Int>
}

fn main() {
    let ids: List<Int> = [7]
    let mut buckets: Map<String, Bucket> = {:}
    buckets.insert("root", Bucket { ids: ids })
    let bucket = buckets["root"]

    let empty: List<Int> = []
    let mut seen = empty.toSet()
    seen.insert(7)

    println(bucket.ids[0])
    println(seen.len())
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	cmd.Env = append(os.Environ(), "OSTY_GC_THRESHOLD_BYTES=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "7\n1\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryExtendedListSortedAndToSet(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let words: List<String> = ["pear", "apple", "banana", "apple"]
    let wordSet = words.sorted().toSet()
    println(wordSet.len())
    println(wordSet.toList().sorted()[0])

    let values: List<Float> = [3.5, 1.5, 2.5, 1.5]
    let sortedValues = values.sorted()
    println(sortedValues[0])
    let uniqueValues = sortedValues.toSet()
    println(uniqueValues.len())

    let flags: List<Bool> = [true, false, true]
    let sortedFlags = flags.sorted()
    if sortedFlags[0] {
        println(1)
    } else {
        println(0)
    }
    let uniqueFlags = flags.toSet()
    println(uniqueFlags.len())
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "3\napple\n1.500000\n3\n0\n2\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryPtrBackedListToSetAndBoolPrint(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

fn main() {
    let item = strings.Split("a,b", ",")
    let items: List<List<String>> = [item, item]
    let seen = items.toSet()

    println(seen.len())
    println(seen.contains(item))
    println(seen.len() == 1)
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "1\ntrue\ntrue\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryGenericEnumVariantFromLetContext(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `enum Maybe<T> { Some(T), None }

fn main() {
    let value: Maybe<Int> = Maybe.Some(42)
    if let Maybe.Some(x) = value {
        println(x)
    } else {
        println(0)
    }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "42\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryGenericEnumVariantInferredFromPayload(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `enum Maybe<T> { Some(T), None }

fn main() {
    let value = Maybe.Some(42)
    if let Maybe.Some(x) = value {
        println(x)
    } else {
        println(0)
    }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "42\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryGenericEnumPayloadFreeVariantFromLetContext(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `enum Maybe<T> { Some(T), None }

fn main() {
    let value: Maybe<Int> = Maybe.None
    if let Maybe.None = value {
        println(1)
    } else {
        println(0)
    }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "1\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryBuiltinResultFieldConstructors(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let ok: Result<Int, String> = Result.Ok(42)
    let err: Result<Int, String> = Result.Err("x")
    println(1)
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "1\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryBuiltinResultConstructorsTrackLocalContext(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `struct Holder {
    ok: Result<Int, String>
    flag: Result<Bool, String>
}

fn wrap(value: Int) -> Result<Int, String> {
    return Result.Ok(value)
}

fn consume(value: Result<Bool, String>) -> Int {
    1
}

fn main() {
    let ok: Result<Int, String> = Result.Ok(42)
    let flag: Result<Bool, String> = Result.Ok(true)
    let holder = Holder { ok: Result.Err("bad"), flag: Result.Ok(true) }
    let wrapped = wrap(7)
    println(consume(Result.Ok(true)))
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "1\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryLetStructPatternDestructuring(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

struct Pair {
    first: Int
    second: Int
}

struct Bucket {
    pair: Pair
    items: List<String>
}

fn main() {
    let bucket @ Bucket {
        pair: Pair { first, second },
        items,
    } = Bucket {
        pair: Pair { first: 1, second: 2 },
        items: strings.Split("pear,apple", ","),
    }
    println(first)
    println(second)
    println(items.sorted()[0])
    println(bucket.pair.first)
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "1\n2\napple\n1\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

// TestLLVMBackendBinaryRunsGenericIdentity covers the generic
// monomorphization path Phase 1 introduced, all the way through clang
// to an executable. The monomorphizer must produce `_Z2idIlEl`, clang
// must link and run it, and the process must print the forwarded
// value. Complements the IR-only smoke in
// `internal/llvmgen/ir_module_test.go::TestGenerateModuleGenericIdentityMonomorphized`.
func TestLLVMBackendBinaryRunsGenericIdentity(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn id<T>(x: T) -> T { x }

fn main() {
    println(id::<Int>(42))
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "42\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

// TestLLVMBackendBinaryRunsInterfaceBoxingDispatch exercises the full
// Phase 6a-6e interface pipeline end-to-end: a struct's method set
// structurally satisfies an interface, the concrete value is boxed
// into a `%osty.iface` fat pointer at the `let` site, and the
// subsequent method call is lowered to a vtable indirect call that
// the linked binary actually executes. Complements the IR-only
// smokes `TestGenerateModuleInterfaceBoxingDispatch` and friends by
// confirming the emitted `insertvalue` / `extractvalue` / `load ptr`
// / indirect-call sequence survives clang and runs correctly.
func TestLLVMBackendBinaryRunsInterfaceBoxingDispatch(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `interface Sized {
    fn size(self) -> Int
}

struct Vec {
    count: Int,

    fn size(self) -> Int {
        self.count
    }
}

fn main() {
    let v = Vec { count: 3 }
    let s: Sized = v
    println(s.size())
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "3\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}
