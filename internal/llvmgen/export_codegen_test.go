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

// TestExportSymbolFlowsThroughMIR verifies the §19.6 `#[export("name")]`
// contract end-to-end via the MIR pipeline: a manually constructed
// ir.FnDecl with ExportSymbol set propagates through ir → mir → LLVM
// emission and produces `@<exact-symbol>` in the output instead of
// the mangled default.
//
// This is the first backend-side test for the GC self-hosting work.
// The corresponding front-end pieces (`#[export]` annotation, arg
// validator, AST → IR extraction) live in PRs #284, #316, and the
// extractExportSymbol hook added in this PR.
func TestExportSymbolFlowsThroughMIR(t *testing.T) {
	// Build a minimal IR module with one exported fn:
	//   #[export("osty.gc.spike_v1")]
	//   pub fn spike_v1() -> Int { 0 }
	mod := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:         "spike_v1",
				ExportSymbol: "osty.gc.spike_v1",
				Return:       &ir.PrimType{Kind: ir.PrimInt},
				Exported:     true,
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
	// Verify the MIR function carries ExportSymbol.
	var fn *mir.Function
	for _, f := range mirMod.Functions {
		if f != nil && f.Name == "spike_v1" {
			fn = f
			break
		}
	}
	if fn == nil {
		t.Fatalf("spike_v1 not found in MIR functions; have %d functions", len(mirMod.Functions))
	}
	if fn.ExportSymbol != "osty.gc.spike_v1" {
		t.Fatalf("MIR function ExportSymbol = %q, want %q", fn.ExportSymbol, "osty.gc.spike_v1")
	}

	// Now drive the MIR generator end-to-end and check the LLVM IR.
	out, err := GenerateFromMIR(mirMod, Options{PackageName: "main", SourcePath: "/tmp/spike.osty"})
	if err != nil {
		t.Fatalf("mir generator error: %v", err)
	}
	ir2 := string(out)
	// Must contain a definition for the exported symbol.
	if !strings.Contains(ir2, "@osty.gc.spike_v1") {
		t.Fatalf("LLVM IR missing @osty.gc.spike_v1 reference:\n%s", ir2)
	}
	// Must NOT contain the mangled-default form (just `@spike_v1` as a
	// direct definition would indicate the override didn't apply).
	// We use a strict prefix check: `define ... @spike_v1(` would be
	// the unmangled fallback name.
	if strings.Contains(ir2, "define i64 @spike_v1(") {
		t.Fatalf("LLVM IR fell back to mangled-default name `@spike_v1` instead of using ExportSymbol:\n%s", ir2)
	}
}

// TestExportSymbolEmptyFallsBackToMangled verifies the fallback path:
// when ExportSymbol is "", emission uses fn.Name as before. Guards
// against the spike accidentally always overriding.
func TestExportSymbolEmptyFallsBackToMangled(t *testing.T) {
	mod := &ir.Module{
		Package: "main",
		Decls: []ir.Decl{
			&ir.FnDecl{
				Name:     "ordinary",
				Return:   &ir.PrimType{Kind: ir.PrimInt},
				Exported: true,
				Body: &ir.Block{
					Result: &ir.IntLit{Text: "42"},
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
	if !strings.Contains(string(out), "@ordinary") {
		t.Fatalf("LLVM IR missing default fn name @ordinary:\n%s", out)
	}
}

// TestExportSymbolFromSourceFile drives the FULL pipeline (parser →
// resolver → check → ir.Lower → ir.Monomorphize → mir.Lower →
// GenerateFromMIR) on a source string with `#[export("name")]` and
// verifies the symbol survives. Without the `extractExportSymbol`
// hook in `internal/ir/lower.go`, the front-end would discard the
// annotation before MIR ever sees it.
func TestExportSymbolFromSourceFile(t *testing.T) {
	src := `
#[export("osty.gc.test_v1")]
pub fn test_v1() -> Int {
    7
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

	// Spot-check: the `#[export]` annotation flowed into ir.FnDecl.
	var found bool
	for _, decl := range mod.Decls {
		if fd, ok := decl.(*ir.FnDecl); ok && fd.Name == "test_v1" {
			if fd.ExportSymbol != "osty.gc.test_v1" {
				t.Fatalf("ir.FnDecl ExportSymbol = %q, want %q",
					fd.ExportSymbol, "osty.gc.test_v1")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("test_v1 not found in IR module")
	}

	monoMod, monoErrs := ir.Monomorphize(mod)
	if len(monoErrs) != 0 {
		t.Fatalf("monomorphize: %v", monoErrs)
	}
	mirMod := mir.Lower(monoMod)
	if mirMod == nil {
		t.Fatal("mir.Lower returned nil")
	}
	out, err := GenerateFromMIR(mirMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/test_export.osty",
	})
	if err != nil {
		t.Fatalf("GenerateFromMIR error: %v", err)
	}
	if !strings.Contains(string(out), "@osty.gc.test_v1") {
		t.Fatalf("LLVM IR missing the exported symbol @osty.gc.test_v1:\n%s", out)
	}
}
