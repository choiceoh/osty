package mir

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// TestLowerNestedStructBindingPatternEndToEnd covers the canonical
// nested-binding destructure from CLAUDE.md §A.5 — but driven from
// source through the full parse → resolve → check → ir.Lower →
// Monomorphize → mir.Lower pipeline. Complements the IR-shape
// regression `TestLowerNestedStructBindingPatternKeepsAliasAndLeaves`
// (which builds the IR manually) by guaranteeing the front-end
// lowering keeps the projection chain intact for the *non-alias*
// pattern variant too:
//
//	let User { addr: Address { city, zip }, score } = u
//	score + city + zip
//
// Plan entry: 🔴 "Pattern lowering — nested binding / destructuring"
// in `LLVM_MIGRATION_PLAN.md` Tier A (`recursive extractvalue plus
// name @ pattern alias`). The LIR Proto `source_nested_struct_binding`
// fixture locks the LLVM-side `extractvalue` cascade; this test
// locks the matching MIR-side projection cascade so a regression at
// either layer surfaces in isolation.
//
// Asserts each inner field is read by a single dotted projection
// (`use _<scrutinee>.addr.city`, …) rather than via an intermediate
// aggregate copy. The `addr` field never materialises into its own
// local — only the leaves (`city`, `zip`, `score`) do. If a future
// scrutinee-walking change introduces a temporary aggregate, this
// test fails before the LIR Proto emitter sees the regression.
func TestLowerNestedStructBindingPatternEndToEnd(t *testing.T) {
	src := `struct Address {
    city: Int,
    zip: Int,
}

struct User {
    addr: Address,
    score: Int,
}

fn check(u: User) -> Int {
    let User { addr: Address { city, zip }, score } = u
    score + city + zip
}
`
	mirText := lowerSourceToMIR(t, src)
	for _, want := range []string{
		"use _2.addr.city",
		"use _2.addr.zip",
		"use _2.score",
	} {
		if !strings.Contains(mirText, want) {
			t.Errorf("missing %q in MIR:\n%s", want, mirText)
		}
	}
	for _, ban := range []string{
		"unsupported projection",
		"projection on non-aggregate",
	} {
		if strings.Contains(mirText, ban) {
			t.Errorf("MIR contains GAP-INSTR-006 marker %q:\n%s", ban, mirText)
		}
	}
	// The intermediate `addr` binding must NOT materialise — only
	// the leaves get scratch slots. A naive walker would emit
	// something like `_X: Address  // addr` here.
	if strings.Contains(mirText, "// addr") {
		t.Errorf("intermediate `addr` field unexpectedly materialised:\n%s", mirText)
	}
}

func lowerSourceToMIR(t *testing.T, src string) string {
	t.Helper()
	file, diags := parser.ParseDiagnostics([]byte(src))
	if len(diags) != 0 {
		t.Fatalf("parse diags: %v", diags)
	}
	reg := stdlib.LoadCached()
	res := resolve.ResolveFileSourceDefault([]byte(src), file, reg)
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
	})
	mod, _ := ir.Lower("main", file, res, chk)
	if mod == nil {
		t.Fatal("nil hir module")
	}
	monoMod, _ := ir.Monomorphize(mod)
	if monoMod == nil {
		monoMod = mod
	}
	mirMod := Lower(monoMod)
	return Print(mirMod)
}
