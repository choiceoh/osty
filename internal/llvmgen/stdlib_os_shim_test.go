package llvmgen

import (
	"strings"
	"testing"
)

func TestStdOsExecRoutesToRuntimeAndSupportsOutputFields(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.os as os

fn main() {
    match os.exec("sh", ["-c", "printf hi"]) {
        Ok(out) -> {
            println(out.stdout)
            println(out.stderr)
            println(out.exitCode)
        },
        Err(err) -> println(err.message()),
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_os_exec.osty",
	})
	if err != nil {
		t.Fatalf("generateFromAST: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%__osty_std_os_Output = type { i64, ptr, ptr }",
		"declare ptr @osty_rt_os_exec(ptr, ptr, i1)",
		"declare void @osty_rt_os_exec_result_free(ptr)",
		"call ptr @osty_rt_os_exec(ptr",
		"getelementptr inbounds { i64, i64, i1, ptr, ptr, ptr }, ptr",
		"load i64, ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
}

func TestStdOsExecWithRoutesOptionsAndTimedOutField(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.os as os

fn main() {
    let env: Map<String, String> = {:}
    match os.execWith("sh", ["-c", "printf hi"], "/tmp", env, 25) {
        Ok(out) -> {
            println(out.stdout)
            println(out.stderr)
            println(out.exitCode)
            println(out.timedOut)
        },
        Err(err) -> println(err.message()),
    }

    match os.execShellWith("printf shell", "/tmp", env, 0) {
        Ok(out) -> println(out.stdout),
        Err(err) -> println(err.message()),
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_os_exec_with.osty",
	})
	if err != nil {
		t.Fatalf("generateFromAST: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%__osty_std_os_ExecOutput = type { i64, ptr, ptr, i1 }",
		"declare ptr @osty_rt_os_exec_options(ptr, ptr, i1, ptr, ptr, i64)",
		"call ptr @osty_rt_os_exec_options(ptr",
		"getelementptr inbounds { i64, i64, i1, ptr, ptr, ptr }, ptr",
		"load i1, ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
}

func TestStdOsExecInputRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.os as os

fn main() {
    let env: Map<String, String> = {:}
    match os.execInputWith("sh", ["-c", "cat"], "hello", "/tmp", env, 0) {
        Ok(out) -> println(out.stdout),
        Err(err) -> println(err.message()),
    }

    match os.execShellInput("cat", "shell") {
        Ok(out) -> println(out.stdout),
        Err(err) -> println(err.message()),
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_os_exec_input.osty",
	})
	if err != nil {
		t.Fatalf("generateFromAST: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_os_exec_input_options(ptr, ptr, i1, ptr, ptr, i64, ptr)",
		"declare ptr @osty_rt_os_exec_input(ptr, ptr, i1, ptr)",
		"call ptr @osty_rt_os_exec_input_options(ptr",
		"call ptr @osty_rt_os_exec_input(ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
}

func TestStdOsPidHostnameAndExitRouteToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.os as os

fn main() {
    println(os.pid())
    match os.hostname() {
        Ok(host) -> println(host),
        Err(err) -> println(err.message()),
    }
}

fn stop() {
    os.exit(7)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_os_misc.osty",
	})
	if err != nil {
		t.Fatalf("generateFromAST: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare i64 @osty_rt_os_pid()",
		"call i64 @osty_rt_os_pid()",
		"declare ptr @osty_rt_os_hostname()",
		"declare void @osty_rt_os_string_result_free(ptr)",
		"call ptr @osty_rt_os_hostname()",
		"declare void @osty_rt_os_exit(i32)",
		"call void @osty_rt_os_exit(i32",
		"unreachable",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
}
