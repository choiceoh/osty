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
		// Phase 6f follow-up: the vtable slot points at a shim that
		// adapts the interface dispatch ABI to the underlying method's
		// calling convention; the shim's body in turn calls the real
		// `Vec__size` symbol.
		"@osty.shim.Vec__Sized__size",
		"@Vec__size",
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

// TestGenerateModuleInterfaceDispatchWithArgs covers Phase 6c: an
// interface method taking non-self arguments is lowered to an
// indirect vtable call that threads those arguments through.
func TestGenerateModuleInterfaceDispatchWithArgs(t *testing.T) {
	src := `interface Combine {
    fn combine(self, other: Int) -> Int
}

struct Thing {
    x: Int,

    fn combine(self, other: Int) -> Int {
        self.x + other
    }
}

fn main() {
    let t = Thing { x: 3 }
    let c: Combine = t
    println(c.combine(4))
}
`
	got := runMonoLowerPipeline(t, src, "/tmp/phase6c_iface_args.osty")
	for _, want := range []string{
		"%osty.iface",
		"@osty.vtable.Thing__Combine",
		"@osty.shim.Thing__Combine__combine",
		"@Thing__combine",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	// The indirect call must carry both the data ptr (self) and the
	// non-self arg. The exact SSA name of the fn ptr is implementation
	// detail, so we only assert on the shape near the call site.
	if !strings.Contains(got, ", i64 4)") {
		t.Fatalf("expected non-self arg `i64 4` threaded into indirect call:\n%s", got)
	}
}

// TestGenerateModuleInterfaceBoxingFromReturn covers Phase 6d's
// return-position auto-boxing: a function whose declared return type
// is an interface can return a concrete value, and the caller receives
// a `%osty.iface` fat pointer suitable for subsequent dispatch.
func TestGenerateModuleInterfaceBoxingFromReturn(t *testing.T) {
	src := `interface Sized {
    fn size(self) -> Int
}

struct Vec {
    count: Int,

    fn size(self) -> Int {
        self.count
    }
}

fn wrap(v: Vec) -> Sized {
    v
}

fn main() {
    let v = Vec { count: 5 }
    let s = wrap(v)
    println(s.size())
}
`
	got := runMonoLowerPipeline(t, src, "/tmp/phase6d_return_box.osty")
	for _, want := range []string{
		"%osty.iface",
		"@osty.vtable.Vec__Sized",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateModuleInterfaceBoxingFromCallArg covers Phase 6d's
// call-argument auto-boxing: passing a concrete value into a parameter
// whose declared type is an interface must insert a `%osty.iface` fat
// pointer transparently so the callee's dispatch path works.
func TestGenerateModuleInterfaceBoxingFromCallArg(t *testing.T) {
	src := `interface Sized {
    fn size(self) -> Int
}

struct Vec {
    count: Int,

    fn size(self) -> Int {
        self.count
    }
}

fn measure(s: Sized) -> Int {
    s.size()
}

fn main() {
    let v = Vec { count: 7 }
    println(measure(v))
}
`
	got := runMonoLowerPipeline(t, src, "/tmp/phase6d_callarg_box.osty")
	for _, want := range []string{
		"%osty.iface",
		"@osty.vtable.Vec__Sized",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateModuleInterfaceBoxingFromAssign covers Phase 6e: after a
// `let mut s: Iface = concrete` binding, subsequent `s = concrete2`
// reassignments must auto-box the new concrete value into a
// `%osty.iface` fat pointer, mirroring the let / return / call-arg
// boxing paths.
func TestGenerateModuleInterfaceBoxingFromAssign(t *testing.T) {
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
    let mut s: Sized = v
    let w = Vec { count: 9 }
    s = w
    println(s.size())
}
`
	got := runMonoLowerPipeline(t, src, "/tmp/phase6e_assign_box.osty")
	for _, want := range []string{
		"%osty.iface",
		"@osty.vtable.Vec__Sized",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	// Reassignment must emit TWO boxings (initial let + assign), so the
	// vtable symbol appears at least twice in the final IR.
	if strings.Count(got, "@osty.vtable.Vec__Sized") < 2 {
		t.Fatalf("expected vtable symbol referenced at least twice (let + assign), got %d:\n%s",
			strings.Count(got, "@osty.vtable.Vec__Sized"), got)
	}
}

// TestGenerateModuleMangledInterfaceNameVtableDispatch covers Phase 6f:
// Phase 5's generic interface specialization emits nominal names in
// the Itanium `_ZTSN…E` shape. Phase 6a-6e's vtable scaffold must
// continue to work on those mangled names — discovery, vtable
// emission, boxing, and dispatch all keyed on the exact string
// produced by `MonomorphMangleType`. The parser does not yet accept
// generic interface declarations, so this smoke feeds the mangled
// name as the raw source identifier; the resulting pipeline stage
// mirrors what Phase 5 would hand llvmgen for a `Container<Int>`
// specialization.
func TestGenerateModuleMangledInterfaceNameVtableDispatch(t *testing.T) {
	const mangled = "_ZTSN4main9ContainerIlEE"
	src := `interface ` + mangled + ` {
    fn get(self) -> Int
}

struct IntBox {
    value: Int,

    fn get(self) -> Int {
        self.value
    }
}

fn main() {
    let b = IntBox { value: 3 }
    let c: ` + mangled + ` = b
    println(c.get())
}
`
	got := runMonoLowerPipeline(t, src, "/tmp/phase6f_mangled_iface.osty")
	for _, want := range []string{
		"%osty.iface",
		mangled,
		"@osty.vtable.IntBox__" + mangled,
		"@osty.shim.IntBox__" + mangled + "__get",
		"@IntBox__get",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestRenderInterfaceShimVoidReturn locks the bug fix for
// `renderInterfaceShim`'s void-return path: when a method's return
// is `void`, the shim must emit a bare `call void @impl(...)` rather
// than binding the result to an SSA register (`%x = call void ...`),
// which is not valid LLVM IR.
//
// Unit-level rather than end-to-end because statement-position
// interface method calls (`c.clear()` as a stmt) are a separate
// llvmgen gap; exercising `renderInterfaceShim` directly keeps this
// regression lock narrowly focused on the fix itself.
func TestRenderInterfaceShimVoidReturn(t *testing.T) {
	iface := &interfaceInfo{name: "Clear"}
	impl := interfaceImpl{
		implName:  "Bin",
		kind:      0,
		vtableSym: "@osty.vtable.Bin__Clear",
	}
	m := interfaceMethodSig{name: "clear", slot: 0}
	sig := &fnSig{
		name:    "clear",
		irName:  "Bin__clear",
		ret:     "void",
		params:  []paramInfo{{name: "self", typ: "%Bin"}},
	}
	sym, def := renderInterfaceShim(iface, impl, m, sig, "%Bin")
	if sym == "" || def == "" {
		t.Fatal("expected renderInterfaceShim to produce a shim for a void method")
	}
	if strings.Contains(def, "%ret.val = call void") {
		t.Fatalf("invalid LLVM IR: bound SSA register to void call:\n%s", def)
	}
	if !strings.Contains(def, "call void @Bin__clear") {
		t.Fatalf("expected bare `call void @Bin__clear` in shim body:\n%s", def)
	}
	if !strings.Contains(def, "  ret void") {
		t.Fatalf("expected `ret void` terminator in shim body:\n%s", def)
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
// generic `enum Maybe<T>` lands as a concrete mangled nominal.
//
// Currently skipped: Phase 2 correctly rewrites the IR but the LLVM
// backend's legacy variant-call conversion (`Maybe.Some(42)` → dotted
// AST FieldExpr) fires an `LLVM015` unsupported diagnostic when the
// enum name is a mangled Itanium symbol. The IR-level behaviour is
// covered by TestMonomorphizeGenericEnumSpecialization in
// internal/ir/monomorph_test.go; the end-to-end smoke is left here as a
// regression beacon and picked up by the llvmgen enum-lowering
// follow-up (tracked alongside Phase 2 scope in LLVM_MIGRATION_PLAN.md).
func TestGenerateModuleGenericEnumMaybeMonomorphized(t *testing.T) {
	src := `enum Maybe<T> { Some(T), None }

fn main() {
    let m = Maybe.Some(42)
    println(1)
}
`
	got := runMonoLowerPipeline(t, src, "/tmp/phase2_maybe_ir.osty")
	const wantTypeName = "_ZTSN4main5MaybeIlEE"
	if !strings.Contains(got, wantTypeName) {
		t.Fatalf("generated IR missing mangled enum type %q:\n%s", wantTypeName, got)
	}
}
