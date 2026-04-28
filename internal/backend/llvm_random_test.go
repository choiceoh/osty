package backend

import (
	"context"
	"os/exec"
	"testing"
)

func TestLLVMBackendBinaryRunsStdRandom(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.random

fn main() {
    let a = random.seeded(42)
    let b = random.seeded(42)
    println(a.int(-1000, 1000) == b.int(-1000, 1000))
    println(a.intInclusive(-1000, 1000) == b.intInclusive(-1000, 1000))

    let wide = a.int((-9223372036854775807) - 1, 9223372036854775807)
    println(wide >= ((-9223372036854775807) - 1) && wide < 9223372036854775807)

    let inclusive = a.intInclusive((-9223372036854775807) - 1, 9223372036854775807)
    println(inclusive >= ((-9223372036854775807) - 1) && inclusive <= 9223372036854775807)

    let f = a.float()
    println(f >= 0.0 && f < 1.0)

    let d = random.default()
    println(d.bytes(8).len() == 8)

    let blob = a.bytes(16)
    println(blob.len() == 16)

    println(a.choice([]).isNone())
    match a.choice([10, 20, 30, 40]) {
        Some(v) -> println(v == 10 || v == 20 || v == 30 || v == 40),
        None -> println(false),
    }

    let xs = [1, 2, 3, 4, 5, 6]
    let ys = [1, 2, 3, 4, 5, 6]
    let s1 = random.seeded(7)
    let s2 = random.seeded(7)
    s1.shuffle(xs)
    s2.shuffle(ys)
    println(xs[0] == ys[0] && xs[1] == ys[1] && xs[2] == ys[2] && xs[3] == ys[3] && xs[4] == ys[4] && xs[5] == ys[5])
    println(xs[0] == 5 && xs[1] == 2 && xs[2] == 1 && xs[3] == 6 && xs[4] == 4 && xs[5] == 3)
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
	if got, want := string(output), "true\ntrue\ntrue\ntrue\ntrue\ntrue\ntrue\ntrue\ntrue\ntrue\ntrue\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}
