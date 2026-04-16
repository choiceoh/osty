package llvmgen

import (
	"strings"
	"testing"
)

func TestGenerateTopLevelTypeAliasAndLet(t *testing.T) {
	file := parseLLVMGenFile(t, `type Count = Int

pub let MAX_USERS: Count = 10000

fn limit() -> Count {
    MAX_USERS
}

fn main() {
    println(limit())
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/top_level_alias_let.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"@osty_global_MAX_USERS = internal constant i64 10000",
		"define i64 @limit()",
		"load i64, ptr @osty_global_MAX_USERS",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateTopLevelInterfaceAndInterfaceAliasLet(t *testing.T) {
	file := parseLLVMGenFile(t, `pub interface Error {
    fn message(self) -> String
}

type AppError = Error

pub let DEFAULT_ERROR: AppError = "broken"

fn main() {
    println(DEFAULT_ERROR)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/top_level_interface_let.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"@osty_global_DEFAULT_ERROR = internal constant ptr @.str0",
		"load ptr, ptr @osty_global_DEFAULT_ERROR",
		"call i32 (ptr, ...) @printf(ptr @.fmt_str, ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateTopLevelMutableLetUsesGlobalStorage(t *testing.T) {
	file := parseLLVMGenFile(t, `pub let mut current: Int = 1

fn bump() -> Int {
    current = current + 1
    current
}

fn main() {
    println(bump())
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/top_level_mut_let.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"@osty_global_current = internal global i64 1",
		"load i64, ptr @osty_global_current",
		"store i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
