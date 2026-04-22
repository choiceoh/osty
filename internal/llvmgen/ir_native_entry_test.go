package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	ostyir "github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

func lowerNativeEntryModule(t *testing.T, src string) *ostyir.Module {
	t.Helper()
	file := parseLLVMGenFile(t, src)
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.File(file, res, check.Opts{
		UseGolegacy:   true,
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
	})
	mod, issues := ostyir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower issues: %v", issues)
	}
	if validateErrs := ostyir.Validate(mod); len(validateErrs) != 0 {
		t.Fatalf("ir.Validate errors: %v", validateErrs)
	}
	return mod
}

func renderNativeOwnedModuleText(nativeMod *llvmNativeModule) string {
	if nativeMod == nil {
		return ""
	}
	out := []byte(llvmNativeEmitModule(nativeMod))
	return string(withDataLayout(out, nativeMod.target))
}

func TestTryGenerateNativeOwnedModuleAddsHostTargetHeader(t *testing.T) {
	mod := lowerNativeEntryModule(t, `fn main() { println(1) }`)
	out, ok, err := TryGenerateNativeOwnedModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_entry_host_target.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule returned error: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported unsupported for primitive main")
	}
	wantTarget := CanonicalLLVMTarget("")
	got := string(out)
	if !strings.Contains(got, `target triple = "`+wantTarget+`"`) {
		t.Fatalf("native-owned IR missing canonical host target %q:\n%s", wantTarget, got)
	}
	if !strings.Contains(got, `target datalayout = "`) {
		t.Fatalf("native-owned IR missing target datalayout:\n%s", got)
	}
}

func TestNativeOwnedModuleVectorizedScalarListLoopEmitsLoopMetadata(t *testing.T) {
	src := `#[vectorize]
fn sum(xs: List<Int>) -> Int {
    let mut out = 0
    for i in 0..xs.len() {
        out = out + xs[i]
    }
    out
}

fn main() {
    println(sum([1, 2, 3, 4]))
}
`
	mod := lowerNativeEntryModule(t, src)
	nativeMod, ok := nativeModuleFromIR(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_entry_vector_loop.osty",
	})
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for vectorized scalar list loop")
	}
	got := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"!llvm.loop !",
		`!"llvm.loop.vectorize.enable", i1 true`,
		`!"llvm.loop.parallel_accesses",`,
		`!llvm.access.group !`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("native-owned loop IR missing %q:\n%s", want, got)
		}
	}
}

func TestNativeOwnedModuleEntryPrimitiveSlice(t *testing.T) {
	src := `fn pick(flag: Bool) -> Int {
    if flag {
        42
    } else {
        0
    }
}

fn main() {
    let mut i = 0
    let mut sum = 0
    for i < 3 {
        sum = sum + pick(i == 2)
        i = i + 1
    }
    println(sum)
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_slice.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for primitive slice")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"define i64 @pick(i1 %flag)",
		"phi i64",
		"for.cond",
		"call i64 @pick(i1",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestTryGenerateNativeOwnedModulePrimitiveSlice(t *testing.T) {
	src := `fn pick(flag: Bool) -> Int {
    if flag {
        42
    } else {
        0
    }
}

fn main() {
    let mut i = 0
    let mut sum = 0
    for i < 3 {
        sum = sum + pick(i == 2)
        i = i + 1
    }
    println(sum)
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_try.osty"}
	out, ok, err := TryGenerateNativeOwnedModule(mod, opts)
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule returned error: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported not covered for primitive slice")
	}
	for _, want := range []string{
		"define i64 @pick(i1 %flag)",
		"phi i64",
		"for.cond",
		"call i64 @pick(i1",
	} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("native-owned helper IR missing %q:\n%s", want, string(out))
		}
	}
	generated, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(generated) != string(out) {
		t.Fatalf("TryGenerateNativeOwnedModule diverged from GenerateModule\n--- try ---\n%s\n--- generate ---\n%s", string(out), string(generated))
	}
}

func TestNativeOwnedModuleEntryStructSlice(t *testing.T) {
	src := `struct Pair { left: Int, right: Int }

fn main() {
    let pair = Pair { left: 1, right: 2 }
    println(pair.left)
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_script.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for plain struct slice")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"%Pair = type { i64, i64 }",
		"insertvalue %Pair",
		"extractvalue %Pair",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned struct IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned struct entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryStructMethodSlice(t *testing.T) {
	src := `struct Pair {
    left: Int,
    right: Int,

    fn total(self) -> Int {
        self.left + self.right
    }
}

fn main() {
    let pair = Pair { left: 1, right: 2 }
    println(pair.total())
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_struct_method.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for plain struct method slice")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"define i64 @Pair__total(%Pair %self)",
		"call i64 @Pair__total(%Pair",
		"extractvalue %Pair",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned struct-method IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned struct-method entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryMutSelfMethodSlice(t *testing.T) {
	src := `struct Counter {
    value: Int,

    fn bump(mut self) {
        self.value = self.value + 1
    }
}

fn main() {
    let mut c = Counter { value: 1 }
    c.bump()
    println(c.value)
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_mut_self_method.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for mut self method slice")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"define void @Counter__bump(ptr %self)",
		"call void @Counter__bump(ptr",
		"load %Counter, ptr",
		"store %Counter",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned mut-self IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned mut-self entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryMutSelfProjectedReceiverLocal(t *testing.T) {
	src := `struct Counter {
    value: Int,

    fn bump(mut self) {
        self.value = self.value + 1
    }
}

struct Box {
    counter: Counter,
}

fn main() {
    let mut box = Box { counter: Counter { value: 1 } }
    box.counter.bump()
    println(box.counter.value)
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_mut_self_projected_local.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for projected local mut self receiver")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"alloca %Counter",
		"call void @Counter__bump(ptr",
		"insertvalue %Box",
		"store %Box",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned projected local mut-self IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned projected local mut-self entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryMutSelfProjectedReceiverParam(t *testing.T) {
	src := `struct Counter {
    value: Int,

    fn bump(mut self) {
        self.value = self.value + 1
    }
}

struct Box {
    counter: Counter,
}

fn run(mut box: Box) {
    box.counter.bump()
    println(box.counter.value)
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_mut_self_projected_param.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for projected param mut self receiver")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"define void @run(%Box %box)",
		"alloca %Box",
		"alloca %Counter",
		"call void @Counter__bump(ptr",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned projected param mut-self IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned projected param mut-self entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryRejectsMutSelfOnNonAddressableReceiver(t *testing.T) {
	src := `struct Counter {
    value: Int,

    fn bump(mut self) {
        self.value = self.value + 1
    }
}

struct Box {
    counter: Counter,
}

fn makeBox() -> Box {
    Box { counter: Counter { value: 1 } }
}

fn main() {
    makeBox().counter.bump()
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_mut_self_non_addressable.osty"}
	if nativeMod, ok := nativeModuleFromIR(mod, opts); ok {
		t.Fatalf("nativeModuleFromIR unexpectedly accepted non-addressable mut self receiver: %#v", nativeMod)
	}
	out, err := GenerateModule(mod, opts)
	if err == nil {
		t.Fatalf("GenerateModule unexpectedly accepted non-addressable mut self receiver:\n%s", string(out))
	}
	// Legacy HIR path catches the non-addressable `mut self` receiver
	// with a specific diagnostic; the native-owned path catches the
	// same case earlier by rejecting the module. When legacy is
	// retired, the generic "did not cover module" message is the
	// only one left — accept either shape so this assertion keeps
	// working across the migration.
	msg := err.Error()
	if !strings.Contains(msg, "mut receiver for \"bump\"") &&
		!strings.Contains(msg, "native-owned emitter did not cover module") {
		t.Fatalf("GenerateModule error missing remaining wall, got: %v", err)
	}
}

func TestNativeOwnedModuleEntryStructFieldAssignLocal(t *testing.T) {
	src := `struct Pair { left: Int, right: Int }

fn main() {
    let mut pair = Pair { left: 1, right: 2 }
    pair.left = 3
    println(pair.left)
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_struct_assign.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for local struct field assignment")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"%Pair = type { i64, i64 }",
		"store %Pair",
		"insertvalue %Pair",
		"extractvalue %Pair",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned field-assign IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned field-assign entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryNestedStructFieldAssignLocal(t *testing.T) {
	src := `struct Inner { value: Int }
struct Outer { inner: Inner }

fn main() {
    let mut outer = Outer { inner: Inner { value: 1 } }
    outer.inner.value = 3
    println(outer.inner.value)
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_nested_struct_assign.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for nested local struct field assignment")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	if strings.Count(direct, "extractvalue") < 2 {
		t.Fatalf("native-owned nested field-assign IR missing chained extractvalue ops:\n%s", direct)
	}
	if strings.Count(direct, "insertvalue") < 3 {
		t.Fatalf("native-owned nested field-assign IR missing chained insertvalue ops:\n%s", direct)
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned nested field-assign entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryStructFieldAssignParam(t *testing.T) {
	src := `struct Pair { left: Int, right: Int }

fn bump(mut pair: Pair) {
    pair.left = 3
    println(pair.left)
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_param_struct_assign.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for param struct field assignment")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"define void @bump(%Pair %pair)",
		"alloca %Pair",
		"store %Pair %pair",
		"insertvalue %Pair",
		"extractvalue %Pair",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned param field-assign IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned param field-assign entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryStructFieldAssignGlobal(t *testing.T) {
	src := `struct Point { x: Int, y: Int }

pub let mut ORIGIN: Point = Point { x: 1, y: 2 }

fn main() {
    ORIGIN.x = 3
    println(ORIGIN.x)
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_global_struct_assign.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for global struct field assignment")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"@osty_global_ORIGIN = internal global %Point { i64 1, i64 2 }",
		"load %Point, ptr @osty_global_ORIGIN",
		"insertvalue %Point",
		"store %Point",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned global field-assign IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned global field-assign entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryGlobalLetSlice(t *testing.T) {
	src := `pub let MAX_USERS: Int = 10000

fn limit() -> Int {
    MAX_USERS
}

fn main() {
    println(limit())
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_global_let.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for top-level global let")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"@osty_global_MAX_USERS = internal constant i64 10000",
		"define i64 @limit()",
		"load i64, ptr @osty_global_MAX_USERS",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned global-let IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned global-let entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryGlobalStringLetSlice(t *testing.T) {
	src := `pub let DEFAULT_ERROR: String = "broken"

fn main() {
    println(DEFAULT_ERROR)
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_global_string_let.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for top-level global string let")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"@osty_global_DEFAULT_ERROR = internal constant ptr @.str0",
		"load ptr, ptr @osty_global_DEFAULT_ERROR",
		"@.str0 = private unnamed_addr constant",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned global-string-let IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned global-string-let entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryGlobalStructLetSlice(t *testing.T) {
	src := `struct Point { x: Int, y: Int }

pub let ORIGIN: Point = Point { x: 0, y: 0 }

fn main() {
    println(ORIGIN.x)
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_global_struct_let.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for top-level global struct let")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"%Point = type { i64, i64 }",
		"@osty_global_ORIGIN = internal constant %Point { i64 0, i64 0 }",
		"load %Point, ptr @osty_global_ORIGIN",
		"extractvalue %Point",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned global-struct-let IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned global-struct-let entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryStringMethodBatch(t *testing.T) {
	src := `fn describe(s: String) -> Int {
    let parts = s.trim().trimPrefix("[").trimSuffix("]").split(",")
    let chars = s.chars()
    let bytes = s.bytes()
    println(",".join(parts))
    println(s.repeat(2))
    println(s.startsWith("["))
    println(s.endsWith("]"))
    println(s.contains(","))
    println(s.lines().len())
    chars.len() + bytes.len() + s.charCount()
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_string_methods.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for string method batch")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"define i64 @describe(ptr %s)",
		"declare ptr @osty_rt_strings_TrimSpace(ptr)",
		"declare ptr @osty_rt_strings_Join(ptr, ptr)",
		"declare ptr @osty_rt_strings_Split(ptr, ptr)",
		"declare ptr @osty_rt_strings_Chars(ptr)",
		"declare i64 @osty_rt_list_len(ptr)",
		"call ptr @osty_rt_strings_Join(ptr",
		"call i64 @osty_rt_list_len(ptr",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned string-method IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned string-method entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryListSetMethodBatch(t *testing.T) {
	src := `fn touch(words: List<String>) -> Int {
    let mut seen = words.toSet()
    seen.insert("z")
    seen.remove("skip")
    if seen.contains("z") {
        seen.toList().sorted().len()
    } else {
        0
    }
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_list_set_methods.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for list/set method batch")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"define i64 @touch(ptr %words)",
		"declare ptr @osty_rt_list_to_set_string(ptr)",
		"declare i1 @osty_rt_set_insert_string(ptr, ptr)",
		"declare ptr @osty_rt_set_to_list(ptr)",
		"declare ptr @osty_rt_list_sorted_string(ptr)",
		"call ptr @osty_rt_list_to_set_string(ptr",
		"call i1 @osty_rt_set_contains_string(ptr",
		"call ptr @osty_rt_set_to_list(ptr",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned list/set-method IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned list/set-method entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryListPushInsertBatch(t *testing.T) {
	src := `fn build(mut xs: List<Int>) -> Int {
    xs.push(3)
    xs.insert(0, 1)
    xs.sorted().len()
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_list_push_insert.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for list push/insert batch")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"define i64 @build(ptr %xs)",
		"declare void @osty_rt_list_push_i64(ptr, i64)",
		"declare void @osty_rt_list_insert_i64(ptr, i64, i64)",
		"declare ptr @osty_rt_list_sorted_i64(ptr)",
		"call void @osty_rt_list_push_i64(ptr",
		"call void @osty_rt_list_insert_i64(ptr",
		"call ptr @osty_rt_list_sorted_i64(ptr",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned list push/insert IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned list push/insert entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryMapMethodBatch(t *testing.T) {
	src := `fn touch(mut counts: Map<String, Int>) -> Int {
    counts.insert("a", 1)
    counts.insert("b", 2)
    counts.remove("missing")
    let keys = counts.keys().sorted()
    if counts.containsKey("a") {
        keys.len()
    } else {
        0
    }
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_map_methods.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for map method batch")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"define i64 @touch(ptr %counts)",
		"declare void @osty_rt_map_insert_string(ptr, ptr, ptr)",
		"declare ptr @osty_rt_map_keys(ptr)",
		"declare i1 @osty_rt_map_contains_string(ptr, ptr)",
		"alloca i64",
		"call void @osty_rt_map_insert_string(ptr",
		"call ptr @osty_rt_map_keys(ptr",
		"call i1 @osty_rt_map_contains_string(ptr",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned map-method IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned map-method entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryListLiteralAndIndexBatch(t *testing.T) {
	src := `fn build() -> Int {
    let xs = [1, 2, 3]
    xs[1]
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_list_literal_index.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for list literal/index batch")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"define i64 @build()",
		"declare ptr @osty_rt_list_new()",
		"declare void @osty_rt_list_push_i64(ptr, i64)",
		"declare i64 @osty_rt_list_get_i64(ptr, i64)",
		"call ptr @osty_rt_list_new()",
		"call void @osty_rt_list_push_i64(ptr",
		"call i64 @osty_rt_list_get_i64(ptr",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned list literal/index IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned list literal/index entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryListStructBytesBatch(t *testing.T) {
	src := `struct Point { x: Int, y: Int }

fn build() -> Int {
    let xs = [Point { x: 1, y: 2 }]
    xs[0].x
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_list_struct_bytes.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for list struct bytes batch")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"%Point = type { i64, i64 }",
		"declare void @osty_rt_list_push_bytes_v1(ptr, ptr, i64)",
		"declare void @osty_rt_list_get_bytes_v1(ptr, i64, ptr, i64)",
		"call void @osty_rt_list_push_bytes_v1(",
		"call void @osty_rt_list_get_bytes_v1(",
		"getelementptr %Point, ptr null, i32 1",
		"extractvalue %Point",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned list struct bytes IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned list struct bytes entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryMapIndexBatch(t *testing.T) {
	src := `fn lookup(counts: Map<String, Int>) -> Int {
    counts["a"]
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_map_index.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for map index batch")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"define i64 @lookup(ptr %counts)",
		"declare void @osty_rt_map_get_or_abort_string(ptr, ptr, ptr)",
		"alloca i64",
		"call void @osty_rt_map_get_or_abort_string(ptr",
		"load i64, ptr",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned map index IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned map index entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryTupleBatch(t *testing.T) {
	src := `fn pack() -> Int {
    let t = (1, 2)
    t.0 + t.1
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_tuple_batch.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for tuple batch")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"%Tuple.i64.i64 = type { i64, i64 }",
		"insertvalue %Tuple.i64.i64 undef, i64 1, 0",
		"extractvalue %Tuple.i64.i64",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned tuple IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned tuple entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryTupleParamBatch(t *testing.T) {
	src := `fn first(p: (Int, String)) -> Int {
    p.0
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_tuple_param.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for tuple param batch")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"%Tuple.i64.ptr = type { i64, ptr }",
		"define i64 @first(%Tuple.i64.ptr %p)",
		"extractvalue %Tuple.i64.ptr",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned tuple param IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned tuple param entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryMapLiteralStructBatch(t *testing.T) {
	src := `struct Point { x: Int, y: Int }

fn build() -> Int {
    let points = {"p": Point { x: 1, y: 2 }}
    points["p"].x
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_map_literal_struct.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for map literal struct batch")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"%Point = type { i64, i64 }",
		"declare ptr @osty_rt_map_new(i64, i64, i64, ptr)",
		"declare void @osty_rt_map_insert_string(ptr, ptr, ptr)",
		"declare void @osty_rt_map_get_or_abort_string(ptr, ptr, ptr)",
		"call ptr @osty_rt_map_new(",
		"call void @osty_rt_map_insert_string(ptr",
		"alloca %Point",
		"call void @osty_rt_map_get_or_abort_string(ptr",
		"extractvalue %Point",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned map literal struct IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned map literal struct entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryMutSelfProjectedReceiverGlobal(t *testing.T) {
	src := `struct Counter {
    value: Int,

    fn bump(mut self) {
        self.value = self.value + 1
    }
}

struct Box {
    counter: Counter,
}

pub let mut STATE: Box = Box { counter: Counter { value: 1 } }

fn main() {
    STATE.counter.bump()
    println(STATE.counter.value)
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_mut_self_projected_global.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for projected global mut self receiver")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"@osty_global_STATE = internal global %Box",
		"load %Box, ptr @osty_global_STATE",
		"call void @Counter__bump(ptr",
		"store %Box",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned projected global mut-self IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned projected global mut-self entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestTryGenerateNativeOwnedModuleCoversStructFieldAssign(t *testing.T) {
	src := `struct Pair { left: Int, right: Int }

fn main() {
    let mut pair = Pair { left: 1, right: 2 }
    pair.left = 3
    println(pair.left)
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_try_struct_assign.osty"}
	out, ok, err := TryGenerateNativeOwnedModule(mod, opts)
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule returned error: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported not covered for struct field assignment")
	}
	got := string(out)
	for _, want := range []string{
		"%Pair = type { i64, i64 }",
		"extractvalue %Pair",
		"insertvalue %Pair",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("native-owned IR missing %q:\n%s", want, got)
		}
	}
}

func TestTryGenerateNativeOwnedModuleCoversNestedStructFieldAssign(t *testing.T) {
	src := `struct Inner { value: Int }

struct Outer { inner: Inner, flag: Int }

fn main() {
    let mut outer = Outer { inner: Inner { value: 1 }, flag: 2 }
    outer.inner.value = 3
    println(outer.inner.value)
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_try_nested_struct_assign.osty"}
	out, ok, err := TryGenerateNativeOwnedModule(mod, opts)
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule returned error: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported not covered for nested struct field assignment")
	}
	got := string(out)
	for _, want := range []string{
		"%Inner = type { i64 }",
		"%Outer = type { %Inner, i64 }",
		"extractvalue %Outer",
		"insertvalue %Inner",
		"insertvalue %Outer",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("nested native-owned IR missing %q:\n%s", want, got)
		}
	}
}

func TestTryGenerateNativeOwnedModuleCoversListLiteralAndIndex(t *testing.T) {
	src := `fn main() {
    let xs = [1, 2]
    println(xs[0])
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_try_list_index.osty"}
	out, ok, err := TryGenerateNativeOwnedModule(mod, opts)
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule returned error: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported not covered for list index")
	}
	got := string(out)
	for _, want := range []string{
		"declare ptr @osty_rt_list_new()",
		"call ptr @osty_rt_list_new()",
		"call void @osty_rt_list_push_i64(",
		"call i64 @osty_rt_list_get_i64(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("list native-owned IR missing %q:\n%s", want, got)
		}
	}
}

func TestTryGenerateNativeOwnedModuleAppliesExportAndCABI(t *testing.T) {
	mod := &ostyir.Module{
		Package: "main",
		Decls: []ostyir.Decl{
			&ostyir.FnDecl{
				Name:         "native_entry_v1",
				ExportSymbol: "osty.gc.native_entry_v1",
				CABI:         true,
				Return:       &ostyir.PrimType{Kind: ostyir.PrimInt},
				Body: &ostyir.Block{
					Result: &ostyir.IntLit{Text: "0"},
				},
			},
		},
	}
	out, ok, err := TryGenerateNativeOwnedModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_entry_export_cabi.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule returned error: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported not covered for exported C ABI function")
	}
	got := string(out)
	if !strings.Contains(got, "define ccc i64 @native_entry_v1()") {
		t.Fatalf("native-owned helper IR missing C ABI calling convention:\n%s", got)
	}
	if !strings.Contains(got, "@osty.gc.native_entry_v1 = dso_local alias ptr, ptr @native_entry_v1") {
		t.Fatalf("native-owned helper IR missing export alias:\n%s", got)
	}
}

func TestNativeOwnedModuleEntryOptionalMethodBatch(t *testing.T) {
	src := `fn flags(name: String?) -> Bool {
    name.isSome() && !name.isNone()
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_optional_methods.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for optional method batch")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"define i1 @flags(ptr %name)",
		"icmp ne ptr",
		"icmp eq ptr",
		"xor i1",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned optional-method IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned optional-method entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryOptionalCoalesceStringBatch(t *testing.T) {
	src := `fn display(name: String?) -> String {
    name ?? "anon"
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_optional_coalesce_string.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for optional string coalesce batch")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"define ptr @display(ptr %name)",
		"icmp eq ptr",
		"phi ptr",
		"@.str0 = private unnamed_addr constant",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned optional-string-coalesce IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned optional-string-coalesce entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryOptionalCoalesceScalarBatch(t *testing.T) {
	src := `fn score(value: Int?) -> Int {
    value ?? 7
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_optional_coalesce_scalar.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for optional scalar coalesce batch")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"define i64 @score(ptr %value)",
		"icmp eq ptr",
		"load i64, ptr %t",
		"phi i64",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned optional-scalar-coalesce IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned optional-scalar-coalesce entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryOptionalQuestionScalarBatch(t *testing.T) {
	src := `fn requireScore(score: Int?) -> String? {
    let value = score?
    println(value)
    "ok"
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_optional_question_scalar.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for optional scalar question batch")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"define ptr @requireScore(ptr %score)",
		"ret ptr null",
		"load i64, ptr %t",
		"call ptr @osty_rt_int_to_string(i64",
		"call void @osty_rt_io_write(ptr",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned optional-scalar-question IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned optional-scalar-question entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryOptionalQuestionStructBatch(t *testing.T) {
	src := `struct Profile { name: String }

fn requireName(profile: Profile?) -> String? {
    let value = profile?
    value.name
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_optional_question_struct.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for optional struct question batch")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"%Profile = type { ptr }",
		"define ptr @requireName(ptr %profile)",
		"ret ptr null",
		"load %Profile, ptr %t",
		"extractvalue %Profile",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned optional-struct-question IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned optional-struct-question entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}

func TestNativeOwnedModuleEntryOptionalFieldBatch(t *testing.T) {
	src := `struct Profile { name: String }

fn maybeName(profile: Profile?) -> String? {
    profile?.name
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_optional_field.osty"}
	nativeMod, ok := nativeModuleFromIR(mod, opts)
	if !ok {
		t.Fatal("nativeModuleFromIR returned unsupported for optional field batch")
	}
	direct := renderNativeOwnedModuleText(nativeMod)
	for _, want := range []string{
		"%Profile = type { ptr }",
		"define ptr @maybeName(ptr %profile)",
		"load %Profile, ptr %t",
		"extractvalue %Profile",
		"phi ptr",
	} {
		if !strings.Contains(direct, want) {
			t.Fatalf("native-owned optional-field IR missing %q:\n%s", want, direct)
		}
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	if string(out) != direct {
		t.Fatalf("GenerateModule diverged from native-owned optional-field entrypoint\n--- direct ---\n%s\n--- generate ---\n%s", direct, string(out))
	}
}
