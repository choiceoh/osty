package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestRunLoadedPackageGenUsesPackageLoweringForSiblingFiles(t *testing.T) {
	dir := t.TempDir()
	writePipelineTestFile(t, dir, "a.osty", "pub fn helper() -> Int { 1 }\n")
	writePipelineTestFile(t, dir, "b.osty", "fn main() { println(helper()) }\n")

	pkg, err := resolve.LoadPackage(dir)
	if err != nil {
		t.Fatalf("LoadPackage() error = %v", err)
	}
	got := RunLoadedPackage(pkg, nil, Config{RunGen: true})
	if got.GenError != nil {
		t.Fatalf("RunLoadedPackage() gen error = %v\n%s", got.GenError, got.GenBytes)
	}
	text := string(got.GenBytes)
	if strings.Contains(text, "; ---- ") {
		t.Fatalf("package gen output kept per-file headers:\n%s", text)
	}
	if !strings.Contains(text, "define i64 @helper(") {
		t.Fatalf("package gen output missing helper definition:\n%s", text)
	}
	if !strings.Contains(text, "@main(") {
		t.Fatalf("package gen output missing main definition:\n%s", text)
	}
}

func TestRunWorkspaceGenAggregatesPerPackageModules(t *testing.T) {
	root := t.TempDir()
	alpha := filepath.Join(root, "alpha")
	beta := filepath.Join(root, "beta")
	if err := os.MkdirAll(alpha, 0o755); err != nil {
		t.Fatalf("MkdirAll(alpha): %v", err)
	}
	if err := os.MkdirAll(beta, 0o755); err != nil {
		t.Fatalf("MkdirAll(beta): %v", err)
	}
	writePipelineTestFile(t, alpha, "a.osty", "pub fn helper() -> Int { 1 }\n")
	writePipelineTestFile(t, alpha, "b.osty", "fn main() { println(helper()) }\n")
	writePipelineTestFile(t, beta, "main.osty", "fn main() { println(7) }\n")

	got, err := RunWorkspace(root, nil, Config{RunGen: true})
	if err != nil {
		t.Fatalf("RunWorkspace() error = %v", err)
	}
	if got.GenError != nil {
		t.Fatalf("RunWorkspace() gen error = %v\n%s", got.GenError, got.GenBytes)
	}
	text := string(got.GenBytes)
	if strings.Count(text, "; ---- ") != 2 {
		t.Fatalf("workspace gen header count = %d, want 2 package headers\n%s", strings.Count(text, "; ---- "), text)
	}
	if !strings.Contains(text, alpha) || !strings.Contains(text, beta) {
		t.Fatalf("workspace gen output missing package headers:\n%s", text)
	}
	if strings.Contains(text, "; ---- "+filepath.Join(alpha, "a.osty")+" ----") || strings.Contains(text, "; ---- "+filepath.Join(alpha, "b.osty")+" ----") {
		t.Fatalf("workspace gen output still uses per-file headers:\n%s", text)
	}
}

func TestRunLoadedPackageGenUsesManagedNativeLLVMGenWhenCovered(t *testing.T) {
	dir := t.TempDir()
	writePipelineTestFile(t, dir, "a.osty", "pub fn helper() -> Int { 1 }\n")
	writePipelineTestFile(t, dir, "b.osty", "fn main() { println(helper()) }\n")

	pkg, err := resolve.LoadPackage(dir)
	if err != nil {
		t.Fatalf("LoadPackage() error = %v", err)
	}

	oldTry := tryExternalPipelineLLVMIR
	tryExternalPipelineLLVMIR = func(entryPath string, gotPkg *resolve.Package) ([]byte, bool, []error, error) {
		if filepath.Base(entryPath) != "a.osty" {
			t.Fatalf("entry path = %q, want first lowerable file a.osty", entryPath)
		}
		if gotPkg != pkg {
			t.Fatal("pipeline passed an unexpected package pointer")
		}
		return []byte("; external pipeline llvm ir"), true, nil, nil
	}
	t.Cleanup(func() { tryExternalPipelineLLVMIR = oldTry })

	got := RunLoadedPackage(pkg, nil, Config{RunGen: true})
	if got.GenError != nil {
		t.Fatalf("RunLoadedPackage() gen error = %v\n%s", got.GenError, got.GenBytes)
	}
	if string(got.GenBytes) != "; external pipeline llvm ir" {
		t.Fatalf("gen bytes = %q, want external output", got.GenBytes)
	}
}

func TestRunLoadedPackageGenSingleFileUsesManagedNativeLLVMGen(t *testing.T) {
	dir := t.TempDir()
	writePipelineTestFile(t, dir, "main.osty", "fn main() { println(1) }\n")

	pkg, err := resolve.LoadPackage(dir)
	if err != nil {
		t.Fatalf("LoadPackage() error = %v", err)
	}

	oldTry := tryExternalPipelineLLVMIR
	tryExternalPipelineLLVMIR = func(entryPath string, gotPkg *resolve.Package) ([]byte, bool, []error, error) {
		if filepath.Base(entryPath) != "main.osty" {
			t.Fatalf("entry path = %q, want main.osty", entryPath)
		}
		if gotPkg != pkg {
			t.Fatal("pipeline passed an unexpected package pointer")
		}
		return []byte("; external single-file llvm ir"), true, nil, nil
	}
	t.Cleanup(func() { tryExternalPipelineLLVMIR = oldTry })

	got := RunLoadedPackage(pkg, nil, Config{RunGen: true})
	if got.GenError != nil {
		t.Fatalf("RunLoadedPackage() gen error = %v\n%s", got.GenError, got.GenBytes)
	}
	if string(got.GenBytes) != "; external single-file llvm ir" {
		t.Fatalf("gen bytes = %q, want external single-file output", got.GenBytes)
	}
}

func writePipelineTestFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}
