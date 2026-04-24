package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	ir "github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// TestIntrinsicFlagFlowsThroughIRAndMIR verifies the §19.5 / §19.6
// `#[intrinsic]` annotation propagates through the front-end into
// `ir.FnDecl.IsIntrinsic` and onward to `mir.Function.IsIntrinsic`.
//
// The flag exists from earlier work; this PR is the first to set it
// from the user-facing annotation. The MIR backend already refuses
// to compile modules containing intrinsic functions ("mir-mvp:
// intrinsic function declaration X") — that guard now becomes
// reachable from source.
func TestIntrinsicFlagFlowsThroughIRAndMIR(t *testing.T) {
	// Bodyless `#[intrinsic]` declaration matching the stdlib stub
	// form (`std.runtime.raw.null`, etc.).
	src := `
#[intrinsic]
pub fn raw_null() -> Int

fn main() {}
`
	file := parseLLVMGenFile(t, src)
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{

		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
		Privileged:    true,
	})
	mod, _ := ir.Lower("main", file, res, chk)

	// IR-side check.
	var found *ir.FnDecl
	for _, decl := range mod.Decls {
		if fd, ok := decl.(*ir.FnDecl); ok && fd.Name == "raw_null" {
			found = fd
			break
		}
	}
	if found == nil {
		t.Fatal("raw_null not in IR module decls")
	}
	if !found.IsIntrinsic {
		t.Fatal("ir.FnDecl.IsIntrinsic not set from #[intrinsic] annotation")
	}

	// MIR-side propagation.
	monoMod, _ := ir.Monomorphize(mod)
	mirMod := mir.Lower(monoMod)
	var mirFn *mir.Function
	for _, f := range mirMod.Functions {
		if f != nil && f.Name == "raw_null" {
			mirFn = f
			break
		}
	}
	if mirFn == nil {
		t.Fatal("raw_null not in MIR module functions")
	}
	if !mirFn.IsIntrinsic {
		t.Fatal("mir.Function.IsIntrinsic not propagated from ir.FnDecl")
	}
}

// TestIntrinsicAbsenceIsFalse — regression guard: a fn without
// `#[intrinsic]` must not have IsIntrinsic = true. Catches accidental
// always-true setters in the lowerer.
func TestIntrinsicAbsenceIsFalse(t *testing.T) {
	src := `
pub fn ordinary() -> Int { 42 }

fn main() {}
`
	file := parseLLVMGenFile(t, src)
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{

		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
	})
	mod, _ := ir.Lower("main", file, res, chk)
	for _, decl := range mod.Decls {
		if fd, ok := decl.(*ir.FnDecl); ok && fd.Name == "ordinary" {
			if fd.IsIntrinsic {
				t.Fatal("plain fn must not have IsIntrinsic=true")
			}
		}
	}
	monoMod, _ := ir.Monomorphize(mod)
	mirMod := mir.Lower(monoMod)
	for _, f := range mirMod.Functions {
		if f != nil && f.Name == "ordinary" && f.IsIntrinsic {
			t.Fatal("plain fn's MIR Function must not have IsIntrinsic=true")
		}
	}
}

// TestIntrinsicCausesMIRBackendBail wires the propagation into the
// MIR LLVM backend and verifies the existing "unsupported" guard
// fires. This is the safety net: before per-intrinsic LLVM lowering
// lands, any `#[intrinsic]` fn in a compiled module produces a
// clear error rather than silently emitting a body-less stub.
func TestIntrinsicCausesMIRBackendBail(t *testing.T) {
	mod := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:        "raw_null",
				IsIntrinsic: true,
				Return:      &ir.PrimType{Kind: ir.PrimInt},
				Exported:    true,
			},
		},
	}
	monoMod, _ := ir.Monomorphize(mod)
	mirMod := mir.Lower(monoMod)
	_, err := GenerateFromMIR(mirMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/intrinsic.osty",
	})
	if err == nil {
		t.Fatal("expected MIR backend to bail on intrinsic fn, got nil error")
	}
	if !strings.Contains(err.Error(), "intrinsic") {
		t.Fatalf("expected error to mention 'intrinsic', got: %v", err)
	}
}
