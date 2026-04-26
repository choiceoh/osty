package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	ir "github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// TestLegacyExportAliasEmitsBothSymbols verifies the §19.6
// `#[export("name")]` annotation produces an LLVM `alias` line in
// the default `GenerateModule` (legacy) emit path.
//
// Background: PR #329 wired ExportSymbol through the MIR pipeline
// (`GenerateFromMIR`, opt-in via `Options.UseMIR`). Default callers
// route through `GenerateModule` → `legacyFileFromModule` →
// `generateASTFile`, which never knew about `#[export]`. This PR
// adds a post-process step in `GenerateModule` that scans the IR
// module for ExportSymbol-bearing fns and appends one alias line
// per fn so the export symbol is link-resolvable without renaming
// the underlying function (which would break in-module callers).
func TestLegacyExportAliasEmitsBothSymbols(t *testing.T) {
	src := `
#[export("osty.gc.legacy_v1")]
pub fn legacy_v1() -> Int { 11 }

fn main() {}
`
	out := buildLegacyIR(t, src)
	got := string(out)

	if !strings.Contains(got, "define i64 @legacy_v1(") {
		t.Errorf("expected `define i64 @legacy_v1(` in IR — original fn name should remain:\n%s", got)
	}
	if !strings.Contains(got, "@osty.gc.legacy_v1 = dso_local alias ptr, ptr @legacy_v1") {
		t.Errorf("expected alias `@osty.gc.legacy_v1 = ... alias ptr, ptr @legacy_v1` in IR:\n%s", got)
	}
}

// TestLegacyExportAliasMultipleFns verifies the post-process loop
// handles every #[export]-tagged fn, not just the first.
func TestLegacyExportAliasMultipleFns(t *testing.T) {
	src := `
#[export("osty.gc.first_v1")]
pub fn first() -> Int { 1 }

#[export("osty.gc.second_v1")]
pub fn second() -> Int { 2 }

fn main() {}
`
	out := buildLegacyIR(t, src)
	got := string(out)
	for _, sym := range []string{"osty.gc.first_v1", "osty.gc.second_v1"} {
		if !strings.Contains(got, "@"+sym+" = dso_local alias") {
			t.Errorf("missing alias for `%s`:\n%s", sym, got)
		}
	}
}

// TestLegacyNoExportAliasForOrdinaryFn — regression guard:
// ordinary functions (no #[export]) must not produce alias lines.
func TestLegacyNoExportAliasForOrdinaryFn(t *testing.T) {
	src := `
pub fn ordinary() -> Int { 0 }

fn main() {}
`
	out := buildLegacyIR(t, src)
	got := string(out)
	if strings.Contains(got, "= dso_local alias ptr") {
		t.Errorf("ordinary fn must not produce alias:\n%s", got)
	}
}

// TestLegacyExportAliasRedundantNameSkipped — when ExportSymbol
// equals the fn's existing name (no rename needed), don't emit a
// no-op alias `@foo = alias ptr, ptr @foo`.
func TestLegacyExportAliasRedundantNameSkipped(t *testing.T) {
	src := `
#[export("redundant")]
pub fn redundant() -> Int { 0 }

fn main() {}
`
	out := buildLegacyIR(t, src)
	got := string(out)
	if strings.Contains(got, "@redundant = dso_local alias") {
		t.Errorf("redundant alias (same name) must be skipped:\n%s", got)
	}
}

func TestLegacyCABIExportSurface(t *testing.T) {
	src := `
#[export("osty.gc.legacy_cabi_v1")]
#[c_abi]
#[no_alloc]
pub fn legacy_cabi_v1() -> Int { 13 }

fn main() {}
`
	out := buildLegacyIR(t, src)
	got := string(out)

	if !strings.Contains(got, "define ccc i64 @legacy_cabi_v1(") {
		t.Fatalf("expected legacy path to preserve `#[c_abi]` as `define ccc`:\n%s", got)
	}
	if !strings.Contains(got, "@osty.gc.legacy_cabi_v1 = dso_local alias ptr, ptr @legacy_cabi_v1") {
		t.Fatalf("expected legacy path to preserve `#[export]` alias alongside `#[c_abi]`:\n%s", got)
	}
}

func TestLegacyIntrinsicRejectedExplicitly(t *testing.T) {
	src := `
#[intrinsic]
pub fn raw_null() -> Int

fn main() {}
`
	_, err := buildLegacyIRResult(t, src)
	if err == nil {
		t.Fatal("expected legacy GenerateModule to reject #[intrinsic], got nil error")
	}
	if !strings.Contains(err.Error(), "intrinsic function declaration raw_null") {
		t.Fatalf("legacy intrinsic rejection must stay explicit, got: %v", err)
	}
}

// buildLegacyIR drives the full source-level pipeline through
// `GenerateModule` (the default, legacy AST-based emit path) and
// returns the raw LLVM IR bytes.
func buildLegacyIR(t *testing.T, src string) []byte {
	t.Helper()
	out, err := buildLegacyIRResult(t, src)
	if err != nil {
		t.Fatalf("GenerateModule error: %v", err)
	}
	return out
}

func buildLegacyIRResult(t *testing.T, src string) ([]byte, error) {
	t.Helper()
	file := parseLLVMGenFile(t, src)
	res := resolve.ResolveFileDefault(file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{

		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
		Privileged:    true,
	})
	mod, _ := ir.Lower("main", file, res, chk)
	monoMod, _ := ir.Monomorphize(mod)
	return GenerateModule(monoMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/legacy_export_test.osty",
	})
}
