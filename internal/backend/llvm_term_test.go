package backend

import (
	"context"
	"os/exec"
	"testing"
)

func TestLLVMBackendBinaryRunsStdTermBasicSurface(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.term

fn main() {
    println(term.isTerminal() == term.isTerminal())
    match term.size() {
        Ok(size) -> {
            println(size.width > 0)
            println(size.height > 0)
        },
        Err(err) -> {
            println(false)
            println(err.message())
        },
    }
    match term.write("hello") {
        Ok(_) -> {},
        Err(err) -> println(err.message()),
    }
    match term.flush() {
        Ok(_) -> println("-flushed"),
        Err(err) -> println(err.message()),
    }
    match term.setRawMode(false) {
        Ok(_) -> println("raw-off"),
        Err(err) -> println(err.message()),
    }
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
	if got, want := string(output), "true\ntrue\ntrue\nhello-flushed\nraw-off\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}
