package llvmgen

// LANG_SPEC §19.5 — `raw.null()` LLVM lowering.
//
// MIR rewrites the call into `IntrinsicRawNull` (no symbol, no
// CallInstr); the LLVM emitter stores the opaque-pointer constant
// `null` into the destination slot. There is no runtime symbol — the
// pointer-shaped null constant is materialised inline.

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	ir "github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

const rawNullSrc = `
use std.runtime.raw

#[no_alloc]
pub fn demo() -> RawPtr {
    raw.null()
}

fn main() {}
`

// lowerPrivilegedToMIR runs the full front-end + IR + MIR pipeline on
// a privileged source and returns the MIR module. Tests for §19
// intrinsics share this helper so adding a new intrinsic test is one
// `lowerPrivilegedToMIR(t, src)` line, not 12 lines of boilerplate.
func lowerPrivilegedToMIR(t *testing.T, src string) *mir.Module {
	t.Helper()
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
	mod, issues := ir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower issues: %v", issues)
	}
	monoMod, _ := ir.Monomorphize(mod)
	return mir.Lower(monoMod)
}

func TestRawNullEndToEndLLVMEmit(t *testing.T) {
	mirMod := lowerPrivilegedToMIR(t, rawNullSrc)
	out, err := GenerateFromMIR(mirMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/raw_null.osty",
	})
	if err != nil {
		t.Fatalf("GenerateFromMIR error: %v", err)
	}
	got := string(out)
	// If MIR left raw.null as a CallInstr, the symbol would leak as
	// either the qualified path or the printer label.
	for _, bad := range []string{
		"@std.runtime.raw.null",
		`@"std.runtime.raw.null"`,
		"@raw.null",
	} {
		if strings.Contains(got, bad) {
			t.Fatalf("MIR left raw.null as a call instead of intercepting (found %q):\n%s", bad, got)
		}
	}
	// `ptr null` is the canonical opaque-pointer constant in LLVM IR
	// and is what storeIntrinsicResult emits for IntrinsicRawNull.
	if !strings.Contains(got, "ptr null") {
		t.Fatalf("expected `ptr null` constant in emitted LLVM IR:\n%s", got)
	}
}

func TestRawNullMIRIntrinsicShape(t *testing.T) {
	mirMod := lowerPrivilegedToMIR(t, rawNullSrc)

	var fn *mir.Function
	var seenNames []string
	for _, f := range mirMod.Functions {
		if f == nil {
			continue
		}
		seenNames = append(seenNames, f.Name)
		if f.Name == "demo" {
			fn = f
		}
	}
	if fn == nil {
		t.Fatalf("demo not in MIR module; saw functions %v", seenNames)
	}
	var found *mir.IntrinsicInstr
	var seenInstrs []string
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		for _, inst := range bb.Instrs {
			if ii, ok := inst.(*mir.IntrinsicInstr); ok {
				if ii.Kind == mir.IntrinsicRawNull {
					found = ii
					break
				}
				seenInstrs = append(seenInstrs, "intrinsic")
			} else {
				seenInstrs = append(seenInstrs, "other")
			}
		}
		if found != nil {
			break
		}
	}
	if found == nil {
		t.Fatalf("MIR demo body has no IntrinsicRawNull — call was not intercepted; instrs=%v", seenInstrs)
	}
	if len(found.Args) != 0 {
		t.Fatalf("IntrinsicRawNull arity = %d, want 0", len(found.Args))
	}
}
