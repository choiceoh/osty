package llvmgen

import (
	"strings"
	"testing"
)

func TestStdStringsCompareRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    let r = strings.compare("a", "b")
    println(r)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_strings_compare.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare i64 @osty_rt_strings_Compare(ptr, ptr)",
		"call i64 @osty_rt_strings_Compare",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdStringsHasPrefixRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    if strings.hasPrefix("osty", "ost") {
        println(1)
    } else {
        println(0)
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_strings_hasprefix.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare i1 @osty_rt_strings_HasPrefix(ptr, ptr)",
		"call i1 @osty_rt_strings_HasPrefix",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdStringsAliasRespectsRename(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as s

fn main() {
    let r = s.compare("a", "b")
    println(r)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_strings_alias.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	if got := string(ir); !strings.Contains(got, "call i64 @osty_rt_strings_Compare") {
		t.Fatalf("aliased std.strings.compare did not route to runtime:\n%s", got)
	}
}

func TestStdStringsJoinRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    let parts: List<String> = ["a", "b", "c"]
    let out = strings.join(parts, ", ")
    println(out)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_join.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare ptr @osty_rt_strings_Join(ptr, ptr)",
		"call ptr @osty_rt_strings_Join",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestBareNoneSomeInPtrBackedOptional(t *testing.T) {
	file := parseLLVMGenFile(t, `fn pickNone() -> String? {
    return None
}

fn pickSome() -> String? {
    return Some("x")
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/option_none_some.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, "ret ptr null") {
		t.Fatalf("expected `ret ptr null` for bare None:\n%s", got)
	}
	if !strings.Contains(got, "ret ptr @.str0") {
		t.Fatalf("expected `ret ptr @.str0` for Some(literal):\n%s", got)
	}
}

func TestCollectStdStringsAliasesIgnoresRuntimeFFI(t *testing.T) {
	file := parseLLVMGenFile(t, `use runtime.strings as strings {
    fn HasPrefix(s: String, prefix: String) -> Bool
}

fn main() {
    if strings.HasPrefix("a", "a") {
        println(1)
    }
}
`)
	aliases := collectStdStringsAliases(file)
	if len(aliases) != 0 {
		t.Fatalf("expected no std.strings aliases from runtime FFI use, got %v", aliases)
	}
}
