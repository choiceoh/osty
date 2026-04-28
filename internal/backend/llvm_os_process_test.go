package backend

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"testing"
)

func stdOsProcessCommands() (directProgram string, directArgs []string, shellCommand string) {
	if runtime.GOOS == "windows" {
		return "cmd",
			[]string{"/d", "/s", "/c", "(echo|set /p =direct-out) & (echo direct-err 1>&2) & exit /b 3"},
			"(echo|set /p =shell-out) & (echo shell-err 1>&2)"
	}
	return "sh",
		[]string{"-c", "printf direct-out && printf direct-err 1>&2 && exit 3"},
		"printf shell-out && printf shell-err 1>&2"
}

func ostyStringListLiteral(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	out := "["
	for i, value := range values {
		if i != 0 {
			out += ", "
		}
		out += fmt.Sprintf("%q", value)
	}
	out += "]"
	return out
}

func TestLLVMBackendBinaryRunsStdOsProcessSurface(t *testing.T) {
	parallelClangBackendTest(t)

	prog, args, shellCmd := stdOsProcessCommands()
	src := fmt.Sprintf(`use std.os

fn main() {
    match os.exec(%q, %s) {
        Ok(direct) -> {
            println(direct.exitCode == 3)
            println(direct.stdout == "direct-out")
            println(direct.stderr.contains("direct-err"))
        },
        Err(err) -> {
            println(false)
            println(err.message())
            println(false)
        },
    }

    match os.execShell(%q) {
        Ok(shell) -> {
            println(shell.exitCode == 0)
            println(shell.stdout == "shell-out")
            println(shell.stderr.contains("shell-err"))
        },
        Err(err) -> {
            println(false)
            println(err.message())
            println(false)
        },
    }

    println(os.pid() > 0)
    match os.hostname() {
        Ok(host) -> println(host.len() > 0),
        Err(err) -> println(err.message()),
    }
}
`, prog, ostyStringListLiteral(args), shellCmd)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, src)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "true\ntrue\ntrue\ntrue\ntrue\ntrue\ntrue\ntrue\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryStdOsExitUsesRequestedCode(t *testing.T) {
	parallelClangBackendTest(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.os

fn main() {
    os.exit(7)
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected %q to exit non-zero, got success with output %q", result.Artifacts.Binary, output)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("run error type = %T, want *exec.ExitError (%v)", err, err)
	}
	if exitErr.ExitCode() != 7 {
		t.Fatalf("exit code = %d, want 7; output=%q", exitErr.ExitCode(), output)
	}
}
