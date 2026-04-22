package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func runLLVMGenSource(t *testing.T, path string, source string) llvmgenResponse {
	t.Helper()
	reqBody, err := json.Marshal(llvmgenRequest{Path: path, Source: source})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var stdout bytes.Buffer
	if err := run(bytes.NewReader(reqBody), &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp llvmgenResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func TestRunEmitsNativeOwnedLLVMIRForSource(t *testing.T) {
	var stdout bytes.Buffer
	stdin := strings.NewReader(`{"path":"main.osty","source":"fn pick(flag: Bool) -> Int { if flag { 42 } else { 0 } }\nfn main() { let mut i = 0 let mut sum = 0 for i < 3 { sum = sum + pick(i == 2) i = i + 1 } println(sum) }\n"}`)
	if err := run(stdin, &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp llvmgenResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Covered {
		t.Fatalf("covered = false, want true")
	}
	if !strings.Contains(resp.LLVMIR, "define i64 @pick(i1 %flag)") {
		t.Fatalf("llvmIr missing pick definition:\n%s", resp.LLVMIR)
	}
	if !strings.Contains(resp.LLVMIR, "for.cond") {
		t.Fatalf("llvmIr missing loop label:\n%s", resp.LLVMIR)
	}
}

func TestRunEmitsNativeOwnedLLVMIRForPackage(t *testing.T) {
	reqBody, err := json.Marshal(llvmgenRequest{
		Path: "b.osty",
		Package: &llvmgenPackageInput{
			Files: []llvmgenPackageFile{
				{Name: "a.osty", Source: "pub fn helper() -> Int { 1 }\n"},
				{Name: "b.osty", Source: "fn main() { println(helper()) }\n"},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	var stdout bytes.Buffer
	if err := run(bytes.NewReader(reqBody), &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp llvmgenResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Covered {
		t.Fatalf("covered = false, want true")
	}
	if !strings.Contains(resp.LLVMIR, "@helper") {
		t.Fatalf("llvmIr missing helper symbol:\n%s", resp.LLVMIR)
	}
	if !strings.Contains(resp.LLVMIR, "@main") {
		t.Fatalf("llvmIr missing main symbol:\n%s", resp.LLVMIR)
	}
}

func TestRunEmitsNativeOwnedLLVMIRForStructFieldAssign(t *testing.T) {
	var stdout bytes.Buffer
	stdin := strings.NewReader(`{"path":"main.osty","source":"struct Pair { left: Int, right: Int }\nfn main() { let mut pair = Pair { left: 1, right: 2 } pair.left = 3 println(pair.left) }\n"}`)
	if err := run(stdin, &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp llvmgenResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Covered {
		t.Fatalf("covered = false, want true")
	}
	for _, want := range []string{
		"%Pair = type { i64, i64 }",
		"extractvalue %Pair",
		"insertvalue %Pair",
	} {
		if !strings.Contains(resp.LLVMIR, want) {
			t.Fatalf("llvmIr missing %q:\n%s", want, resp.LLVMIR)
		}
	}
}

func TestRunEmitsNativeOwnedLLVMIRForListIndex(t *testing.T) {
	var stdout bytes.Buffer
	stdin := strings.NewReader(`{"path":"main.osty","source":"fn main() { let xs = [1, 2] println(xs[0]) }\n"}`)
	if err := run(stdin, &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp llvmgenResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Covered {
		t.Fatalf("covered = false, want true")
	}
	for _, want := range []string{
		"declare ptr @osty_rt_list_new()",
		"call ptr @osty_rt_list_new()",
		"call void @osty_rt_list_push_i64(",
		"call i64 @osty_rt_list_get_i64(",
	} {
		if !strings.Contains(resp.LLVMIR, want) {
			t.Fatalf("llvmIr missing %q:\n%s", want, resp.LLVMIR)
		}
	}
}

func TestRunReportsCoveredForOptionalCoalesceSource(t *testing.T) {
	var stdout bytes.Buffer
	stdin := strings.NewReader(`{"path":"main.osty","source":"fn resolve(name: String?) -> String {\n    name ?? \"anonymous\"\n}\n\nfn main() {}\n"}`)
	if err := run(stdin, &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp llvmgenResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Covered {
		t.Fatalf("covered = false, want true")
	}
	for _, want := range []string{
		"define ptr @resolve(ptr %name)",
		"coalesce.some",
		"coalesce.none",
		"phi ptr",
	} {
		if !strings.Contains(resp.LLVMIR, want) {
			t.Fatalf("llvmIr missing %q:\n%s", want, resp.LLVMIR)
		}
	}
}

func TestRunUsesNativeOwnedExportAndCABIShape(t *testing.T) {
	var stdout bytes.Buffer
	stdin := strings.NewReader(`{"path":"main.osty","source":"#[export(\"osty.gc.native_entry_v1\")]\n#[c_abi]\npub fn native_entry_v1() -> Int { 0 }\n"}`)
	if err := run(stdin, &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp llvmgenResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Covered {
		t.Fatalf("covered = false, want true")
	}
	if !strings.Contains(resp.LLVMIR, "define ccc i64 @native_entry_v1()") {
		t.Fatalf("llvmIr missing C ABI function definition:\n%s", resp.LLVMIR)
	}
	if !strings.Contains(resp.LLVMIR, "@osty.gc.native_entry_v1 = dso_local alias ptr, ptr @native_entry_v1") {
		t.Fatalf("llvmIr missing export alias:\n%s", resp.LLVMIR)
	}
}

func TestRunCoversRuntimeStringsSplitAndListToSet(t *testing.T) {
	resp := runLLVMGenSource(t, "main.osty", `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

fn main() {
    let items = strings.Split("pear,apple", ",")
    let seen = items.toSet()
    println(seen.contains("pear"))
}
`)
	if !resp.Covered {
		t.Fatalf("covered = false, want true")
	}
	for _, want := range []string{
		"declare ptr @osty_rt_strings_Split(ptr, ptr)",
		"call ptr @osty_rt_strings_Split(",
		"declare ptr @osty_rt_list_to_set_string(ptr)",
		"call i1 @osty_rt_set_contains_string(",
	} {
		if !strings.Contains(resp.LLVMIR, want) {
			t.Fatalf("llvmIr missing %q:\n%s", want, resp.LLVMIR)
		}
	}
}

func TestRunDefersStdTestingHelpersToMIRBackend(t *testing.T) {
	resp := runLLVMGenSource(t, "main.osty", `use std.testing

enum CalcError {
    DivideByZero,
}

fn div(a: Int, b: Int) -> Result<Int, CalcError> {
    if b == 0 { Err(DivideByZero) } else { Ok(a / b) }
}

fn main() {
    let q = testing.expectOk(div(10, 2))
    testing.assertEq(q, 5)
    testing.expectError(div(1, 0))
}
`)
	if resp.Covered {
		t.Fatalf("covered = true, want false (std.testing should route to MIR backend)")
	}
}

func TestRunCoversNestedStructBindingPattern(t *testing.T) {
	resp := runLLVMGenSource(t, "main.osty", `struct Inner {
    x: Int
}

struct Outer {
    inner: Inner
}

fn main() {
    let outer @ Outer { inner: Inner { x } } = Outer { inner: Inner { x: 7 } }
    println(x)
    println(outer.inner.x)
}
`)
	if !resp.Covered {
		t.Fatalf("covered = false, want true")
	}
	for _, want := range []string{
		"%Inner = type { i64 }",
		"%Outer = type { %Inner }",
		"extractvalue %Outer",
		"extractvalue %Inner",
	} {
		if !strings.Contains(resp.LLVMIR, want) {
			t.Fatalf("llvmIr missing %q:\n%s", want, resp.LLVMIR)
		}
	}
}

func TestRunRejectsInvalidJSON(t *testing.T) {
	var stdout bytes.Buffer
	err := run(strings.NewReader("{"), &stdout)
	if err == nil || !strings.Contains(err.Error(), "decode llvmgen request") {
		t.Fatalf("run error = %v, want decode error", err)
	}
}
