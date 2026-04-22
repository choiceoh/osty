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
	src := `use std.testing as testing
use std.testing.gen as gen

fn main() {
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
        "choices",
        gen.oneOf(["aa", "bbb", "cccc"]),
        |s: String| s == "aa" || s == "bbb" || s == "cccc",
    )
}
`
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
		"declare ptr @osty_rt_test_gen_ascii_string(i64, i64)",
		"call ptr @osty_rt_test_gen_ascii_string(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateModuleTestingPropertyGeneratorSubset(t *testing.T) {
	src := `use std.testing as testing
use std.testing.gen as gen

fn main() {
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
        "choices",
        gen.oneOf(["aa", "bbb", "cccc"]),
        |s: String| s == "aa" || s == "bbb" || s == "cccc",
    )
}
`
	file := parseLLVMGenFile(t, src)
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.File(file, res, check.Opts{
		UseGolegacy:   true,
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
		"declare ptr @osty_rt_test_gen_ascii_string(i64, i64)",
		"call ptr @osty_rt_test_gen_ascii_string(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
