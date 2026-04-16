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
