package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	ostyir "github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// TestTryGenerateNativeOwnedModuleCoversResultOkErr locks the
// built-in Result<T, E> constructor path: `Result.Ok(x)` and
// `Result.Err(e)` lower to insertvalue sequences over the
// `%Result.<ok>.<err>` storage type. The projection layer
// intercepts the IR's `Result.Ok` / `Result.Err` method calls
// (receiver is a type-name ident) before the generic concrete-
// method dispatch, so no user-side Result struct is required.
func TestTryGenerateNativeOwnedModuleCoversResultOkErr(t *testing.T) {
	src := `fn main() {
    let ok: Result<Int, String> = Result.Ok(42)
    let err: Result<Int, String> = Result.Err("x")
    println(1)
}
`
	mod := lowerResultNativeEntryModule(t, src)
	out, ok, err := TryGenerateNativeOwnedModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_result_ok_err.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule errored: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported uncovered for Result constructors")
	}
	got := string(out)
	for _, want := range []string{
		"%Result.i64.ptr = type { i64, i64, ptr }",
		"insertvalue %Result.i64.ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("native-owned IR missing %q:\n%s", want, got)
		}
	}
}

// TestTryGenerateNativeOwnedModuleCoversResultMultipleSpecializations
// locks that multiple Result<T, E> type args produce distinct
// `%Result.<ok>.<err>` type defs — one per unique specialization,
// in the order they first appear in the module.
func TestTryGenerateNativeOwnedModuleCoversResultMultipleSpecializations(t *testing.T) {
	src := `fn main() {
    let a: Result<Int, String> = Result.Ok(1)
    let b: Result<Bool, String> = Result.Ok(true)
    println(1)
}
`
	mod := lowerResultNativeEntryModule(t, src)
	out, ok, err := TryGenerateNativeOwnedModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_result_multi.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule errored: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported uncovered for multiple Result specializations")
	}
	got := string(out)
	for _, want := range []string{
		"%Result.i64.ptr",
		"%Result.i1.ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("native-owned IR missing %q:\n%s", want, got)
		}
	}
}

func lowerResultNativeEntryModule(t *testing.T, src string) *ostyir.Module {
	t.Helper()
	file := parseLLVMGenFile(t, src)
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{

		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
	})
	mod, _ := ostyir.Lower("main", file, res, chk)
	monoMod, _ := ostyir.Monomorphize(mod)
	return monoMod
}
