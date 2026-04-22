package llvmgen

import (
	"errors"
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// TestMIRDirectCoversFormerFallbackWalls is the regression gate for
// five MIR-direct walls that previously forced GenerateFromMIR to
// surface ErrUnsupported and delegate to the legacy HIR→AST emitter:
//
//   - Downcast        : `recv.downcast::<T>()` on an interface value
//     — rejected with "unsupported local type <Iface>".
//   - EnumWiden       : a user enum used as `Result<_, E>` Err payload
//     — rejected with "cannot widen %<Enum> to i64".
//   - TestingContext  : `std.testing.context(label, closure)`
//     — rejected with "unresolved symbol std.testing.*".
//   - TestingBenchmark: `std.testing.benchmark(n, closure)` with `Ok(())`
//     — rejected with "tuple aggregate type ()".
//   - TestingSnapshot : `std.testing.snapshot(name, output)` —
//     previously fell through to an unresolved
//     `std.testing.*` symbol because the MIR
//     dispatcher did not forward it to the runtime
//     helper.
//
// Each sub-test calls GenerateFromMIR ONLY (no legacy fallback) and
// asserts that the MIR emitter now produces the shape-critical IR
// substrings directly. Regressions that re-surface any of these walls
// will fail this gate even while the sibling HIR-capable tests keep
// passing via their fallback path.
func TestMIRDirectCoversFormerFallbackWalls(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "Downcast",
			src: `interface Printable {
    fn show(self) -> String
}
struct Note {
    pub msg: String,
    pub fn show(self) -> String { self.msg }
}
fn probe(p: Printable) -> Note? { p.downcast::<Note>() }
fn main() {}
`,
			want: []string{
				"%osty.iface = type { ptr, ptr }",
				"@osty.vtable.Note__Printable",
				"extractvalue %osty.iface",
				"icmp eq ptr",
				"select i1",
			},
		},
		{
			name: "EnumWiden",
			src: `enum CalcError { DivideByZero, }
fn div(a: Int, b: Int) -> Result<Int, CalcError> {
    if b == 0 { Err(DivideByZero) } else { Ok(a / b) }
}
fn main() { let _ = div(1, 0) }
`,
			want: []string{
				"ptrtoint ptr",
				"@osty.gc.alloc_v1",
			},
		},
		{
			name: "TestingContext",
			src: `use std.testing as t
fn add(a: Int, b: Int) -> Int { a + b }
fn main() {
    t.context("simple", || { t.assertEq(add(1, 2), 3) })
}
`,
			want: []string{
				"call i64 @add(i64 1, i64 2)",
			},
		},
		{
			name: "TestingBenchmark",
			src: `use std.testing
fn add(a: Int, b: Int) -> Int { a + b }
fn main() {
    testing.benchmark(5, || {
        let _ = add(1, 2)
        Ok(())
    })
}
`,
			want: []string{
				"call i64 @add(i64 1, i64 2)",
			},
		},
		{
			name: "TestingSnapshot",
			src: `use std.testing as testing
fn main() {
    testing.snapshot("golden", "hello\nworld\n")
}
`,
			want: []string{
				"declare void @osty_rt_test_snapshot(ptr, ptr, ptr)",
				"call void @osty_rt_test_snapshot(",
				"/tmp/probe_4walls.osty",
			},
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			f := parseLLVMGenFile(t, c.src)
			res := resolve.FileWithStdlib(f, resolve.NewPrelude(), stdlib.LoadCached())
			reg := stdlib.LoadCached()
			chk := check.File(f, res, check.Opts{
				UseGolegacy: true, Stdlib: reg, Primitives: reg.Primitives,
				ResultMethods: reg.ResultMethods, Source: []byte(c.src),
				Privileged: true,
			})
			mod, iss := ir.Lower("main", f, res, chk)
			if len(iss) != 0 {
				t.Fatalf("ir.Lower issues: %v", iss)
			}
			mono, mErr := ir.Monomorphize(mod)
			if len(mErr) != 0 {
				t.Fatalf("mono errs: %v", mErr)
			}
			mirMod := mir.Lower(mono)
			if mirMod == nil {
				t.Fatal("mir.Lower nil")
			}
			out, err := GenerateFromMIR(mirMod, Options{PackageName: "main", SourcePath: "/tmp/probe_4walls.osty"})
			if err != nil {
				if errors.Is(err, ErrUnsupported) {
					t.Fatalf("MIR-direct still returns ErrUnsupported: %v", err)
				}
				t.Fatalf("MIR-direct hard error: %v", err)
			}
			got := string(out)
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Fatalf("MIR-direct IR missing %q", w)
				}
			}
		})
	}
}
