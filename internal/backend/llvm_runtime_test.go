package backend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

type fakeLLVMToolchain struct {
	compileIRs     []string
	compileObjects []string
	linkObjects    [][]string
	linkBinaries   []string
}

func (f *fakeLLVMToolchain) CompileObject(_ context.Context, irPath, objectPath, _ string) error {
	f.compileIRs = append(f.compileIRs, irPath)
	f.compileObjects = append(f.compileObjects, objectPath)
	return nil
}

func (f *fakeLLVMToolchain) LinkBinary(_ context.Context, objectPaths []string, binaryPath, _ string) error {
	copied := append([]string(nil), objectPaths...)
	f.linkObjects = append(f.linkObjects, copied)
	f.linkBinaries = append(f.linkBinaries, binaryPath)
	return nil
}

func testLLVMRequest(t *testing.T, src string, emit EmitMode, root string) Request {
	t.Helper()
	file, diags := parser.ParseDiagnostics([]byte(src))
	if len(diags) != 0 {
		t.Fatalf("parse diagnostics: %v", diags)
	}
	reg := stdlib.LoadCached()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	if len(res.Diags) != 0 {
		t.Fatalf("resolve diagnostics: %v", res.Diags)
	}
	return Request{
		Layout:     Layout{Root: root, Profile: "debug"},
		Emit:       emit,
		BinaryName: "app",
		Entry: Entry{
			PackageName: "main",
			SourcePath:  filepath.Join(root, "main.osty"),
			File:        file,
			Resolve:     res,
		},
	}
}

func TestLLVMBackendEmitBinaryLinksLocalGCRuntime(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	toolchain := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: toolchain}
	req := testLLVMRequest(t, "fn main() {\n    println(42)\n}\n", EmitBinary, dir)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
	if result == nil {
		t.Fatal("Emit() returned nil result")
	}
	if got := len(toolchain.compileObjects); got != 2 {
		t.Fatalf("CompileObject call count = %d, want 2", got)
	}
	runtimeIRPath := filepath.Join(result.Artifacts.RuntimeDir, "gc_runtime.ll")
	runtimeObjectPath := filepath.Join(result.Artifacts.RuntimeDir, "gc_runtime.o")
	if toolchain.compileObjects[0] != result.Artifacts.Object {
		t.Fatalf("first object = %q, want %q", toolchain.compileObjects[0], result.Artifacts.Object)
	}
	if toolchain.compileObjects[1] != runtimeObjectPath {
		t.Fatalf("second object = %q, want %q", toolchain.compileObjects[1], runtimeObjectPath)
	}
	if got := len(toolchain.linkObjects); got != 1 {
		t.Fatalf("LinkBinary call count = %d, want 1", got)
	}
	if got, want := toolchain.linkObjects[0], []string{result.Artifacts.Object, runtimeObjectPath}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("link objects = %v, want %v", got, want)
	}
	data, err := os.ReadFile(runtimeIRPath)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", runtimeIRPath, err)
	}
	ir := string(data)
	for _, want := range []string{`define ptr @"osty.gc.alloc_v1"`, `define void @"osty.gc.post_write_v1"`, `define void @"osty.gc.root_bind_v1"`, `define void @"osty.gc.root_release_v1"`} {
		if !contains(ir, want) {
			t.Fatalf("runtime IR missing %q\nIR:\n%s", want, ir)
		}
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
