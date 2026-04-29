package llvmgen

import (
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

// TestNativeToolchainMergedMIRPipelineIsClean gates the MIR backend on
// the merged non-bootstrap toolchain. The self-host critical path must
// now lower the merged native toolchain through MIR directly; falling
// back to the legacy HIR→AST emitter would hide real backend coverage
// regressions.
//
// This test is the authoritative gate for the "complete MIR-direct
// pipeline" milestone. Earlier versions accepted ErrUnsupported from
// GenerateFromMIR and only required the legacy fallback to succeed; the
// merged native toolchain is now expected to produce LLVM IR on the MIR
// path itself.
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
	files, _, err := collectToolchainProbeFiles(dir, true)
	if err != nil {
		t.Fatalf("read toolchain: %v", err)
	}
	merged := mergeToolchainSources(t, root, files)
	file, _ := parser.ParseDiagnostics(merged)
	if file == nil {
		t.Fatalf("merged parse returned nil (%d files, %d bytes)", len(files), len(merged))
	}
	res := resolve.ResolveFileDefault(file, stdlib.LoadCached())
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

	mirMod := mir.Lower(monoMod)
	if mirMod == nil {
		t.Fatalf("mir.Lower returned nil module")
	}
	out, genErr := GenerateFromMIR(mirMod, opts)
	if genErr != nil {
		t.Fatalf("GenerateFromMIR failed on merged native toolchain: %v", genErr)
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
// lowerQualifiedCall), the merged native toolchain carries only a
// small handful of ErrType locals into MIR. Regressions that increase
// that count
// (e.g. a new checker-coverage gap or a reverted recovery helper)
// will re-expand cascading ErrType propagation and fail this gate.
//
// When the number drops below the floor, tighten it.
func TestNativeToolchainMergedMIRErrTypeFloor(t *testing.T) {
	if testing.Short() {
		t.Skip("slow; skipped in -short")
	}
	const floor = 8
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	dir := filepath.Join(root, "toolchain")
	files, _, err := collectToolchainProbeFiles(dir, true)
	if err != nil {
		t.Fatalf("read toolchain: %v", err)
	}
	merged := mergeToolchainSources(t, root, files)
	file, _ := parser.ParseDiagnostics(merged)
	if file == nil {
		t.Fatalf("merged parse returned nil")
	}
	res := resolve.ResolveFileDefault(file, stdlib.LoadCached())
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
