package llvmgen

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// TestNativeToolchainMergedMIRPipelineIsClean gates the MIR-first
// dispatcher on the merged non-bootstrap toolchain. It mirrors the
// production path in internal/backend/llvm.go: attempt
// GenerateFromMIR; on ErrUnsupported fall back to the legacy
// HIR→AST emitter. The whole pipeline must produce LLVM IR without
// a hard error.
//
// This test is the authoritative gate for the "complete MIR-direct
// pipeline" milestone: both the fast path (MIR) and the graceful
// fallback (legacy) must cooperate cleanly. MIR coverage gaps inside
// the merged checker surface used to block the fast path outright
// (first wall was `unsupported local type <error>`); the operand-based
// type recovery in ir.Lower now drops ErrType locals by 91 %
// (444 → 39), with the remainder concentrated in a handful of
// synthesised-span BinaryRVs. When the fast path still refuses (the
// typical case for the merged toolchain today) this gate checks the
// fallback succeeds.
//
// Paired with TestProbeNativeToolchainMergedMIR which is info-only
// and logs the first MIR wall for debugging.
func TestNativeToolchainMergedMIRPipelineIsClean(t *testing.T) {
	if testing.Short() {
		t.Skip("slow (~60s); skipped in -short")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	dir := filepath.Join(root, "toolchain")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read toolchain: %v", err)
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".osty") || strings.HasSuffix(name, "_test.osty") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if isBootstrapOnlyOstyFile(src) {
			continue
		}
		files = append(files, name)
	}
	merged := mergeToolchainSources(t, root, files)
	file, _ := parser.ParseDiagnostics(merged)
	if file == nil {
		t.Fatalf("merged parse returned nil (%d files, %d bytes)", len(files), len(merged))
	}
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{
		
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        merged,
		Privileged:    true,
	})
	mod, _ := ir.Lower("main", file, res, chk)
	if mod == nil {
		t.Fatalf("ir.Lower returned nil module")
	}
	monoMod, _ := ir.Monomorphize(mod)
	if monoMod == nil {
		t.Fatalf("ir.Monomorphize returned nil module")
	}
	opts := Options{PackageName: "main", SourcePath: "/tmp/toolchain_native_merged_mir_pipeline.osty"}

	// Mirror the dispatcher at internal/backend/llvm.go:177-183.
	var (
		out    []byte
		genErr error
	)
	mirMod := mir.Lower(monoMod)
	if mirMod != nil {
		out, genErr = GenerateFromMIR(mirMod, opts)
	}
	if genErr != nil && !errors.Is(genErr, ErrUnsupported) {
		t.Fatalf("GenerateFromMIR hard error (not ErrUnsupported): %v", genErr)
	}
	if genErr != nil {
		// Fall back to legacy HIR path — the production dispatcher
		// does the same on ErrUnsupported.
		out, genErr = generateFromAST(file, opts)
		if genErr != nil {
			t.Fatalf("legacy fallback failed after MIR refusal: %v", genErr)
		}
	}
	if len(out) == 0 {
		t.Fatalf("pipeline produced empty IR output")
	}
	if !strings.Contains(string(out), "target triple") {
		t.Fatalf("pipeline output missing LLVM module header; got first 200 chars:\n%s", firstN(out, 200))
	}
}

func firstN(b []byte, n int) string {
	if len(b) < n {
		return string(b)
	}
	return string(b[:n])
}

// TestNativeToolchainMergedMIRErrTypeFloor locks the current MIR
// ErrType leak count as a progress floor. After the operand-based
// type recovery in ir.Lower (lowerBinary / lowerIfExpr / lowerCall /
// lowerQualifiedCall), the merged native toolchain carries ≤40
// ErrType locals into MIR. Regressions that increase that count
// (e.g. a new checker-coverage gap or a reverted recovery helper)
// will re-expand cascading ErrType propagation and fail this gate.
//
// When the number drops below the floor, tighten it.
func TestNativeToolchainMergedMIRErrTypeFloor(t *testing.T) {
	if testing.Short() {
		t.Skip("slow; skipped in -short")
	}
	const floor = 40
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	dir := filepath.Join(root, "toolchain")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read toolchain: %v", err)
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".osty") || strings.HasSuffix(name, "_test.osty") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if isBootstrapOnlyOstyFile(src) {
			continue
		}
		files = append(files, name)
	}
	merged := mergeToolchainSources(t, root, files)
	file, _ := parser.ParseDiagnostics(merged)
	if file == nil {
		t.Fatalf("merged parse returned nil")
	}
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{
		
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        merged,
		Privileged:    true,
	})
	mod, _ := ir.Lower("main", file, res, chk)
	monoMod, _ := ir.Monomorphize(mod)
	mirMod := mir.Lower(monoMod)
	if mirMod == nil {
		t.Fatalf("mir.Lower returned nil")
	}
	errCount := 0
	for _, fn := range mirMod.Functions {
		if fn == nil {
			continue
		}
		for _, loc := range fn.Locals {
			if loc == nil {
				continue
			}
			if _, ok := loc.Type.(*ir.ErrType); ok {
				errCount++
			}
		}
	}
	if errCount > floor {
		t.Fatalf("ErrType local count regressed: got %d, floor %d — a checker-coverage gap or a reverted recovery helper likely leaked typing back into MIR", errCount, floor)
	}
	if errCount+10 < floor {
		t.Logf("ErrType count %d is well below floor %d — consider tightening the floor to catch future regressions sooner", errCount, floor)
	}
}
