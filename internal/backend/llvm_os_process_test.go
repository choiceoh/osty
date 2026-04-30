package backend

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

func TestLLVMBackendBinaryRunsStdOsExecWithSplitArgs(t *testing.T) {
	parallelClangBackendTest(t)

	prog, args, _ := stdOsProcessCommands()
	src := fmt.Sprintf(`use std.os
use std.strings

fn main() {
    let args = strings.split(%q, "\n")
    match os.exec(%q, args) {
        Ok(out) -> {
            println(out.exitCode == 3)
            println(out.stdout == "direct-out")
            println(out.stderr.contains("direct-err"))
        },
        Err(err) -> {
            println(false)
            println(err.message())
            println(false)
        },
    }
}
`, strings.Join(args, "\n"), prog)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, src)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	irBytes, readErr := os.ReadFile(result.Artifacts.LLVMIR)
	if readErr != nil {
		t.Fatalf("ReadFile(%q): %v", result.Artifacts.LLVMIR, readErr)
	}
	if !strings.Contains(string(irBytes), "osty LLVM MIR backend") {
		t.Fatalf("std.os split-arg test did not exercise MIR backend:\n%s", irBytes)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "true\ntrue\ntrue\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func stdCmdEnvCwdCommand() (program string, args []string, timeoutShell string) {
	if runtime.GOOS == "windows" {
		return "cmd",
			[]string{"/d", "/s", "/c", "<nul set /p =%OSTY_CMD_TEST%:%CD%"},
			"ping -n 3 127.0.0.1 >NUL"
	}
	return "sh",
		[]string{"-c", "printf \"$OSTY_CMD_TEST:$(basename \"$PWD\")\""},
		"sleep 2"
}

func TestLLVMBackendBinaryRunsStdCmdBuilderSurface(t *testing.T) {
	parallelClangBackendTest(t)

	tmp := t.TempDir()
	prog, args, timeoutShell := stdCmdEnvCwdCommand()
	base := filepath.Base(tmp)
	pipelineBlock := ""
	want := "true\ntrue\ntrue\ntrue\ntrue\ntrue\n"
	if runtime.GOOS != "windows" {
		pipelineBlock = `
    match cmd.pipe(
        cmd.command("printf").arg("hello\nworld\n"),
        cmd.command("grep").arg("world"),
    ).run() {
        Ok(out) -> {
            println(out.exitCode == 0)
            println(out.stdout.contains("world"))
        },
        Err(err) -> {
            println(false)
            println(err.message())
        },
    }
`
		want += "true\ntrue\n"
	}
	src := fmt.Sprintf(`use std.cmd

fn main() {
    match cmd.command(%q).withArgs(%s).withCwd(%q).withEnv("OSTY_CMD_TEST", "env-ok").run() {
        Ok(out) -> {
            println(out.exitCode == 0)
            println(out.stdout.contains("env-ok"))
            println(out.stdout.contains(%q))
            println(!out.timedOut)
        },
        Err(err) -> {
            println(false)
            println(err.message())
            println(false)
            println(false)
        },
    }

    match cmd.shell(%q).withTimeoutMillis(100).run() {
        Ok(out) -> {
            println(out.timedOut)
            println(out.exitCode != 0)
        },
        Err(err) -> {
            println(false)
            println(err.message())
        },
    }
%s}
`, prog, ostyStringListLiteral(args), tmp, base, timeoutShell, pipelineBlock)

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
	if got := string(output); got != want {
		t.Fatalf("binary stdout = %q, want %q\nsource:\n%s", got, want, src)
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
