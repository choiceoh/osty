package backend

import (
	"context"
	"os/exec"
	"testing"
)

func TestLLVMBackendBinaryRunsStdMathAndFloatMethods(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.math as math

fn main() {
    println(math.sin(math.PI / 2.0).round().toInt())

    let n: Float32 = 3.75
    println(n.floor().toInt())

    println(math.log(100.0, 10.0).round().toInt())
    println((2.5).round().toInt())
    println((3.25).fract().toFixed(2))
    println(math.NAN.isNaN())
    println(math.INFINITY.isInfinite())
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "1\n3\n2\n2\n0.25\ntrue\ntrue\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}
