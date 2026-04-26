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

// TestCABIFlowsThroughMIR verifies the §19.6 `#[c_abi]` contract
// end-to-end via the MIR pipeline: a manually constructed ir.FnDecl
// with CABI = true propagates through ir → mir → LLVM emission and
// causes the `ccc` calling-convention keyword to appear in the
// emitted `define` line.
func TestCABIFlowsThroughMIR(t *testing.T) {
	mod := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:     "abi_v1",
				CABI:     true,
				Return:   &ir.PrimType{Kind: ir.PrimInt},
				Exported: true,
				Body: &ir.Block{
					Result: &ir.IntLit{Text: "0"},
				},
			},
		},
	}
	monoMod, errs := ir.Monomorphize(mod)
	if len(errs) > 0 {
		t.Fatalf("monomorphize errors: %v", errs)
	}
	mirMod := mir.Lower(monoMod)
	if mirMod == nil {
		t.Fatal("mir.Lower returned nil")
	}
	var fn *mir.Function
	for _, f := range mirMod.Functions {
		if f != nil && f.Name == "abi_v1" {
			fn = f
			break
		}
	}
	if fn == nil {
		t.Fatalf("abi_v1 not found in MIR functions")
	}
	if !fn.CABI {
		t.Fatalf("MIR function CABI = false, want true")
	}

	out, err := GenerateFromMIR(mirMod, Options{PackageName: "main", SourcePath: "/tmp/cabi.osty"})
	if err != nil {
		t.Fatalf("mir generator error: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "define ccc i64 @abi_v1") {
		t.Fatalf("LLVM IR missing `define ccc` for CABI fn:\n%s", got)
	}
}

// TestCABINotSetEmitsDefault verifies the absence path: CABI=false
// produces the same output as before this PR (no `ccc` keyword).
func TestCABINotSetEmitsDefault(t *testing.T) {
	mod := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:     "ordinary_v1",
				Return:   &ir.PrimType{Kind: ir.PrimInt},
				Exported: true,
				Body: &ir.Block{
					Result: &ir.IntLit{Text: "0"},
				},
			},
		},
	}
	monoMod, _ := ir.Monomorphize(mod)
	mirMod := mir.Lower(monoMod)
	out, err := GenerateFromMIR(mirMod, Options{PackageName: "main", SourcePath: "/tmp/ord.osty"})
	if err != nil {
		t.Fatalf("mir generator error: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "define ccc") {
		t.Fatalf("ordinary fn must not emit `ccc`:\n%s", got)
	}
	if !strings.Contains(got, "define i64 @ordinary_v1") {
		t.Fatalf("ordinary fn missing default emit:\n%s", got)
	}
}

// TestCABIFromSourceFile drives the FULL pipeline (parser → resolver
// → check → ir.Lower → ir.Monomorphize → mir.Lower → GenerateFromMIR)
// on a source string with `#[c_abi]` (and the canonical paired
// `#[export]` + `#[no_alloc]`) and verifies BOTH the symbol name
// override and the calling-convention keyword survive.
func TestCABIFromSourceFile(t *testing.T) {
	src := `
#[export("osty.gc.cabi_v1")]
#[c_abi]
#[no_alloc]
pub fn cabi_v1() -> Int {
    9
}

fn main() {}
`
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
	mod, issues := ir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower issues: %v", issues)
	}
	var found *ir.FnDecl
	for _, decl := range mod.Decls {
		if fd, ok := decl.(*ir.FnDecl); ok && fd.Name == "cabi_v1" {
			found = fd
			break
		}
	}
	if found == nil {
		t.Fatal("cabi_v1 not found in IR module")
	}
	if !found.CABI {
		t.Fatal("ir.FnDecl CABI = false, expected true (#[c_abi] not propagated from AST)")
	}
	if found.ExportSymbol != "osty.gc.cabi_v1" {
		t.Fatalf("ExportSymbol = %q, want osty.gc.cabi_v1", found.ExportSymbol)
	}

	monoMod, _ := ir.Monomorphize(mod)
	mirMod := mir.Lower(monoMod)
	out, err := GenerateFromMIR(mirMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/cabi_e2e.osty",
	})
	if err != nil {
		t.Fatalf("GenerateFromMIR error: %v", err)
	}
	got := string(out)
	want := "define ccc i64 @osty.gc.cabi_v1"
	if !strings.Contains(got, want) {
		t.Fatalf("LLVM IR missing combined `%s`:\n%s", want, got)
	}
}
