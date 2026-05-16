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

// TestLowerClosureArgInfersParamsFromMethodReceiver covers the
// canonical "closure arg to stdlib higher-order method" shape
// where the user doesn't annotate the closure params:
//
//	xs.filter(|n| n > 0)         // List<Int>.filter
//	opt.map(|x| x * 2)           // Option<Int>.map
//	r.map(|n| n * 2)             // Result<Int, _>.map
//
// Plan entry: 🟡 closure parameter inference (from CLAUDE.md
// §A.5 examples) — historically the checker only inferred these
// in a narrow subset of shapes and the rest dropped to
// `<error>`-typed params + poisoned bodies. The IR lowerer's
// `backfillClosureArgsFromMethodCall` derives the expected
// closure signature from the receiver type + method name table
// (`expectedClosureFnTypeForBuiltin`) and writes back into the
// closure's Params / Return / T slots, then walks the body to
// re-type Idents referring to backfilled params so propagated
// expressions (`n > 0`, `x * 2`, `box.n`) recover concrete types
// without needing a checker round-trip.
//
// Each subtest asserts:
//
//  1. the closure's resulting `T` is a concrete `fn(T) -> R`;
//  2. the closure's `Return` matches the expected output type
//     (Bool for predicates; the same numeric type for arithmetic
//     identity maps; Unit for forEach / inspect);
//  3. each closure param's `Type` is filled in (no nil);
//  4. the lowered MIR has no `<error>` leaks.
func TestLowerClosureArgInfersParamsFromMethodReceiver(t *testing.T) {
	cases := []struct {
		name         string
		src          string
		wantParamT   string
		wantReturnT  string
		wantClosureT string
	}{
		{
			name:         "list_map",
			src:          `fn double(xs: List<Int>) -> List<Int> { xs.map(|x| x * 2) }`,
			wantParamT:   "Int",
			wantReturnT:  "Int",
			wantClosureT: "fn(Int) -> Int",
		},
		{
			name:         "list_filter",
			src:          `fn pick(xs: List<Int>) -> List<Int> { xs.filter(|n| n > 0) }`,
			wantParamT:   "Int",
			wantReturnT:  "Bool",
			wantClosureT: "fn(Int) -> Bool",
		},
		{
			name:         "option_map",
			src:          `fn double(opt: Int?) -> Int? { opt.map(|x| x * 2) }`,
			wantParamT:   "Int",
			wantReturnT:  "Int",
			wantClosureT: "fn(Int) -> Int",
		},
		{
			name:         "option_filter",
			src:          `fn pick(opt: Int?) -> Int? { opt.filter(|n| n > 0) }`,
			wantParamT:   "Int",
			wantReturnT:  "Bool",
			wantClosureT: "fn(Int) -> Bool",
		},
		{
			name:         "result_map",
			src:          `fn double(r: Result<Int, String>) -> Result<Int, String> { r.map(|n| n * 2) }`,
			wantParamT:   "Int",
			wantReturnT:  "Int",
			wantClosureT: "fn(Int) -> Int",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			file, _ := parser.ParseDiagnostics([]byte(c.src))
			reg := stdlib.LoadCached()
			res := resolve.ResolveFileSourceDefault([]byte(c.src), file, reg)
			chk := check.SelfhostFile(file, res, check.Opts{
				Stdlib: reg, Primitives: reg.Primitives, ResultMethods: reg.ResultMethods, Source: []byte(c.src),
			})
			mod, _ := ir.Lower("main", file, res, chk)
			var found *ir.Closure
			for _, d := range mod.Decls {
				if fn, ok := d.(*ir.FnDecl); ok {
					ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
						if cl, ok := n.(*ir.Closure); ok && found == nil {
							found = cl
						}
						return true
					}), fn)
				}
			}
			if found == nil {
				t.Fatalf("no closure in lowered IR")
			}
			if got := typeStr(found.T); got != c.wantClosureT {
				t.Errorf("Closure.T = %q, want %q", got, c.wantClosureT)
			}
			if got := typeStr(found.Return); got != c.wantReturnT {
				t.Errorf("Closure.Return = %q, want %q", got, c.wantReturnT)
			}
			if len(found.Params) == 0 || found.Params[0] == nil {
				t.Fatalf("Closure.Params[0] missing")
			}
			if got := typeStr(found.Params[0].Type); got != c.wantParamT {
				t.Errorf("Closure.Params[0].Type = %q, want %q", got, c.wantParamT)
			}
			mirText := lowerSourceToMIR(t, c.src)
			if strings.Contains(mirText, "<error>") {
				t.Errorf("ErrTypeVal leaked into MIR for %s:\n%s", c.name, mirText)
			}
		})
	}
}

func typeStr(t ir.Type) string {
	if t == nil {
		return "<nil>"
	}
	return t.String()
}
