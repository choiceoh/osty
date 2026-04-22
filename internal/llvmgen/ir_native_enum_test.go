package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	ostyir "github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// Enum support lands in two stages. Stage 1 (this file) locks the
// Osty-mirrored data model + type-def emission + projection helper.
// Stage 2 wires the projection into `nativeModuleFromIR` so generic
// enum modules lower end-to-end through `TryGenerateNativeOwnedModule`
// instead of the legacy IR→AST bridge.

func TestLlvmNativeEmitEnumTypeDefPayloadless(t *testing.T) {
	got := llvmNativeEmitEnumTypeDef(&llvmNativeEnum{
		name:     "_ZTSN4main4UnitE",
		llvmType: "%_ZTSN4main4UnitE",
	})
	want := "%_ZTSN4main4UnitE = type { i64 }"
	if got != want {
		t.Fatalf("payloadless enum type def mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestLlvmNativeEmitEnumTypeDefWithPayload(t *testing.T) {
	got := llvmNativeEmitEnumTypeDef(&llvmNativeEnum{
		name:            "_ZTSN4main5MaybeIlEE",
		llvmType:        "%_ZTSN4main5MaybeIlEE",
		payloadSlotType: "i64",
	})
	want := "%_ZTSN4main5MaybeIlEE = type { i64, i64 }"
	if got != want {
		t.Fatalf("payload enum type def mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestLlvmNativeEmitModuleIncludesEnumTypeDefs(t *testing.T) {
	mod := &llvmNativeModule{
		sourcePath: "/tmp/enum_typedef.osty",
		enums: []*llvmNativeEnum{{
			name:            "_ZTSN4main5MaybeIlEE",
			llvmType:        "%_ZTSN4main5MaybeIlEE",
			payloadSlotType: "i64",
			variants: []*llvmNativeEnumVariant{
				{name: "Some", tag: 0, payloadType: "i64"},
				{name: "None", tag: 1, payloadType: ""},
			},
		}},
	}
	out := llvmNativeEmitModule(mod)
	want := "%_ZTSN4main5MaybeIlEE = type { i64, i64 }"
	if !strings.Contains(out, want) {
		t.Fatalf("module IR missing enum type def %q:\n%s", want, out)
	}
}

func TestNativeRegisterEnumDeclPayloadlessAndPayload(t *testing.T) {
	decl := &ostyir.EnumDecl{
		Name: "_ZTSN4main5MaybeIlEE",
		Variants: []*ostyir.Variant{
			{Name: "Some", Payload: []ostyir.Type{ostyir.TInt}},
			{Name: "None"},
		},
	}
	ctx := &nativeProjectionCtx{}
	info, ok := nativeRegisterEnumDecl(ctx, decl)
	if !ok {
		t.Fatal("nativeRegisterEnumDecl rejected Maybe<Int>-shaped enum")
	}
	if info.def.name != "_ZTSN4main5MaybeIlEE" {
		t.Fatalf("enum def name = %q, want %q", info.def.name, "_ZTSN4main5MaybeIlEE")
	}
	if info.def.llvmType != "%_ZTSN4main5MaybeIlEE" {
		t.Fatalf("enum def llvmType = %q, want %q", info.def.llvmType, "%_ZTSN4main5MaybeIlEE")
	}
	if info.def.payloadSlotType != "i64" {
		t.Fatalf("payloadSlotType = %q, want %q", info.def.payloadSlotType, "i64")
	}
	if len(info.def.variants) != 2 {
		t.Fatalf("variants len = %d, want 2", len(info.def.variants))
	}
	some, ok := info.variantsByName["Some"]
	if !ok {
		t.Fatal("variantsByName missing Some")
	}
	if some.tag != 0 || some.payloadLLVMType != "i64" {
		t.Fatalf("Some variant = %+v, want tag=0 payload=i64", some)
	}
	none, ok := info.variantsByName["None"]
	if !ok {
		t.Fatal("variantsByName missing None")
	}
	if none.tag != 1 || none.payloadLLVMType != "" {
		t.Fatalf("None variant = %+v, want tag=1 payload=\"\"", none)
	}
}

func TestNativeRegisterEnumDeclRejectsGenericTemplate(t *testing.T) {
	decl := &ostyir.EnumDecl{
		Name:     "Maybe",
		Generics: []*ostyir.TypeParam{{Name: "T"}},
		Variants: []*ostyir.Variant{
			{Name: "Some"},
			{Name: "None"},
		},
	}
	ctx := &nativeProjectionCtx{}
	if _, ok := nativeRegisterEnumDecl(ctx, decl); ok {
		t.Fatal("nativeRegisterEnumDecl accepted an un-monomorphized generic template")
	}
}

// TestTryGenerateNativeOwnedModuleCoversGenericEnumMaybe locks in
// that the stage-2 wiring actually routes `Maybe<Int>` end-to-end
// through the native-owned path — not the legacy IR→AST bridge.
// The sibling `TestGenerateModuleGenericEnumMaybe*` tests pass via
// fallback too, so they don't distinguish; this one calls
// `TryGenerateNativeOwnedModule` directly so `ok=true` is the real
// coverage signal.
func TestTryGenerateNativeOwnedModuleCoversGenericEnumMaybe(t *testing.T) {
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
	monoMod, monoErrs := ostyir.Monomorphize(mod)
	if len(monoErrs) != 0 {
		t.Fatalf("ir.Monomorphize errors: %v", monoErrs)
	}
	out, ok, err := TryGenerateNativeOwnedModule(monoMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_enum_maybe.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule errored: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported uncovered for Maybe<Int> — enum wiring regressed")
	}
	got := string(out)
	for _, want := range []string{
		"%_ZTSN4main5MaybeIlEE = type { i64, i64 }",
		"insertvalue %_ZTSN4main5MaybeIlEE",
		"extractvalue %_ZTSN4main5MaybeIlEE",
		"icmp eq i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("native-owned enum IR missing %q:\n%s", want, got)
		}
	}
}

// TestTryGenerateNativeOwnedModuleCoversGenericEnumMaybeNone locks
// the payload-free variant construction shape (`Maybe.None`) under
// a with-payload enum (`Maybe<Int>` has `Some(Int)` so the slot
// width is non-zero). Variant zero-padding lives in
// nativeVariantLitFromIR.
func TestTryGenerateNativeOwnedModuleCoversGenericEnumMaybeNone(t *testing.T) {
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
	monoMod, monoErrs := ostyir.Monomorphize(mod)
	if len(monoErrs) != 0 {
		t.Fatalf("ir.Monomorphize errors: %v", monoErrs)
	}
	out, ok, err := TryGenerateNativeOwnedModule(monoMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_enum_none.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule errored: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported uncovered for Maybe.None")
	}
	got := string(out)
	for _, want := range []string{
		"%_ZTSN4main5MaybeIlEE = type { i64, i64 }",
		"insertvalue %_ZTSN4main5MaybeIlEE",
		"icmp eq i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("native-owned None enum IR missing %q:\n%s", want, got)
		}
	}
}

func TestNativeRegisterEnumDeclRejectsMixedPayloadShapes(t *testing.T) {
	// Mixed payload shapes require a union-sized slot the stage-1
	// helper intentionally defers. Stage 2 will extend this with a
	// synthesized union struct.
	decl := &ostyir.EnumDecl{
		Name: "_ZTSN4main6EitherIlfEE",
		Variants: []*ostyir.Variant{
			{Name: "L", Payload: []ostyir.Type{ostyir.TInt}},
			{Name: "R", Payload: []ostyir.Type{ostyir.TFloat}},
		},
	}
	ctx := &nativeProjectionCtx{}
	if _, ok := nativeRegisterEnumDecl(ctx, decl); ok {
		t.Fatal("nativeRegisterEnumDecl accepted mixed-payload enum (should defer to stage 2)")
	}
}
