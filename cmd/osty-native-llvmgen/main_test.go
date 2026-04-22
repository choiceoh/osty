package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

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

func TestRunReportsNotCoveredForUnsupportedSource(t *testing.T) {
	var stdout bytes.Buffer
	stdin := strings.NewReader(`{"path":"main.osty","source":"struct Pair { left: Int, right: Int }\nfn main() { let xs = [Pair { left: 1, right: 2 }] println(xs[0].left) }\n"}`)
	if err := run(stdin, &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp llvmgenResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Covered {
		t.Fatalf("covered = true, want false\nllvmIr:\n%s", resp.LLVMIR)
	}
	if resp.LLVMIR != "" {
		t.Fatalf("llvmIr = %q, want empty output for uncovered source", resp.LLVMIR)
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

func TestRunRejectsInvalidJSON(t *testing.T) {
	var stdout bytes.Buffer
	err := run(strings.NewReader("{"), &stdout)
	if err == nil || !strings.Contains(err.Error(), "decode llvmgen request") {
		t.Fatalf("run error = %v, want decode error", err)
	}
}
