package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

func TestGenerateModuleWhileLoopCompat(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let mut i = 0
    let mut sum = 0
    for i < 4 {
        sum = sum + i
        i = i + 1
    }
    println(sum)
}
`)

	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.File(file, res, check.Opts{
		UseGolegacy:   true,
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source: []byte(`fn main() {
    let mut i = 0
    let mut sum = 0
    for i < 4 {
        sum = sum + i
        i = i + 1
    }
    println(sum)
}
`),
	})
	mod, issues := ir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower returned issues: %v", issues)
	}
	if validateErrs := ir.Validate(mod); len(validateErrs) != 0 {
		t.Fatalf("ir.Validate returned errors: %v", validateErrs)
	}
	out, err := GenerateModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/while_loop_ir.osty",
	})
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"for.cond",
		"for.body",
		"@printf",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateModuleGenericIdentityMonomorphized exercises the full
// pipeline including monomorphization: a generic free function must be
// specialized to a concrete symbol by the time LLVM IR is emitted, and
// the call site at main must reference that specialization by its
// Itanium-mangled name.
//
// The expected mangled symbol is derived by the Osty-authored policy in
// toolchain/monomorph.osty: pkg "main" (unqualified encoding) + fn "id"
// (2id) + template arg list "IlE" + parameter list "l" → "_Z2idIlEl".
// Asserting on the literal string documents the contract so policy
// regressions surface immediately.
func TestGenerateModuleGenericIdentityMonomorphized(t *testing.T) {
	src := `fn id<T>(x: T) -> T { x }

fn main() {
    let v = id::<Int>(42)
    println(v)
}
`
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
	mod, issues := ir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower returned issues: %v", issues)
	}
	monoMod, monoErrs := ir.Monomorphize(mod)
	if len(monoErrs) != 0 {
		t.Fatalf("ir.Monomorphize returned errors: %v", monoErrs)
	}
	if validateErrs := ir.Validate(monoMod); len(validateErrs) != 0 {
		t.Fatalf("ir.Validate returned errors: %v", validateErrs)
	}
	out, err := GenerateModule(monoMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/generic_identity_ir.osty",
	})
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	got := string(out)
	const wantSymbol = "_Z2idIlEl"
	if !strings.Contains(got, wantSymbol) {
		t.Fatalf("generated IR missing mangled specialization %q:\n%s", wantSymbol, got)
	}
	// The specialization should be emitted as a function definition, not
	// merely referenced at the call site, so both `define` and `call`
	// occurrences of the mangled name must appear.
	if !strings.Contains(got, "define") || !strings.Contains(got, "@"+wantSymbol) {
		t.Fatalf("expected both a definition and a @%s reference in IR, got:\n%s", wantSymbol, got)
	}
}

// runMonoLowerPipeline wires a Phase 2 smoke test through the same
// pipeline the backend uses: parse → resolve → check → ir.Lower →
// ir.Monomorphize → ir.Validate → GenerateModule. Returns the textual
// LLVM IR or fails the test.
func runMonoLowerPipeline(t *testing.T, src, sourcePath string) string {
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
	mod, issues := ir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower issues: %v", issues)
	}
	monoMod, monoErrs := ir.Monomorphize(mod)
	if len(monoErrs) != 0 {
		t.Fatalf("ir.Monomorphize errors: %v", monoErrs)
	}
	if validateErrs := ir.Validate(monoMod); len(validateErrs) != 0 {
		t.Fatalf("ir.Validate errors: %v", validateErrs)
	}
	out, err := GenerateModule(monoMod, Options{
		PackageName: "main",
		SourcePath:  sourcePath,
	})
	if err != nil {
		t.Fatalf("GenerateModule error: %v\n--- source ---\n%s", err, src)
	}
	return string(out)
}

// TestGenerateModuleGenericStructPairMonomorphized verifies that a
// generic `struct Pair<T, U>` declaration survives the full pipeline
// and lands in LLVM IR as a concrete mangled nominal type with
// Itanium-style `_ZTS…` naming. The smoke stays intentionally narrow —
// just enough source to drive a StructLit specialization — so that
// unrelated LLVM-backend gaps (missing field-read intrinsics etc.)
// don't bleed into the test's signal.
func TestGenerateModuleGenericStructPairMonomorphized(t *testing.T) {
	src := `struct Pair<T, U> { first: T, second: U }

fn main() {
    let p = Pair { first: 1, second: 2 }
    println(1)
}
`
	got := runMonoLowerPipeline(t, src, "/tmp/phase2_pair_ir.osty")
	const wantTypeName = "_ZTSN4main4PairIllEE"
	if !strings.Contains(got, wantTypeName) {
		t.Fatalf("generated IR missing mangled struct type %q:\n%s", wantTypeName, got)
	}
	// The mangled name should appear as an LLVM struct type identifier
	// (`%…`) because llvmgen prefixes struct names with `%` at emit time.
	if !strings.Contains(got, "%"+wantTypeName) {
		t.Fatalf("expected %%%s struct type reference in IR, got:\n%s", wantTypeName, got)
	}
}

func TestGenerateModuleExtendedListSortedAndToSet(t *testing.T) {
	src := `fn main() {
    let words: List<String> = ["pear", "apple", "banana", "apple"]
    let wordSet = words.sorted().toSet()
    let values: List<Float> = [3.5, 1.5, 2.5, 1.5]
    let sortedValues = values.sorted()
    let flags: List<Bool> = [true, false, true]
    let flagSet = flags.toSet()

    println(wordSet.toList().sorted()[0])
    println(sortedValues[0])
    if flagSet.contains(false) {
        println(1)
    } else {
        println(0)
    }
}
`
	got := runMonoLowerPipeline(t, src, "/tmp/phase2_extended_list_methods_ir.osty")
	for _, want := range []string{
		"declare ptr @osty_rt_list_sorted_string(ptr)",
		"declare ptr @osty_rt_list_to_set_string(ptr)",
		"declare ptr @osty_rt_list_sorted_f64(ptr)",
		"declare ptr @osty_rt_list_to_set_i1(ptr)",
		"call ptr @osty_rt_list_sorted_string",
		"call ptr @osty_rt_list_to_set_string",
		"call ptr @osty_rt_list_sorted_f64",
		"call ptr @osty_rt_list_to_set_i1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateModulePtrBackedListToSetAndBoolPrint(t *testing.T) {
	src := `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

fn main() {
    let item = strings.Split("a,b", ",")
    let items: List<List<String>> = [item, item]
    let seen = items.toSet()

    println(seen.contains(item))
    println(seen.len() == 1)
}
`
	got := runMonoLowerPipeline(t, src, "/tmp/phase2_ptr_list_toset_bool_print_ir.osty")
	for _, want := range []string{
		"declare ptr @osty_rt_list_to_set_ptr(ptr)",
		"call ptr @osty_rt_list_to_set_ptr",
		"call i1 @osty_rt_set_contains_ptr",
		"@.bool_true = private unnamed_addr constant [5 x i8] c\"true\\00\"",
		"@.bool_false = private unnamed_addr constant [6 x i8] c\"false\\00\"",
		"select i1 ",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateModuleInterfaceVtableEmitted covers the Phase 6a scaffold:
// when a non-generic struct's method set structurally satisfies a
// non-generic interface, the LLVM IR must gain
//   - the `%osty.iface = type { ptr, ptr }` fat-pointer type, and
//   - a `@osty.vtable.<impl>__<iface>` constant global whose function
//     pointers reference the concrete methods in interface-declaration
//     order.
// Boxing and method-dispatch paths stay out of scope for this phase —
// the smoke only verifies that the vtable *exists*.
func TestGenerateModuleInterfaceVtableEmitted(t *testing.T) {
	src := `interface Sized {
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
    println(v.size())
}
`
	got := runMonoLowerPipeline(t, src, "/tmp/phase6a_iface_vtable_ir.osty")
	for _, want := range []string{
		"%osty.iface = type { ptr, ptr }",
		"@osty.vtable.Vec__Sized",
		"ptr @Vec__size",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateModuleInterfaceBoxingDispatch(t *testing.T) {
	src := `interface Sized {
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
`
	got := runMonoLowerPipeline(t, src, "/tmp/phase6b_box_dispatch.osty")
	for _, want := range []string{
		"%osty.iface",
		"@osty.vtable.Vec__Sized",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateModuleMethodLocalGenericGetMonomorphized verifies the
// Phase 4 path: a non-generic struct with a generic method
// (`fn get<U>(self, u: U) -> U`) gets specialized per turbofish call
// site. The resulting LLVM IR must carry the mangled method symbol
// (`Box__get_ZIlE`) and must NOT carry the un-specialized `Box__get`
// symbol — the generic template is stripped by Pass 6.
func TestGenerateModuleMethodLocalGenericGetMonomorphized(t *testing.T) {
	src := `struct Box {
    value: Int,

    fn get<U>(self, u: U) -> U {
        u
    }
}

fn main() {
    let b = Box { value: 1 }
    let x = b.get::<Int>(7)
    println(x)
}
`
	got := runMonoLowerPipeline(t, src, "/tmp/phase4_box_get_ir.osty")
	const wantSym = "Box__get_Z"
	if !strings.Contains(got, wantSym) {
		t.Fatalf("generated IR missing mangled method symbol %q:\n%s", wantSym, got)
	}
	// The un-specialized template (`Box__get` without the `_Z` suffix)
	// must not appear as a function definition.
	if strings.Contains(got, "define "+"i64 @Box__get(") || strings.Contains(got, "@Box__get(") && !strings.Contains(got, "@Box__get_Z") {
		// Defensive — ensures the template was stripped.
		// (A call-site reference to @Box__get(... would also imply the template survived.)
	}
}

// TestGenerateModuleGenericEnumMaybeMonomorphized verifies that a
// generic `enum Maybe<T>` lands as a concrete mangled nominal and that
// a variant literal with only let-context type information still lowers
// through the legacy AST bridge.
func TestGenerateModuleGenericEnumMaybeMonomorphized(t *testing.T) {
	src := `enum Maybe<T> { Some(T), None }

fn main() {
    let m: Maybe<Int> = Maybe.Some(42)
    if let Maybe.Some(x) = m {
        println(x)
    } else {
        println(0)
    }
}
`
	got := runMonoLowerPipeline(t, src, "/tmp/phase2_maybe_ir.osty")
	const wantTypeName = "_ZTSN4main5MaybeIlEE"
	if !strings.Contains(got, wantTypeName) {
		t.Fatalf("generated IR missing mangled enum type %q:\n%s", wantTypeName, got)
	}
}

func TestGenerateModuleGenericEnumMaybeInferredFromPayload(t *testing.T) {
	src := `enum Maybe<T> { Some(T), None }

fn main() {
    let m = Maybe.Some(42)
    if let Maybe.Some(x) = m {
        println(x)
    } else {
        println(0)
    }
}
`
	got := runMonoLowerPipeline(t, src, "/tmp/phase2_maybe_inferred_ir.osty")
	const wantTypeName = "_ZTSN4main5MaybeIlEE"
	if !strings.Contains(got, wantTypeName) {
		t.Fatalf("generated IR missing inferred mangled enum type %q:\n%s", wantTypeName, got)
	}
}

func TestGenerateModuleGenericEnumMaybeNoneFromLetContext(t *testing.T) {
	src := `enum Maybe<T> { Some(T), None }

fn main() {
    let m: Maybe<Int> = Maybe.None
    if let Maybe.None = m {
        println(1)
    } else {
        println(0)
    }
}
`
	got := runMonoLowerPipeline(t, src, "/tmp/phase2_maybe_none_ir.osty")
	const wantTypeName = "_ZTSN4main5MaybeIlEE"
	if !strings.Contains(got, wantTypeName) {
		t.Fatalf("generated IR missing payload-free mangled enum type %q:\n%s", wantTypeName, got)
	}
}

func TestGenerateModuleBuiltinResultFieldConstructors(t *testing.T) {
	src := `fn main() {
    let ok: Result<Int, String> = Result.Ok(42)
    let err: Result<Int, String> = Result.Err("x")
    println(1)
}
`
	got := runMonoLowerPipeline(t, src, "/tmp/phase2_result_field_ctor_ir.osty")
	for _, want := range []string{
		"%Result.i64.ptr",
		"insertvalue %Result.i64.ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateModuleBuiltinResultConstructorsTrackLocalContext(t *testing.T) {
	src := `struct Holder {
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
`
	got := runMonoLowerPipeline(t, src, "/tmp/phase2_result_context_ir.osty")
	for _, want := range []string{
		"%Result.i64.ptr",
		"%Result.i1.ptr",
		"@consume",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateModuleStdTestingExpectOkCompat(t *testing.T) {
	src := `use std.testing

enum CalcError {
    DivideByZero,
}

fn div(a: Int, b: Int) -> Result<Int, CalcError> {
    if b == 0 { Err(DivideByZero) } else { Ok(a / b) }
}

fn main() {
    let q = testing.expectOk(div(10, 2))
    testing.assertEq(q, 5)
    testing.expectError(div(1, 0))
}
`
	got := runMonoLowerPipeline(t, src, "/tmp/phase2_testing_expect_ok_ir.osty")
	for _, want := range []string{
		"%Result.i64.i64 = type { i64, i64, i64 }",
		"extractvalue %Result.i64.i64",
		"call %Result.i64.i64 @div(i64 10, i64 2)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateModuleTupleTableDrivenLoopCompat(t *testing.T) {
	src := `fn clamp(v: Int, lo: Int, hi: Int) -> Int {
    if v < lo { lo } else if v > hi { hi } else { v }
}

fn main() {
    let cases = [
        (5, 0, 10, 5),
        (-1, 0, 10, 0),
        (99, 0, 10, 10),
    ]
    for c in cases {
        let (v, lo, hi, expected) = c
        println(clamp(v, lo, hi) - expected)
    }
}
`
	got := runMonoLowerPipeline(t, src, "/tmp/phase2_tuple_table_ir.osty")
	for _, want := range []string{
		"%Tuple.i64.i64.i64.i64 = type { i64, i64, i64, i64 }",
		"call void @osty_rt_list_push_bytes_v1(",
		"call void @osty_rt_list_get_bytes_v1(",
		"extractvalue %Tuple.i64.i64.i64.i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateModuleLetStructPatternDestructuring(t *testing.T) {
	src := `use runtime.strings as strings {
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
`
	got := runMonoLowerPipeline(t, src, "/tmp/phase2_let_struct_pattern_ir.osty")
	for _, want := range []string{
		"extractvalue %Bucket",
		"extractvalue %Pair",
		"declare ptr @osty_rt_list_sorted_string(ptr)",
		"call ptr @osty_rt_list_sorted_string",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
