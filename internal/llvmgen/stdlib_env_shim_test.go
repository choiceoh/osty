package llvmgen

import (
	"strings"
	"testing"
)

// `use std.env` + `env.args()` must widen `main` to (argc, argv), emit
// the init prologue, and lower the call to the runtime helper. Losing
// any one silently hands the program an empty argv at runtime.
func TestStdEnvArgsRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.env

fn main() {
    let xs = env.args()
    println(xs.len())
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_env_args.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_env_args()",
		"declare void @osty_rt_env_args_init(i64, ptr)",
		"call ptr @osty_rt_env_args()",
		"define i32 @main(i32 %osty_env_argc, ptr %osty_env_argv)",
		"call void @osty_rt_env_args_init(i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// Packages without `std.env` must keep the bare `i32 @main()` so
// existing smoke-test IR snapshots and downstream linker expectations
// don't break on an unrelated param widening.
func TestMainSignatureStableWithoutStdEnv(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    println(1)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/no_env_main.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	if !strings.Contains(got, "define i32 @main()") {
		t.Fatalf("expected bare `define i32 @main()` without std.env import; got:\n%s", got)
	}
	if strings.Contains(got, "osty_rt_env_args_init") {
		t.Fatalf("unexpected env init prologue in main without std.env:\n%s", got)
	}
}

// `env.get(name)` should lower to the runtime helper and preserve the
// Optional<String> source type so `??` keeps compiling on top.
func TestStdEnvGetRoutesToRuntimeAndKeepsOptionalSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.env as osenv

fn main() {
    let home = osenv.get("HOME") ?? "fallback"
    println(home)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_env_get.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_env_get(ptr)",
		"call ptr @osty_rt_env_get(ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// `env.vars()` should lower to the runtime helper and preserve the
// Map<String, String> source type so map methods keep compiling.
func TestStdEnvVarsRoutesToRuntimeAndKeepsMapSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.env as osenv

fn main() {
    let home = osenv.vars().get("HOME") ?? "fallback"
    println(home)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_env_vars.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_env_vars()",
		"call ptr @osty_rt_env_vars()",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// `env.require(name)` should lower through env.get, materialize a
// Result<String, Error>, and keep ptr-backed `Error.message()` usable
// on the Err arm without needing std.error type injection.
func TestStdEnvRequireRoutesToRuntimeAndKeepsErrorMessageFallback(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.env as osenv

fn main() {
    match osenv.require("HOME") {
        Ok(home) -> println(home),
        Err(err) -> println(err.message()),
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_env_require.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_env_get(ptr)",
		"call ptr @osty_rt_env_get(ptr",
		"declare ptr @osty_rt_strings_Concat(ptr, ptr)",
		"call ptr @osty_rt_strings_Concat(ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
