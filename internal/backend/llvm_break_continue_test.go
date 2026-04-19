package backend

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

func TestLLVMBackendBinaryRunsBreakAndContinueLoops(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let mut i = 0
    for i < 6 {
        i = i + 1
        if i == 2 {
            continue
        }
        if i == 5 {
            break
        }
        println(i)
    }

    for j in 0..6 {
        if j == 1 {
            continue
        }
        if j == 4 {
            break
        }
        println(j)
    }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "1\n3\n4\n0\n2\n3\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryManagedListLoopBreakAndContinueSurvivePressure(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

fn main() {
    for item in strings.Split("a,b,c", ",") {
        if item == "a" {
            continue
        }
        println(item)
        break
    }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	cmd.Env = append(os.Environ(), "OSTY_GC_THRESHOLD_BYTES=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "b\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}
