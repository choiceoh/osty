package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/parser"
)

type compileCall struct {
	sourcePath string
	objectPath string
	target     string
}

type linkCall struct {
	objectPaths []string
	binaryPath  string
	target      string
}

type fakeLLVMToolchain struct {
	irCompiles []compileCall
	cCompiles  []compileCall
	links      []linkCall
}

func (f *fakeLLVMToolchain) CompileObject(_ context.Context, irPath, objectPath, target string) error {
	f.irCompiles = append(f.irCompiles, compileCall{
		sourcePath: irPath,
		objectPath: objectPath,
		target:     target,
	})
	return nil
}

func (f *fakeLLVMToolchain) CompileCObject(_ context.Context, sourcePath, objectPath, target string) error {
	f.cCompiles = append(f.cCompiles, compileCall{
		sourcePath: sourcePath,
		objectPath: objectPath,
		target:     target,
	})
	return nil
}

func (f *fakeLLVMToolchain) LinkBinary(_ context.Context, objectPaths []string, binaryPath, target string) error {
	f.links = append(f.links, linkCall{
		objectPaths: append([]string(nil), objectPaths...),
		binaryPath:  binaryPath,
		target:      target,
	})
	return nil
}

func parseBackendFile(t *testing.T, src string) *ast.File {
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

func newBackendRequest(t *testing.T, emit EmitMode, src string) Request {
	t.Helper()

	root := t.TempDir()
	return Request{
		Layout: Layout{
			Root:    root,
			Profile: "debug",
		},
		Emit: emit,
		Entry: Entry{
			PackageName: "main",
			SourcePath:  filepath.Join(root, "main.osty"),
			File:        parseBackendFile(t, src),
		},
		BinaryName: "app",
	}
}

func TestLLVMBackendEmitBinaryBuildsBundledRuntime(t *testing.T) {
	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let mut values: List<Int> = []
    values.push(1)
    println(values.len())
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	if result == nil {
		t.Fatal("Emit returned nil result")
	}
	if got := len(tc.irCompiles); got != 1 {
		t.Fatalf("IR compile count = %d, want 1", got)
	}
	if got := len(tc.cCompiles); got != 1 {
		t.Fatalf("runtime compile count = %d, want 1", got)
	}
	if got := len(tc.links); got != 1 {
		t.Fatalf("link count = %d, want 1", got)
	}
	runtimeSource := filepath.Join(result.Artifacts.RuntimeDir, bundledRuntimeSourceName)
	runtimeObject := filepath.Join(result.Artifacts.RuntimeDir, bundledRuntimeObjectName)
	if got := tc.cCompiles[0].sourcePath; got != runtimeSource {
		t.Fatalf("runtime source path = %q, want %q", got, runtimeSource)
	}
	if got := tc.cCompiles[0].objectPath; got != runtimeObject {
		t.Fatalf("runtime object path = %q, want %q", got, runtimeObject)
	}
	content, err := os.ReadFile(runtimeSource)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", runtimeSource, err)
	}
	for _, want := range []string{
		"osty_rt_list_new",
		"osty_rt_strings_Equal",
		"osty.gc.root_bind_v1",
		"osty_gc_debug_collect",
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("bundled runtime source missing %q", want)
		}
	}
	link := tc.links[0]
	if len(link.objectPaths) != 2 {
		t.Fatalf("link object count = %d, want 2 (%v)", len(link.objectPaths), link.objectPaths)
	}
	if got := link.objectPaths[0]; got != result.Artifacts.Object {
		t.Fatalf("link object[0] = %q, want %q", got, result.Artifacts.Object)
	}
	if got := link.objectPaths[1]; got != runtimeObject {
		t.Fatalf("link object[1] = %q, want %q", got, runtimeObject)
	}
}

func TestLLVMBackendEmitLLVMIRSkipsToolchain(t *testing.T) {
	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    println(1)
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	if result == nil {
		t.Fatal("Emit returned nil result")
	}
	if got := len(tc.irCompiles); got != 0 {
		t.Fatalf("IR compile count = %d, want 0", got)
	}
	if got := len(tc.cCompiles); got != 0 {
		t.Fatalf("runtime compile count = %d, want 0", got)
	}
	if got := len(tc.links); got != 0 {
		t.Fatalf("link count = %d, want 0", got)
	}
	if _, err := os.Stat(result.Artifacts.RuntimeDir); err != nil {
		t.Fatalf("runtime dir %q missing: %v", result.Artifacts.RuntimeDir, err)
	}
}

func TestLLVMBackendBinaryRunsBundledRuntime(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn touch() {
    let mut values: List<Int> = []
    values.push(41)
    values.push(1)
    println(values.len())
}

fn main() {
    touch()
    if "osty" == "osty" {
        println(1)
    } else {
        println(0)
    }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "2\n1\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}
