package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/parser"
)

func parseLLVMGenFile(t *testing.T, src string) *ast.File {
	t.Helper()

	file, diags := parser.ParseDiagnostics([]byte(src))
	if len(diags) != 0 {
		t.Fatalf("ParseDiagnostics returned %d diagnostics: %v", len(diags), diags)
	}
	if file == nil {
		t.Fatal("ParseDiagnostics returned nil file")
	}
	return file
}

func TestGenerateRuntimeFFIUsesRuntimeABI(t *testing.T) {
	file := parseLLVMGenFile(t, `use runtime.strings as strings {
    fn HasPrefix(s: String, prefix: String) -> Bool
    fn Split(s: String, sep: String) -> List<String>
}

fn main() {
    let parts = strings.Split("osty,llvm", ",")
    if strings.HasPrefix("osty", "ost") {
        println(1)
    } else {
        println(0)
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/runtime_has_prefix.osty",
		Target:      "x86_64-unknown-linux-gnu",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, forbidden := range []string{
		"LLVM002",
		"Osty LLVM backend skeleton",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("generated IR still looks unsupported; found %q in:\n%s", forbidden, got)
		}
	}
	for _, want := range []string{
		"declare i1 @osty_rt_strings_HasPrefix(ptr, ptr)",
		"declare ptr @osty_rt_strings_Split(ptr, ptr)",
		"call ptr @osty_rt_strings_Split",
		"call i1 @osty_rt_strings_HasPrefix",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateUseCSurfaceLowersToExternC drives the full v0.5
// `use c "libname" { ... }` surface through parse → resolve → check →
// LLVM IR and verifies the desugared `runtime.cabi.<libname>` path
// emits literal extern C declarations and calls. This is the
// integration test for LANG_SPEC §12.8 surface syntax.
func TestGenerateUseCSurfaceLowersToExternC(t *testing.T) {
	file := parseLLVMGenFile(t, `use c "osty_demo" as demo {
    fn osty_demo_double(x: Int) -> Int
    fn osty_demo_is_zero(x: Int) -> Bool
}

fn main() {
    let doubled = demo.osty_demo_double(21)
    if demo.osty_demo_is_zero(doubled) {
        println(0)
    } else {
        println(doubled)
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/use_c_surface.osty",
		Target:      "x86_64-unknown-linux-gnu",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, forbidden := range []string{
		"@osty_rt_cabi_osty_demo_osty_demo_double",
		"LLVM001",
		"LLVM002",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("generated IR must not contain %q (use c desugars to literal extern C symbols):\n%s", forbidden, got)
		}
	}
	for _, want := range []string{
		"declare i64 @osty_demo_double(i64)",
		"declare i1 @osty_demo_is_zero(i64)",
		"call i64 @osty_demo_double(",
		"call i1 @osty_demo_is_zero(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateRuntimeCABIEmitsLiteralExternSymbol verifies that
// `use runtime.cabi.<lib>` paths bypass the `osty_rt_` namespace and
// declare the function name as the literal extern C symbol. This is
// the entry point for binding arbitrary C libraries from Osty: the
// link step is the user's responsibility (link the providing object /
// shared library via the manifest or build flags).
//
// The test sticks to the runtime ABI types the LLVM backend currently
// supports for FFI (Int / Bool); broader C numeric coverage (Int32,
// Float, etc.) is gated by separate type-system work.
func TestGenerateRuntimeCABIEmitsLiteralExternSymbol(t *testing.T) {
	file := parseLLVMGenFile(t, `use runtime.cabi.osty_demo as demo {
    fn osty_demo_double(x: Int) -> Int
    fn osty_demo_is_zero(x: Int) -> Bool
}

fn main() {
    let doubled = demo.osty_demo_double(21)
    if demo.osty_demo_is_zero(doubled) {
        println(0)
    } else {
        println(doubled)
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/runtime_cabi_demo.osty",
		Target:      "x86_64-unknown-linux-gnu",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, forbidden := range []string{
		"@osty_rt_cabi_osty_demo_osty_demo_double",
		"@osty_rt_cabi_osty_demo_osty_demo_is_zero",
		"LLVM001",
		"LLVM002",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("generated IR must not contain %q (cabi paths emit literal symbols):\n%s", forbidden, got)
		}
	}
	for _, want := range []string{
		"declare i64 @osty_demo_double(i64)",
		"declare i1 @osty_demo_is_zero(i64)",
		"call i64 @osty_demo_double(",
		"call i1 @osty_demo_is_zero(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateUnknownRuntimeFFIStillReportsLLVM002(t *testing.T) {
	file := parseLLVMGenFile(t, `use runtime.unknown as strings {
    fn HasPrefix(s: String, prefix: String) -> Bool
}

fn main() {
    if strings.HasPrefix("osty", "ost") {
        println(1)
    } else {
        println(0)
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/runtime_unknown.osty",
		Target:      "x86_64-unknown-linux-gnu",
	})
	if err == nil {
		t.Fatalf("Generate succeeded unexpectedly; got IR:\n%s", string(ir))
	}

	diag := UnsupportedDiagnosticForError(err)
	if got, want := diag.Code, "LLVM002"; got != want {
		t.Fatalf("diag.Code = %q, want %q", got, want)
	}
	if got, want := diag.Kind, "runtime-ffi"; got != want {
		t.Fatalf("diag.Kind = %q, want %q", got, want)
	}
	if got := diag.Message; !strings.Contains(got, "runtime.unknown") {
		t.Fatalf("diag.Message = %q, want it to mention runtime.unknown", got)
	}
}

func TestGenerateStringCompareUsesRuntimeABI(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    if "osty" != "llvm" {
        println(1)
    } else {
        println(0)
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/string_compare.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare i1 @osty_rt_strings_Equal(ptr, ptr)",
		"call i1 @osty_rt_strings_Equal",
		"xor i1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateNonStringPtrCompareRejected(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let a: List<Int> = [1, 2]
    let b: List<Int> = [3, 4]
    if a == b {
        println(1)
    } else {
        println(0)
    }
}
`)

	_, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/nonstr_compare.osty",
	})
	if err == nil {
		t.Fatal("Generate succeeded, want unsupported diagnostic for non-String ptr ==")
	}
	if got := err.Error(); !strings.Contains(got, "non-String ptr") {
		t.Fatalf("error = %q, want to mention non-String ptr", got)
	}
}

func TestGenerateLibraryModuleWithoutMain(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Kind {
    Manifest,
    Lockfile,
    Source,
    Support,
    Ignored,
}

pub fn kindName(kind: Kind) -> String {
    match kind {
        Manifest -> "manifest",
        Lockfile -> "lockfile",
        Source -> "source",
        Support -> "support",
        Ignored -> "ignored",
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/package_entry.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define ptr @kindName(i64 %kind)",
		"select i1",
		"manifest",
		"ignored",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateOpaqueRuntimeObjectTypes(t *testing.T) {
	file := parseLLVMGenFile(t, `pub struct RuntimeBag {
    items: List<String>
    callback: fn(String) -> Bool
    maybe: String?
}

pub fn keep(bag: RuntimeBag) -> RuntimeBag {
    bag
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/runtime_bag.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%RuntimeBag = type { ptr, ptr, ptr }",
		"define %RuntimeBag @keep(%RuntimeBag %bag)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateVoidFunctionAndCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn touch(value: Int) {
    println(value)
}

fn main() {
    touch(42)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/void_call.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define void @touch(i64 %value)",
		"call void @touch(i64 42)",
		"ret void",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateOpaqueListLiteralsUseRuntimeABI(t *testing.T) {
	file := parseLLVMGenFile(t, `fn empty() -> List<Int> {
    let empty: List<Int> = []
    empty
}

fn values() -> List<Int> {
    let values: List<Int> = [1, 2]
    values
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/list_literals.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"@osty_rt_list_",
		"call ptr @osty_rt_list_",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	if gotCount := strings.Count(got, "@osty_rt_list_"); gotCount < 2 {
		t.Fatalf("generated IR list runtime prefix count = %d, want at least 2:\n%s", gotCount, got)
	}
}

func TestGenerateListLenUsesRuntimeABI(t *testing.T) {
	file := parseLLVMGenFile(t, `fn size(items: List<Int>) -> Int {
    items.len()
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/list_len.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"@osty_rt_list_",
		"call i64 @osty_rt_list_",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateListPushStmtUsesRuntimeABI(t *testing.T) {
	file := parseLLVMGenFile(t, `fn append(mut values: List<Int>) {
    values.push(1)
    values.push(2)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/list_push.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"@osty_rt_list_",
		"call void @osty_rt_list_",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	if gotCount := strings.Count(got, "call void @osty_rt_list_"); gotCount < 2 {
		t.Fatalf("generated IR push call count = %d, want at least 2:\n%s", gotCount, got)
	}
}

func TestGenerateManagedListPushStmtUsesSafepoint(t *testing.T) {
	file := parseLLVMGenFile(t, `fn append(mut values: List<String>, item: String) {
    values.push(item)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/list_push_managed.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare void @osty.gc.safepoint_v1(i64, ptr, i64)",
		"call void @osty.gc.safepoint_v1(",
		"call void @osty_rt_list_push_ptr(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateForInOverListStringUsesRuntimeABI(t *testing.T) {
	file := parseLLVMGenFile(t, `fn visit(items: List<String>) {
    for item in items {
        println(item)
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/list_for_in.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"@osty_rt_list_",
		"br i1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateForInOverTemporaryManagedListUsesRootSlot(t *testing.T) {
	file := parseLLVMGenFile(t, `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

fn main() {
    for item in strings.Split("a,b", ",") {
        println(item)
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/list_for_in_temp.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"call ptr @osty_rt_strings_Split",
		"call void @osty.gc.root_bind_v1",
		"call void @osty.gc.safepoint_v1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateManagedTemporaryCallArgUsesRootSlot(t *testing.T) {
	file := parseLLVMGenFile(t, `use runtime.strings as strings {
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

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/call_arg_temp_root.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"call ptr @osty_rt_strings_Split",
		"call void @osty.gc.root_bind_v1",
		"call i64 @count(",
		"call i64 @take(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateCollectionsUseManagedRuntimeABI(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Pair {
    left: Int
    right: Int
}

fn main() {
    let mut pairs: Map<Int, Pair> = {:}
    pairs.insert(1, Pair { left: 2, right: 3 })
    let pair = pairs[1]
    let keys = pairs.keys().sorted()

    let mut values: List<Pair> = []
    values.push(Pair { left: pair.left, right: pair.right })
    let first = values[0]

    let empty: List<Int> = []
    let mut seen = empty.toSet()
    seen.insert(keys[0])
    let ids = seen.toList()

    println(first.left + first.right + ids[0])
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/collections_runtime.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"call ptr @osty_rt_map_new",
		"call void @osty_rt_map_insert_i64",
		"call void @osty_rt_map_get_or_abort_i64",
		"call ptr @osty_rt_map_keys",
		"call ptr @osty_rt_list_sorted_i64",
		"call void @osty_rt_list_push_bytes",
		"call void @osty_rt_list_get_bytes",
		"call ptr @osty_rt_list_to_set_i64",
		"call i1 @osty_rt_set_insert_i64",
		"call ptr @osty_rt_set_to_list",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateMapPureHelpersTypeCheckThroughFrontEnd(t *testing.T) {
	// Pure-Osty Map helpers (update/getOr/any/count/mergeWith/retainIf) are
	// defined in internal/stdlib/modules/collections.osty with Osty bodies on
	// top of the runtime intrinsics. The LLVM backend does not yet inline
	// Osty-bodied stdlib methods through Generate(); tracking that gap is a
	// separate work item. This test locks in a weaker invariant: a program
	// that calls each new helper must at least parse with zero diagnostics,
	// preventing grammar regressions in the call-site shape of the helpers
	// (2-arg closures, nested `??` in `update`, etc.).
	parseLLVMGenFile(t, `fn main() {
    let mut counts: Map<String, Int> = {:}
    counts.insert("a", 1)
    counts.update("a", |n| (n ?? 0) + 10)

    let fallback = counts.getOr("missing", 0)
    let atLeastTwo = counts.count(|_k, v| v >= 2)
    let hasA = counts.any(|k, _v| k == "a")
    let allPos = counts.all(|_k, v| v > 0)
    let firstBig = counts.find(|_k, v| v > 100)

    let kept = counts.filter(|_k, v| v > 0)
    let labeled = kept.mapValues(|v| v * 2)

    let other: Map<String, Int> = {:}
    let _merged = counts.merge(other)
    let _summed = counts.mergeWith(other, |x, y| x + y)

    counts.insertAll(other)
    counts.retainIf(|_k, v| v > 5)
    counts.forEach(|_k, _v| {})

    if hasA && allPos {
        println(fallback + atLeastTwo + labeled.len())
    }
}

fn demoGroupBy(xs: List<Int>) -> Map<Bool, List<Int>> {
    xs.groupBy(|n| n >= 0)
}
`)
}

func TestGenerateCollectionsEmitTraceHelpersForManagedAggregateValues(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Bucket {
    ids: List<Int>
}

fn main() {
    let ids: List<Int> = [7]
    let mut buckets: Map<String, Bucket> = {:}
    buckets.insert("root", Bucket { ids: ids })
    let bucket = buckets["root"]
    println(bucket.ids[0])
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/collections_trace_runtime.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"call ptr @osty_rt_map_new",
		"call void @osty_rt_map_insert_string",
		"call void @osty_rt_map_get_or_abort_string",
		"ptr @osty_rt_trace_",
		"define void @osty_rt_trace_",
		"call i64 @osty_rt_list_get_i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
