package llvmgen

import (
	"strings"
	"testing"
)

func TestGenerateStringLenMethodDispatch(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let n = "hello".len()
    println(n)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/string_len_method.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare i64 @osty_rt_strings_ByteLen(ptr)",
		"call i64 @osty_rt_strings_ByteLen",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateNestedFieldListLenMethodDispatch(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Version {
    build: List<String>
}

fn main() {
    let v = Version { build: ["meta", "data"] }
    println(v.build.len())
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/nested_field_len.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%Version = type { ptr }",
		"call i64 @osty_rt_list_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateStringCharsNoLongerTripsLLVM015(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let chars = "abc".chars()
    println(chars.len())
}
`)

	_, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/string_chars_dispatch.osty",
	})
	if err == nil {
		t.Fatal("Generate succeeded unexpectedly; wanted the next non-LLVM015 limitation")
	}
	if strings.Contains(err.Error(), "LLVM015") {
		t.Fatalf("String.chars still failed in FieldExpr call dispatch: %v", err)
	}
}
