package ir

import (
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// lowerSrc parses, resolves, and lowers a privileged-mode source
// snippet to an ir.Module. Used by the runtime-annotation IR
// propagation tests.
func lowerSrc(t *testing.T, src string) *Module {
	t.Helper()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse: %v", parseDiags)
	}
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.File(file, res, check.Opts{
		UseGolegacy:   true,
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
		Privileged:    true,
	})
	mod, _ := Lower("main", file, res, chk)
	return mod
}

// --- #[no_alloc] -> ir.FnDecl.NoAlloc ---

func TestNoAllocFlowsToFnDecl(t *testing.T) {
	src := `
#[no_alloc]
pub fn pure_arith(a: Int, b: Int) -> Int {
    a + b
}
`
	mod := lowerSrc(t, src)
	for _, decl := range mod.Decls {
		if fd, ok := decl.(*FnDecl); ok && fd.Name == "pure_arith" {
			if !fd.NoAlloc {
				t.Fatal("ir.FnDecl.NoAlloc not set from #[no_alloc]")
			}
			return
		}
	}
	t.Fatal("pure_arith not in module decls")
}

func TestNoAllocAbsenceIsFalse(t *testing.T) {
	src := `pub fn ordinary() -> Int { 0 }`
	mod := lowerSrc(t, src)
	for _, decl := range mod.Decls {
		if fd, ok := decl.(*FnDecl); ok && fd.Name == "ordinary" && fd.NoAlloc {
			t.Fatal("plain fn must not carry NoAlloc")
		}
	}
}

// --- #[pod] / #[repr] -> ir.StructDecl.Pod / ReprC ---

func TestPodFlowsToStructDecl(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
pub struct Header {
    pub size: Int32,
}
`
	mod := lowerSrc(t, src)
	for _, decl := range mod.Decls {
		if sd, ok := decl.(*StructDecl); ok && sd.Name == "Header" {
			if !sd.Pod {
				t.Fatal("ir.StructDecl.Pod not set from #[pod]")
			}
			if !sd.ReprC {
				t.Fatal("ir.StructDecl.ReprC not set from #[repr(c)]")
			}
			return
		}
	}
	t.Fatal("Header not in module decls")
}

func TestPodAbsenceIsFalse(t *testing.T) {
	src := `pub struct User { pub name: String }`
	mod := lowerSrc(t, src)
	for _, decl := range mod.Decls {
		if sd, ok := decl.(*StructDecl); ok && sd.Name == "User" {
			if sd.Pod {
				t.Fatal("plain struct must not carry Pod")
			}
			if sd.ReprC {
				t.Fatal("plain struct must not carry ReprC")
			}
			return
		}
	}
	t.Fatal("User not in module decls")
}

// --- combined: every runtime annotation in one fn ---

func TestAllRuntimeAnnotationsCoexistOnFn(t *testing.T) {
	src := `
#[export("osty.gc.combo_v1")]
#[c_abi]
#[no_alloc]
pub fn combo_v1() -> Int {
    7
}
`
	mod := lowerSrc(t, src)
	for _, decl := range mod.Decls {
		if fd, ok := decl.(*FnDecl); ok && fd.Name == "combo_v1" {
			if fd.ExportSymbol != "osty.gc.combo_v1" {
				t.Errorf("ExportSymbol = %q", fd.ExportSymbol)
			}
			if !fd.CABI {
				t.Error("CABI not set")
			}
			if !fd.NoAlloc {
				t.Error("NoAlloc not set")
			}
			if fd.IsIntrinsic {
				t.Error("IsIntrinsic must NOT be set (no #[intrinsic])")
			}
			return
		}
	}
	t.Fatal("combo_v1 not in module decls")
}
