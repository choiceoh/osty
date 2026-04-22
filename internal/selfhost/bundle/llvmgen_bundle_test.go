package bundle

import (
	"strings"
	"testing"
)

func TestToolchainLLVMGenBundleIncludesToolchainLLVMGen(t *testing.T) {
	files := ToolchainLLVMGenFiles()
	if !contains(files, "toolchain/llvmgen.osty") {
		t.Fatalf("llvmgen bundle missing toolchain llvmgen source: %#v", files)
	}
}

func TestMergeToolchainLLVMGenNormalizesStringsUsage(t *testing.T) {
	root := t.TempDir()
	writeBundleFile(t, root, "toolchain/llvmgen.osty", `use std.strings as llvmStrings

fn demo(target: String, path: String, trimmed: String) -> String {
    if target.contains("x") {
        return llvmStrings.join(llvmStrings.split(path, "/"), llvmStrings.trimPrefix(trimmed, "x"))
    }
    path
}
`)

	merged, err := MergeToolchainLLVMGen(root)
	if err != nil {
		t.Fatalf("MergeToolchainLLVMGen() error = %v", err)
	}
	got := string(merged)
	if !strings.HasPrefix(got, llvmgenStringsPrelude+"\n") {
		t.Fatalf("merged source missing llvmgen strings prelude:\n%s", got)
	}
	if strings.Contains(got, "use std.strings as llvmStrings") {
		t.Fatalf("merged source kept per-file std.strings import:\n%s", got)
	}
	for _, unwanted := range []string{
		"llvmStrings.join(",
		"llvmStrings.split(",
		"llvmStrings.trimPrefix(",
		"target.contains(",
		"path.startsWith(",
		"trimmed.startsWith(",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("merged source kept unnormalized llvmgen helper call %q:\n%s", unwanted, got)
		}
	}
	if !strings.Contains(got, "llvmStrings.Join(") {
		t.Fatalf("merged source missing PascalCase Join rewrite:\n%s", got)
	}
	if !strings.Contains(got, "llvmStrings.Split(") {
		t.Fatalf("merged source missing PascalCase Split rewrite:\n%s", got)
	}
	if !strings.Contains(got, "llvmStrings.TrimPrefix(") {
		t.Fatalf("merged source missing PascalCase TrimPrefix rewrite:\n%s", got)
	}
	if !strings.Contains(got, "ostyStringsContains(target, ") {
		t.Fatalf("merged source missing contains shim rewrite:\n%s", got)
	}
}
