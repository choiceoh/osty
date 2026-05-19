package main

import (
	"testing"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/mir"
)

// TestStripMainForLibraryModeRemovesMainAndKeepsOthers locks the
// PR-G2 dep-as-library helper behavior: only the `main` function is
// stripped, every other MIR function survives. Without this, a future
// over-aggressive filter could silently drop pub helpers and break
// the link step downstream.
func TestStripMainForLibraryModeRemovesMainAndKeepsOthers(t *testing.T) {
	mainFn := &mir.Function{Name: "main"}
	helperFn := &mir.Function{Name: "frontInvalidTypeRepr"}
	otherFn := &mir.Function{Name: "checkLookupFn"}

	entry := &backend.Entry{
		MIR: &mir.Module{
			Functions: []*mir.Function{mainFn, helperFn, otherFn},
		},
	}
	stripMainForLibraryMode(entry)

	if len(entry.MIR.Functions) != 2 {
		t.Fatalf("Functions length = %d, want 2 (after stripping main)", len(entry.MIR.Functions))
	}
	for _, fn := range entry.MIR.Functions {
		if fn.Name == "main" {
			t.Errorf("`main` survived strip: still in Functions list")
		}
	}
	names := map[string]bool{}
	for _, fn := range entry.MIR.Functions {
		names[fn.Name] = true
	}
	if !names["frontInvalidTypeRepr"] {
		t.Errorf("frontInvalidTypeRepr was stripped (should survive)")
	}
	if !names["checkLookupFn"] {
		t.Errorf("checkLookupFn was stripped (should survive)")
	}
}

// TestStripMainForLibraryModeHandlesNilSafely guards against nil
// entry / nil MIR / nil function slice — preparePackageEntry can fail
// in ways that produce partial entries, and the strip helper runs
// unconditionally after a successful prepare call. Without this
// test, a regression that crashes on nil could ship undetected
// because the cross-pkg dep path is opt-in via env var (only fires
// in dev / one-off CI runs).
func TestStripMainForLibraryModeHandlesNilSafely(t *testing.T) {
	stripMainForLibraryMode(nil)
	stripMainForLibraryMode(&backend.Entry{})

	entry := &backend.Entry{MIR: &mir.Module{}}
	stripMainForLibraryMode(entry)
	if len(entry.MIR.Functions) != 0 {
		t.Fatalf("Functions length = %d, want 0 (no-op on empty)", len(entry.MIR.Functions))
	}

	entry2 := &backend.Entry{
		MIR: &mir.Module{
			Functions: []*mir.Function{nil, {Name: "main"}, nil, {Name: "helper"}},
		},
	}
	stripMainForLibraryMode(entry2)
	if len(entry2.MIR.Functions) != 1 {
		t.Fatalf("Functions length = %d, want 1 (helper survived, nils + main stripped)", len(entry2.MIR.Functions))
	}
	if entry2.MIR.Functions[0].Name != "helper" {
		t.Fatalf("survivor = %q, want \"helper\"", entry2.MIR.Functions[0].Name)
	}
}

// TestQualifyExportedSymbolsForLibraryModeRenamesExportsAndCallsites
// locks the cross-pkg link unblock from
// `docs/llvm-selfhost-plan-cross-pkg-link-measurement.md` Path α:
// exported function names get prefixed with `<packageName>.` and any
// internal `CallInstr.Callee.Symbol` referencing one of those names is
// patched so the dep's library `.o` exports the same symbol the
// consumer's `qualifiedSymbol` mangling expects.
func TestQualifyExportedSymbolsForLibraryModeRenamesExportsAndCallsites(t *testing.T) {
	// `helper` (exported) calls `inner` (private). After the rename
	// the dep's library object exports `@toolchain.helper` and
	// `@inner` keeps its bare name. The call site inside `helper`
	// still targets `@inner`. A second exported function `other`
	// calls `helper` — that call site must be rewritten to
	// `@toolchain.helper`.
	innerFn := &mir.Function{Name: "inner", Exported: false}
	helperFn := &mir.Function{
		Name:     "helper",
		Exported: true,
		Blocks: []*mir.BasicBlock{{
			Instrs: []mir.Instr{
				&mir.CallInstr{Callee: &mir.FnRef{Symbol: "inner"}},
			},
		}},
	}
	otherFn := &mir.Function{
		Name:     "other",
		Exported: true,
		Blocks: []*mir.BasicBlock{{
			Instrs: []mir.Instr{
				&mir.CallInstr{Callee: &mir.FnRef{Symbol: "helper"}},
				&mir.CallInstr{Callee: &mir.FnRef{Symbol: "inner"}},
			},
		}},
	}

	entry := &backend.Entry{
		MIR: &mir.Module{
			Functions: []*mir.Function{innerFn, helperFn, otherFn},
		},
	}
	qualifyExportedSymbolsForLibraryMode(entry, "toolchain")

	if innerFn.Name != "inner" {
		t.Fatalf("inner (private) renamed to %q, want unchanged", innerFn.Name)
	}
	if helperFn.Name != "toolchain.helper" {
		t.Fatalf("helper.Name = %q, want \"toolchain.helper\"", helperFn.Name)
	}
	if otherFn.Name != "toolchain.other" {
		t.Fatalf("other.Name = %q, want \"toolchain.other\"", otherFn.Name)
	}

	// helper's internal call to `inner` is private — must NOT rename.
	if got := callSymbol(helperFn, 0, 0); got != "inner" {
		t.Fatalf("helper -> inner call symbol = %q, want \"inner\" (private)", got)
	}
	// other's call to `helper` is exported — must rename to qualified.
	if got := callSymbol(otherFn, 0, 0); got != "toolchain.helper" {
		t.Fatalf("other -> helper call symbol = %q, want \"toolchain.helper\"", got)
	}
	// other's call to `inner` stays bare.
	if got := callSymbol(otherFn, 0, 1); got != "inner" {
		t.Fatalf("other -> inner call symbol = %q, want \"inner\"", got)
	}
}

// TestQualifyExportedSymbolsForLibraryModeRespectsExportSymbol locks
// the LANG_SPEC §19.6 `#[export("name")]` interaction: when a function
// asks for a verbatim symbol, the qualifier rename is skipped so FFI
// consumers expecting the exact spelling still find the symbol.
func TestQualifyExportedSymbolsForLibraryModeRespectsExportSymbol(t *testing.T) {
	verbatim := &mir.Function{
		Name:         "fooImpl",
		Exported:     true,
		ExportSymbol: "verbatim_c_abi_name",
	}
	plain := &mir.Function{Name: "bar", Exported: true}
	entry := &backend.Entry{
		MIR: &mir.Module{Functions: []*mir.Function{verbatim, plain}},
	}
	qualifyExportedSymbolsForLibraryMode(entry, "pkg")

	if verbatim.Name != "fooImpl" {
		t.Fatalf("verbatim ExportSymbol fn renamed: Name=%q, want \"fooImpl\"", verbatim.Name)
	}
	if plain.Name != "pkg.bar" {
		t.Fatalf("plain exported fn Name=%q, want \"pkg.bar\"", plain.Name)
	}
}

// TestQualifyExportedSymbolsForLibraryModeNoOpOnEmptyPackageName locks
// the backwards-compat fallback: callers that haven't filled
// PackageName get the historical bare-name emission, no rename.
func TestQualifyExportedSymbolsForLibraryModeNoOpOnEmptyPackageName(t *testing.T) {
	fn := &mir.Function{Name: "helper", Exported: true}
	entry := &backend.Entry{
		MIR: &mir.Module{Functions: []*mir.Function{fn}},
	}
	qualifyExportedSymbolsForLibraryMode(entry, "")
	if fn.Name != "helper" {
		t.Fatalf("empty PackageName caused rename: %q", fn.Name)
	}
}

// TestQualifyExportedSymbolsForLibraryModeHandlesNilSafely mirrors
// stripMainForLibraryMode's nil-safety contract.
func TestQualifyExportedSymbolsForLibraryModeHandlesNilSafely(t *testing.T) {
	qualifyExportedSymbolsForLibraryMode(nil, "pkg")
	qualifyExportedSymbolsForLibraryMode(&backend.Entry{}, "pkg")
	qualifyExportedSymbolsForLibraryMode(&backend.Entry{MIR: &mir.Module{}}, "pkg")
	qualifyExportedSymbolsForLibraryMode(&backend.Entry{
		MIR: &mir.Module{Functions: []*mir.Function{nil, {Name: "main"}}},
	}, "pkg")
}

func callSymbol(fn *mir.Function, blockIdx, instrIdx int) string {
	if fn == nil || blockIdx >= len(fn.Blocks) || fn.Blocks[blockIdx] == nil {
		return ""
	}
	bb := fn.Blocks[blockIdx]
	if instrIdx >= len(bb.Instrs) {
		return ""
	}
	ci, ok := bb.Instrs[instrIdx].(*mir.CallInstr)
	if !ok || ci == nil {
		return ""
	}
	ref, ok := ci.Callee.(*mir.FnRef)
	if !ok || ref == nil {
		return ""
	}
	return ref.Symbol
}
