package llvmgen

import (
	"strings"
	"testing"
)

func TestStdMathShimRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.math as math

fn main() {
    let bits = 1.5.toFloat32().toBits()
    let value = math.sin(math.PI / 2.0).round().toIntTrunc()
    let _ = bits
    let _ = value
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_math_runtime.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare double @osty_rt_float_sin(double)",
		"call double @osty_rt_float_sin",
		"declare ptr @osty_rt_float_to_int_trunc(double, ptr)",
		"call ptr @osty_rt_float_to_int_trunc",
		"declare i64 @osty_rt_float32_to_bits(float)",
		"call i64 @osty_rt_float32_to_bits",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdMathShimCoversExecutableMathSurface(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.math as math

fn main() {
    let a = math.sin(math.PI / 2.0).round().toInt()

    let n: Float32 = 3.75
    let b = n.floor().toInt()

    let c = math.log(100.0, 10.0).round().toInt()
    let d = (2.5).round().toInt()
    let e = (3.25).fract().toFixed(2)
    let f = math.NAN.isNaN()
    let g = math.INFINITY.isInfinite()
    let _ = a
    let _ = b
    let _ = c
    let _ = d
    let _ = e
    let _ = f
    let _ = g
}
`)

	if _, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_math_exec_surface.osty",
	}); err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
}
