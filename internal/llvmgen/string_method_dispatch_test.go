package llvmgen

import (
	"strings"
	"testing"
)

// String method-call dispatch — `s.trimPrefix(p)`, `s.trimSuffix(p)`,
// `s.contains(n)`, `s.endsWith(p)` — must lower to the same runtime
// helpers that the free-function `strings.*` forms already use. Prior
// to this commit the toolchain's `trimmed.trimPrefix("runtime.")` call
// in llvmgen.osty tripped LLVM015 because only `startsWith` was
// dispatched; the rest fell through to the "not a known fn" wall.
func TestStringTrimPrefixMethodCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn drop(prefix: String, s: String) -> String {
    s.trimPrefix(prefix)
}

fn main() {
    println(drop("runtime.", "runtime.strings"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_trim_prefix_method.osty"})
	if err != nil {
		t.Fatalf("trimPrefix method call errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_TrimPrefix",
		"declare ptr @osty_rt_strings_TrimPrefix(ptr, ptr)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("trimPrefix method call missing %q:\n%s", want, got)
		}
	}
}

func TestStringTrimSuffixMethodCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn drop(suffix: String, s: String) -> String {
    s.trimSuffix(suffix)
}

fn main() {
    println(drop(".osty", "lexer.osty"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_trim_suffix_method.osty"})
	if err != nil {
		t.Fatalf("trimSuffix method call errored: %v", err)
	}
	if got := string(ir); !strings.Contains(got, "@osty_rt_strings_TrimSuffix") {
		t.Fatalf("trimSuffix method call did not invoke runtime helper:\n%s", got)
	}
}

func TestStringContainsMethodCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn has(s: String, n: String) -> Bool {
    s.contains(n)
}

fn main() {
    println(has("hello, world", "world"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_contains_method.osty"})
	if err != nil {
		t.Fatalf("contains method call errored: %v", err)
	}
	if got := string(ir); !strings.Contains(got, "@osty_rt_strings_Contains") {
		t.Fatalf("contains method call did not invoke runtime helper:\n%s", got)
	}
}

func TestStringEndsWithMethodCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn endsTest(s: String, suffix: String) -> Bool {
    s.endsWith(suffix)
}

fn main() {
    println(endsTest("foo.osty", ".osty"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_ends_with_method.osty"})
	if err != nil {
		t.Fatalf("endsWith method call errored: %v", err)
	}
	if got := string(ir); !strings.Contains(got, "@osty_rt_strings_HasSuffix") {
		t.Fatalf("endsWith method call did not invoke HasSuffix runtime helper:\n%s", got)
	}
}

// Chained method calls — `s.trimPrefix(a).trimSuffix(b).contains(n)` —
// must keep flowing through the String dispatcher rather than dropping
// the source-type hint after the first call. This locks in the
// `sourceType: String` propagation on trimPrefix / trimSuffix outputs.
func TestStringMethodChain(t *testing.T) {
	file := parseLLVMGenFile(t, `fn pipeline(s: String) -> Bool {
    s.trimPrefix("[").trimSuffix("]").contains(",")
}

fn main() {
    println(pipeline("[a,b]"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_method_chain.osty"})
	if err != nil {
		t.Fatalf("chained string methods errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_TrimPrefix",
		"@osty_rt_strings_TrimSuffix",
		"@osty_rt_strings_Contains",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("chain missing %q:\n%s", want, got)
		}
	}
}
