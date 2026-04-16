package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/parser"
)

func parseLLVMGenFile(t *testing.T, src string) *ast.File {
	t.Helper()

	file, diags := parser.ParseDiagnostics([]byte(src))
	if len(diags) != 0 {
		t.Fatalf("ParseDiagnostics returned %d diagnostics: %v", len(diags), diags)
	}
	if file == nil {
		t.Fatal("ParseDiagnostics returned nil file")
	}
	return file
}

func TestGenerateRuntimeFFIUsesRuntimeABI(t *testing.T) {
	file := parseLLVMGenFile(t, `use runtime.strings as strings {
    fn HasPrefix(s: String, prefix: String) -> Bool
    fn Split(s: String, sep: String) -> List<String>
}

fn main() {
    let parts = strings.Split("osty,llvm", ",")
    if strings.HasPrefix("osty", "ost") {
        println(1)
    } else {
        println(0)
    }
}
`)

	ir, err := Generate(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/runtime_has_prefix.osty",
		Target:      "x86_64-unknown-linux-gnu",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, forbidden := range []string{
		"LLVM002",
		"Osty LLVM backend skeleton",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("generated IR still looks unsupported; found %q in:\n%s", forbidden, got)
		}
	}
	for _, want := range []string{
		"declare i1 @osty_rt_strings_HasPrefix(ptr, ptr)",
		"declare ptr @osty_rt_strings_Split(ptr, ptr)",
		"call ptr @osty_rt_strings_Split",
		"call i1 @osty_rt_strings_HasPrefix",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateUnknownRuntimeFFIStillReportsLLVM002(t *testing.T) {
	file := parseLLVMGenFile(t, `use runtime.unknown as strings {
    fn HasPrefix(s: String, prefix: String) -> Bool
}

fn main() {
    if strings.HasPrefix("osty", "ost") {
        println(1)
    } else {
        println(0)
    }
}
`)

	ir, err := Generate(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/runtime_unknown.osty",
		Target:      "x86_64-unknown-linux-gnu",
	})
	if err == nil {
		t.Fatalf("Generate succeeded unexpectedly; got IR:\n%s", string(ir))
	}

	diag := UnsupportedDiagnosticForError(err)
	if got, want := diag.Code, "LLVM002"; got != want {
		t.Fatalf("diag.Code = %q, want %q", got, want)
	}
	if got, want := diag.Kind, "runtime-ffi"; got != want {
		t.Fatalf("diag.Kind = %q, want %q", got, want)
	}
	if got := diag.Message; !strings.Contains(got, "runtime.unknown") {
		t.Fatalf("diag.Message = %q, want it to mention runtime.unknown", got)
	}
}

func TestGenerateStringCompareUsesRuntimeABI(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    if "osty" != "llvm" {
        println(1)
    } else {
        println(0)
    }
}
`)

	ir, err := Generate(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/string_compare.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare i1 @osty_rt_strings_Equal(ptr, ptr)",
		"call i1 @osty_rt_strings_Equal",
		"xor i1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateLibraryModuleWithoutMain(t *testing.T) {
	file := parseLLVMGenFile(t, `enum Kind {
    Manifest,
    Lockfile,
    Source,
    Support,
    Ignored,
}

pub fn kindName(kind: Kind) -> String {
    match kind {
        Manifest -> "manifest",
        Lockfile -> "lockfile",
        Source -> "source",
        Support -> "support",
        Ignored -> "ignored",
    }
}
`)

	ir, err := Generate(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/package_entry.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define ptr @kindName(i64 %kind)",
		"select i1",
		"manifest",
		"ignored",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateOpaqueRuntimeObjectTypes(t *testing.T) {
	file := parseLLVMGenFile(t, `pub struct RuntimeBag {
    items: List<String>
    callback: fn(String) -> Bool
    maybe: String?
}

pub fn keep(bag: RuntimeBag) -> RuntimeBag {
    bag
}
`)

	ir, err := Generate(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/runtime_bag.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%RuntimeBag = type { ptr, ptr, ptr }",
		"define %RuntimeBag @keep(%RuntimeBag %bag)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateVoidFunctionAndCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn touch(value: Int) {
    println(value)
}

fn main() {
    touch(42)
}
`)

	ir, err := Generate(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/void_call.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"define void @touch(i64 %value)",
		"call void @touch(i64 42)",
		"ret void",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateOpaqueListLiteralsUseRuntimeABI(t *testing.T) {
	file := parseLLVMGenFile(t, `fn empty() -> List<Int> {
    let empty: List<Int> = []
    empty
}

fn values() -> List<Int> {
    let values: List<Int> = [1, 2]
    values
}
`)

	ir, err := Generate(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/list_literals.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"@osty_rt_list_",
		"call ptr @osty_rt_list_",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	if gotCount := strings.Count(got, "@osty_rt_list_"); gotCount < 2 {
		t.Fatalf("generated IR list runtime prefix count = %d, want at least 2:\n%s", gotCount, got)
	}
}

func TestGenerateListLenUsesRuntimeABI(t *testing.T) {
	file := parseLLVMGenFile(t, `fn size(items: List<Int>) -> Int {
    items.len()
}
`)

	ir, err := Generate(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/list_len.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"@osty_rt_list_",
		"call i64 @osty_rt_list_",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateListPushStmtUsesRuntimeABI(t *testing.T) {
	file := parseLLVMGenFile(t, `fn append(mut values: List<Int>) {
    values.push(1)
    values.push(2)
}
`)

	ir, err := Generate(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/list_push.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"@osty_rt_list_",
		"call void @osty_rt_list_",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	if gotCount := strings.Count(got, "call void @osty_rt_list_"); gotCount < 2 {
		t.Fatalf("generated IR push call count = %d, want at least 2:\n%s", gotCount, got)
	}
}

func TestGenerateManagedListPushStmtUsesSafepoint(t *testing.T) {
	file := parseLLVMGenFile(t, `fn append(mut values: List<String>, item: String) {
    values.push(item)
}
`)

	ir, err := Generate(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/list_push_managed.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare void @osty.gc.safepoint_v1(i64, ptr, i64)",
		"call void @osty.gc.safepoint_v1(",
		"call void @osty_rt_list_push_ptr(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateForInOverListStringUsesRuntimeABI(t *testing.T) {
	file := parseLLVMGenFile(t, `fn visit(items: List<String>) {
    for item in items {
        println(item)
    }
}
`)

	ir, err := Generate(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/list_for_in.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"@osty_rt_list_",
		"br i1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
