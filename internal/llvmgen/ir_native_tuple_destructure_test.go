package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	ostyir "github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// TestTryGenerateNativeOwnedModuleCoversLetTupleDestructureIdent
// checks the RHS-is-bare-ident fast path: no temp is minted, the
// four synthetic `let` bindings all extract directly off the
// source ident. This is the shape the Phase 2 TupleTable test
// uses at the end of the for-in loop.
func TestTryGenerateNativeOwnedModuleCoversLetTupleDestructureIdent(t *testing.T) {
	src := `fn main() {
    let c = (5, 0, 10, 5)
    let (v, lo, hi, expected) = c
    println(v)
    println(lo)
    println(hi)
    println(expected)
}
`
	mod := lowerNativeEntryModule(t, src)
	out, ok, err := TryGenerateNativeOwnedModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_tuple_destructure_ident.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule errored: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported uncovered for let-tuple-destructure on bare ident")
	}
	got := string(out)
	for _, want := range []string{
		"%Tuple.i64.i64.i64.i64",
		"extractvalue %Tuple.i64.i64.i64.i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("native-owned IR missing %q:\n%s", want, got)
		}
	}
}

// TestTryGenerateNativeOwnedModuleCoversLetTupleDestructureSpill
// checks the spill-to-temp path: the RHS is a non-ident expression
// (function call), so the helper mints a fresh `__osty_native_t<N>`
// binding and reuses it across the element extractions.
func TestTryGenerateNativeOwnedModuleCoversLetTupleDestructureSpill(t *testing.T) {
	src := `fn makePair() -> (Int, Int) {
    (7, 9)
}

fn main() {
    let (a, b) = makePair()
    println(a + b)
}
`
	mod := lowerNativeEntryModule(t, src)
	out, ok, err := TryGenerateNativeOwnedModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_tuple_destructure_spill.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule errored: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported uncovered for let-tuple-destructure on fn-call RHS")
	}
	got := string(out)
	// The native SSA namer mints its own `%tN` labels at emit time,
	// so the synthesized `__osty_native_t<N>` user-level name
	// doesn't leak into the LLVM IR verbatim. Structurally verify:
	// the spill call, the tuple type, and two field extracts.
	for _, want := range []string{
		"%Tuple.i64.i64 = type { i64, i64 }",
		"call %Tuple.i64.i64 @makePair()",
		"extractvalue %Tuple.i64.i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("native-owned IR missing %q:\n%s", want, got)
		}
	}
}

// TestTryGenerateNativeOwnedModuleCoversForInOverTupleList locks
// the combined for-in-over-list + let-tuple-destructure wiring used
// by the Phase 2 `TupleTableDrivenLoop` source. The native path
// must both iterate a `List<Tuple>` (via the range expansion over
// `list.len()`) and pattern-match each element back into four
// named i64 bindings.
func TestTryGenerateNativeOwnedModuleCoversForInOverTupleList(t *testing.T) {
	src := `fn clamp(v: Int, lo: Int, hi: Int) -> Int {
    if v < lo { lo } else if v > hi { hi } else { v }
}

fn main() {
    let cases = [
        (5, 0, 10, 5),
        (-1, 0, 10, 0),
        (99, 0, 10, 10),
    ]
    for c in cases {
        let (v, lo, hi, expected) = c
        println(clamp(v, lo, hi) - expected)
    }
}
`
	file := parseLLVMGenFile(t, src)
	res := resolve.ResolveFileDefault(file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{

		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
	})
	mod, issues := ostyir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower issues: %v", issues)
	}
	monoMod, monoErrs := ostyir.Monomorphize(mod)
	if len(monoErrs) != 0 {
		t.Fatalf("ir.Monomorphize errors: %v", monoErrs)
	}
	out, ok, err := TryGenerateNativeOwnedModule(monoMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_for_in_tuple.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule errored: %v", err)
	}
	if !ok {
		t.Fatal("TryGenerateNativeOwnedModule reported uncovered for tuple-table-driven loop")
	}
	got := string(out)
	for _, want := range []string{
		"%Tuple.i64.i64.i64.i64",
		"call void @osty_rt_list_push_bytes_v1(",
		"call void @osty_rt_list_get_bytes_v1(",
		"extractvalue %Tuple.i64.i64.i64.i64",
		"call i64 @osty_rt_list_len(ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("native-owned IR missing %q:\n%s", want, got)
		}
	}
}

// TestNativeLetTupleDestructureRejectsNonIdentElement — destructure
// elements other than plain `IdentPat` bindings defer to the legacy
// bridge until follow-up coverage lands. Nested `let (a, (b, c)) = t`
// is the canonical example; the whole module must stay uncovered
// natively so GenerateModule routes to the AST path.
func TestNativeLetTupleDestructureRejectsNonIdentElement(t *testing.T) {
	src := `fn main() {
    let nested = ((1, 2), 3)
    let ((a, b), c) = nested
    println(a + b + c)
}
`
	mod := lowerNativeEntryModule(t, src)
	_, ok, err := TryGenerateNativeOwnedModule(mod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/native_tuple_destructure_reject.osty",
	})
	if err != nil {
		t.Fatalf("TryGenerateNativeOwnedModule errored: %v", err)
	}
	if ok {
		t.Fatal("TryGenerateNativeOwnedModule unexpectedly covered nested tuple destructure — stage-1 should defer")
	}
	_ = resolve.ResolveFileDefault // force import used on other tests
	_ = check.SelfhostFile
	_ = stdlib.LoadCached
	_ = ostyir.ForIn
}
