package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

func TestTestingPropertyLowersGeneratorSubset(t *testing.T) {
	src := testingPropertyGeneratorSubsetSource()
	file := parseLLVMGenFile(t, src)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/testing_property_probe.osty",
		Source:      []byte(src),
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare i64 @osty_rt_test_gen_int_range(i64, i64, i64)",
		"call i64 @osty_rt_test_gen_int_range(",
		"declare double @osty_rt_test_gen_float(i64)",
		"call double @osty_rt_test_gen_float(",
		"declare ptr @osty_rt_test_gen_ascii_string(i64, i64)",
		"call ptr @osty_rt_test_gen_ascii_string(",
		"call void @osty_rt_list_push_i64(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func testingPropertyGeneratorSubsetSource() string {
	return `use std.testing as testing
use std.testing.gen as gen

fn main() {
    testing.property(
        "bool",
        gen.bool(),
        |b: Bool| b || !(b),
    )
    testing.property(
        "float",
        gen.float(),
        |f: Float64| f >= 0.0 && f < 1.0,
    )
    testing.property(
        "char",
        gen.char(),
        |c: Char| c.toInt() >= 32 && c.toInt() < 127,
    )
    testing.property(
        "byte",
        gen.byte(),
        |b: Byte| b.toInt() >= 0 && b.toInt() < 256,
    )
    testing.property(
        "range",
        gen.intRange(-5, 5),
        |n: Int| n >= -5 && n < 5,
    )
    testing.property(
        "ascii",
        gen.asciiString(8),
        |s: String| s.len() <= 8,
    )
    testing.property(
        "pair",
        gen.pair(gen.intRange(0, 3), gen.intRange(10, 13)),
        |(a, b): (Int, Int)| a >= 0 && a < 3 && b >= 10 && b < 13,
    )
    testing.property(
        "list",
        gen.list(gen.intRange(0, 5), 4),
        |xs: List<Int>| xs.len() <= 4,
    )
    testing.property(
        "listOfSize",
        gen.listOfSize(gen.bool(), 3),
        |xs: List<Bool>| xs.len() == 3,
    )
    testing.property(
        "option",
        gen.option(gen.intRange(0, 5)),
        |x: Int?| x.isNone() || x.unwrap() >= 0,
    )
    testing.property(
        "result",
        gen.result(gen.intRange(0, 5), gen.constant("err")),
        |r: Result<Int, String>| r.isOk() || r.isErr(),
    )
    testing.property(
        "map",
        gen.map(gen.intRange(0, 5), |n: Int| n + 1),
        |n: Int| n >= 1 && n <= 5,
    )
    testing.property(
        "filter",
        gen.filter(gen.intRange(-8, 8), |n: Int| n >= 0),
        |n: Int| n >= 0,
    )
    testing.property(
        "choices",
        gen.oneOf(["aa", "bbb", "cccc"]),
        |s: String| s == "aa" || s == "bbb" || s == "cccc",
    )
    testing.property(
        "oneOfGens",
        gen.oneOfGens([gen.constant(1), gen.intRange(2, 4)]),
        |n: Int| n == 1 || (n >= 2 && n < 4),
    )
}
`
}

func TestGenerateModuleTestingPropertyGeneratorSubset(t *testing.T) {
	src := testingPropertyGeneratorSubsetSource()
	file := parseLLVMGenFile(t, src)
	res := resolve.ResolveFileSourceDefault([]byte(src), file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{

		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
	})
	mod, issues := ir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower returned issues: %v", issues)
	}
	monoMod, monoErrs := ir.Monomorphize(mod)
	if len(monoErrs) != 0 {
		t.Fatalf("ir.Monomorphize returned errors: %v", monoErrs)
	}
	if validateErrs := ir.Validate(monoMod); len(validateErrs) != 0 {
		t.Fatalf("ir.Validate returned errors: %v", validateErrs)
	}
	out, err := GenerateModule(monoMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/testing_property_ir.osty",
	})
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"declare i64 @osty_rt_test_gen_int_range(i64, i64, i64)",
		"call i64 @osty_rt_test_gen_int_range(",
		"declare double @osty_rt_test_gen_float(i64)",
		"call double @osty_rt_test_gen_float(",
		"declare ptr @osty_rt_test_gen_ascii_string(i64, i64)",
		"call ptr @osty_rt_test_gen_ascii_string(",
		"call void @osty_rt_list_push_i64(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
