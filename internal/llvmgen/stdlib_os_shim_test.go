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
		"getelementptr inbounds { i64, i64, ptr, ptr, ptr }, ptr",
		"load i64, ptr",
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
