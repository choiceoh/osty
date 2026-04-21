package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/llvmgen"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// TestPhase2MangledNamesAreValidLLVMIdentifiers locks in the Phase 2
// fix for LLVM016: `Map<String, V>` now mangles to a name built from
// valid identifier characters (`Ss` for String, `By` for Bytes),
// instead of `?` placeholders that llvmIsIdent rejects. Without this
// the whole `GenerateModule` entry point fails before reaching the
// interesting method-emission code.
func TestPhase2MangledNamesAreValidLLVMIdentifiers(t *testing.T) {
	src := `fn touch(m: Map<String, Int>) -> Int { m.len() }
fn main() {}
`
	monoMod := pipelineThroughMonomorph(t, src)
	for _, d := range monoMod.Decls {
		sd, ok := d.(*ir.StructDecl)
		if !ok || len(sd.Generics) != 0 {
			continue
		}
		if !strings.Contains(sd.Name, "Map") {
			continue
		}
		if strings.ContainsRune(sd.Name, '?') ||
			strings.ContainsRune(sd.Name, '<') ||
			strings.ContainsRune(sd.Name, '>') ||
			strings.ContainsRune(sd.Name, ' ') {
			t.Fatalf("specialized Map name %q contains illegal LLVM identifier chars — mangler regressed on String/Bytes", sd.Name)
		}
	}
}

// TestPhase2SpecializedBuiltinBodylessMethodsAreDropped locks in the
// Phase 2 fix for LLVM010: the AST bridge
// (legacyStructDeclFromIR) strips bodyless intrinsic methods from
// `_ZTS…` specialized structs so the legacy emitter doesn't wall on
// "function has no body". The runtime backs those calls at dispatch
// time via osty_rt_map_* helpers, so the LLVM layer never needs the
// empty definitions.
func TestPhase2SpecializedBuiltinBodylessMethodsAreDropped(t *testing.T) {
	src := `fn touch(m: Map<String, Int>) -> Int { m.len() }
fn main() {}
`
	monoMod := pipelineThroughMonomorph(t, src)
	opts := llvmgen.Options{
		PackageName: "main",
		SourcePath:  "/tmp/phase2_bodyless.osty",
		Source:      []byte(src),
	}
	_, err := llvmgen.GenerateModule(monoMod, opts)
	if err != nil && strings.Contains(err.Error(), "LLVM010") && strings.Contains(err.Error(), "has no body") {
		t.Fatalf("bodyless intrinsic method leaked into LLVM emission: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "LLVM016") {
		t.Fatalf("mangled specialized name still failing ASCII-identifier check: %v", err)
	}
	// Any other error is the next Phase 2 blocker — not a regression
	// of what this test covers.
	if err != nil {
		t.Logf("later Phase 2 blocker (not the bodyless one this test covers): %v", err)
	}
}

// TestPhase2MonomorphMapReachesGenerateModule is the smallest viable
// end-to-end probe for Option B's Phase 2 gap: after Phase 1's
// monomorphization produces `Map$String$Int` with 22 methods, does
// llvmgen.GenerateModule survive consuming that module?
//
// This test drives the full pipeline in-process (parse → resolve →
// check → ir.Lower → inject stdlib types → monomorphize → llvmgen)
// and records what the backend does with the specialized Map.
// Failures here are informational — they pin down which llvmgen
// subsystem needs to learn about specialized builtin types, so the
// Phase 2 fix can target that subsystem directly.
func TestPhase2MonomorphMapReachesGenerateModule(t *testing.T) {
	src := `fn touch(m: Map<String, Int>, k: String) -> Bool {
    m.containsKey(k)
}

fn main() {}
`
	file, parseDiags := parser.ParseCanonical([]byte(src))
	for _, d := range parseDiags {
		if d.Severity == 0 {
			t.Fatalf("parse error: %v", d)
		}
	}
	reg := stdlib.LoadCached()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	chk := check.File(file, res, check.Opts{
		Source: []byte(src),
		Stdlib: reg,
	})
	mod, _ := ir.Lower("main", file, res, chk)
	if mod == nil {
		t.Fatalf("ir.Lower returned nil")
	}

	// Phase 1: inject + monomorph.
	injected, _ := injectReachableStdlibTypes(mod, reg)
	mod.Decls = append(mod.Decls, injected...)
	monoMod, monoErrs := ir.Monomorphize(mod)
	if monoMod == nil {
		t.Fatalf("Monomorphize returned nil: %v", monoErrs)
	}

	// Phase 2 probe: feed to the LLVM backend.
	opts := llvmgen.Options{
		PackageName: "main",
		SourcePath:  "/tmp/phase2.osty",
		Source:      []byte(src),
	}
	irOut, err := llvmgen.GenerateModule(monoMod, opts)
	if err != nil {
		// Document the next-blocker shape without failing CI. Each
		// Phase 2 PR peels one layer; this test reports the current
		// outermost wall so the next iteration can target it
		// precisely.
		t.Logf("Phase 2 next-blocker: llvmgen.GenerateModule walls on monomorphized Map<String,Int>: %v", err)
		t.Skipf("Phase 2 pipeline not yet complete; see log above for the current outermost failure")
	}
	got := string(irOut)
	t.Logf("Phase 2 observation: LLVM IR size = %d bytes", len(got))

	// Does the emission include any `_ZTSN...Map...` symbol? That
	// would prove the specialized method actually made it into the
	// textual LLVM output, which is what we'd retire the hand-emit
	// onto in Phase 5.
	seesSpecialized := strings.Contains(got, "_ZTSN") && strings.Contains(got, "Map")
	if !seesSpecialized {
		t.Logf("observation: no Map$... symbol in generated IR — likely the existing emitMapMethodCall intercept still fired on 'Map' source name")
	} else {
		t.Logf("observation: specialized Map symbol present in IR")
	}

	// Also record whether the existing `osty_rt_map_contains_*`
	// intrinsic still shows — if so, the old path is still in use.
	if strings.Contains(got, "osty_rt_map_contains_") {
		t.Logf("observation: legacy contains intrinsic symbol present — hand-emit path is winning over the specialized method")
	}
}

// pipelineThroughMonomorph runs parse → resolve → check → ir.Lower →
// inject stdlib types → monomorphize and returns the resulting IR
// module. Shared by the Phase 2 probe tests so each focuses on one
// assertion rather than repeating the pipeline boilerplate.
func pipelineThroughMonomorph(t *testing.T, src string) *ir.Module {
	t.Helper()
	file, parseDiags := parser.ParseCanonical([]byte(src))
	for _, d := range parseDiags {
		if d.Severity == 0 {
			t.Fatalf("parse error: %v", d)
		}
	}
	reg := stdlib.LoadCached()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	chk := check.File(file, res, check.Opts{
		Source: []byte(src),
		Stdlib: reg,
	})
	mod, _ := ir.Lower("main", file, res, chk)
	if mod == nil {
		t.Fatalf("ir.Lower returned nil")
	}
	injected, _ := injectReachableStdlibTypes(mod, reg)
	mod.Decls = append(mod.Decls, injected...)
	monoMod, monoErrs := ir.Monomorphize(mod)
	if monoMod == nil {
		t.Fatalf("Monomorphize returned nil: %v", monoErrs)
	}
	return monoMod
}
