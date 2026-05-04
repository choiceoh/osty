package llvmgen

import (
	"strings"
	"testing"
)

// TestStructParamScalarFieldAssignBugIsPinned documents the
// foundational codegen bug that blocks Phase-7 Slice 3 (self-host
// MIR body emission). The bug:
//
//   When a function takes a struct parameter (`fn opAdvance(p:
//   OstyParser)`) and writes to a scalar field (`p.pos = p.pos +
//   1`), the LLVM IR currently allocates a local copy of the struct
//   on entry and mutates that copy. The caller's struct is
//   unchanged on return — so any iteration that depends on advancing
//   a parser cursor never advances, producing an infinite loop.
//
// Concretely, `osty-self compile fn x() {}` SIGABRTs because
// `toolchain/parser.osty`'s `opParseFile` loops forever — every
// `opAdvance(p)` call mutates a fresh copy of `p`, so `p.pos`
// stays at 0 and `opAt(p, FrontEOF)` never fires.
//
// The Go-side seed in `internal/selfhost/generated.go` translates
// `fn opAdvance(p: OstyParser)` to `func opAdvance(p *OstyParser)`
// — by-pointer — which is why production CLI works on the same
// toolchain source. The LLVM emitter diverges: `define %FrontToken
// @opAdvance(%OstyParser %arg0)` passes the struct value, copies it
// into a local alloca, mutates the local, and returns. The IR is
// structurally valid; the semantics are wrong.
//
// Three options to fix (memory note
// `project_selfhost_struct_pass_by_value`):
//
//   A. Codegen rewrite — pass struct params as `ptr` and emit
//      load/store + GEP for field reads / writes. Most surgical
//      but touches every emit site that handles struct params.
//   B. `mut p: T` migration — add the explicit mut-param keyword
//      to every mutating fn in `toolchain/{parser,hir_lower,
//      mir_lower,lir_proto}.osty` and have codegen treat `mut`
//      params as by-pointer. Spec-clean but huge churn.
//   C. Functional rewrite — return new struct state from each
//      mutating fn. Biggest churn, smallest codegen change.
//
// This test currently SKIPS so the broken-by-default CI stays
// green. Removing the `t.Skip` is the regression-locking step
// when the chosen fix lands.
func TestStructParamScalarFieldAssignBugIsPinned(t *testing.T) {
	t.Skip("blocked by struct-by-value param codegen; see project_selfhost_struct_pass_by_value memory note + Phase-7 Slice 3 plan")

	src := `struct Counter { pub n: Int }

fn bump(c: Counter) {
    c.n = c.n + 1
}

fn main() {
    let mut c = Counter { n: 0 }
    bump(c)
    bump(c)
    bump(c)
    println("{c.n}")
}
`
	mod := lowerSrcLLVM(t, src)
	ir, err := GenerateModule(mod, Options{PackageName: "main", SourcePath: "/tmp/struct_param_mut.osty"})
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	got := string(ir)

	// Expected behavior once the codegen treats struct params as
	// by-pointer: `bump` declares a `ptr` param, dereferences for
	// the read, GEPs to field 0 for the store, and the caller
	// passes the address of `c`'s alloca slot.
	for _, want := range []string{
		"define void @bump(ptr",
		"call void @bump(ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("post-fix IR missing %q:\n%s", want, got)
		}
	}
	// And the value-by-value pattern that is currently emitted
	// must NOT appear once the fix lands.
	for _, unwanted := range []string{
		"define void @bump(%Counter",
		"call void @bump(%Counter",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("post-fix IR still contains the by-value pattern %q:\n%s", unwanted, got)
		}
	}
}

// TestStructParamScalarFieldAssignReproducesTheCurrentBuggyShape
// is the inverse — it ASSERTS the current buggy IR shape so a
// silent codegen change doesn't drop the contract without anyone
// noticing. When option A/B/C lands, this test will start failing,
// at which point the `Pinned` test above should be unskipped and
// this reproducer test deleted.
func TestStructParamScalarFieldAssignReproducesTheCurrentBuggyShape(t *testing.T) {
	src := `struct Counter { pub n: Int }

fn bump(c: Counter) {
    c.n = c.n + 1
}

fn main() {
    let mut c = Counter { n: 0 }
    bump(c)
    println("{c.n}")
}
`
	mod := lowerSrcLLVM(t, src)
	ir, err := GenerateModule(mod, Options{PackageName: "main", SourcePath: "/tmp/struct_param_mut_buggy.osty"})
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	got := string(ir)
	// The bug: bump takes the struct by value (`%Counter`, not
	// `ptr`), copies into a local alloca, mutates the local. This
	// IR shape is what produces the parser infinite loop in
	// `toolchain/parser.osty::opParseFile` when running osty-self.
	if !strings.Contains(got, "define void @bump(%Counter ") {
		t.Fatalf("expected current buggy by-value param shape `define void @bump(%%Counter ` not present:\n%s", got)
	}
}
