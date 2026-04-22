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
	direct := llvmNativeEmitModule(nativeMod)
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
	direct := llvmNativeEmitModule(nativeMod)
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

func TestNativeOwnedModuleEntryFallsBackForStructFieldAssign(t *testing.T) {
	src := `struct Pair { left: Int, right: Int }

fn main() {
    let mut pair = Pair { left: 1, right: 2 }
    pair.left = 3
    println(pair.left)
}
`
	mod := lowerNativeEntryModule(t, src)
	opts := Options{PackageName: "main", SourcePath: "/tmp/native_entry_struct_assign.osty"}
	if nativeMod, ok := nativeModuleFromIR(mod, opts); ok {
		t.Fatalf("nativeModuleFromIR unexpectedly accepted struct field assignment: %#v", nativeMod)
	}
	out, err := GenerateModule(mod, opts)
	if err != nil {
		t.Fatalf("GenerateModule fallback failed: %v", err)
	}
	if !strings.Contains(string(out), "%Pair = type { i64, i64 }") {
		t.Fatalf("legacy fallback IR missing struct definition:\n%s", string(out))
	}
}

func TestTryGenerateNativeOwnedModuleReturnsNotCoveredForStructFieldAssign(t *testing.T) {
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
	if ok {
		t.Fatalf("TryGenerateNativeOwnedModule unexpectedly covered struct field assignment:\n%s", string(out))
	}
	if len(out) != 0 {
		t.Fatalf("TryGenerateNativeOwnedModule returned IR for uncovered module:\n%s", string(out))
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
