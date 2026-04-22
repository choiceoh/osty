package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuildCLIUsesManagedNativeLLVMGenForObject(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	writeNativeTestFile(t, dir, "osty.toml", "[package]\nname = \"demo\"\nversion = \"0.1.0\"\nedition = \"0.4\"\n")
	target := writeNativeTestFile(t, dir, "main.osty", "fn main() { println(1) }\n")
	fake := buildFakeExternalLLVMGenCLI(t)
	capture := filepath.Join(t.TempDir(), "request.json")

	t.Setenv("OSTY_NATIVE_LLVMGEN_BIN", fake)
	t.Setenv("FAKE_EXTERNAL_LLVMGEN_CAPTURE", capture)
	t.Setenv("FAKE_EXTERNAL_LLVMGEN_RESPONSE", `{"covered":true,"llvmIr":"source_filename = \"fake\"\ndefine i32 @main() { ret i32 0 }\n"}`)

	got := runOstyCLI(t, "build", "--emit", "object", target)
	if got.exit != 0 {
		t.Fatalf("osty build exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if !strings.Contains(got.stdout, ".o (debug)") {
		t.Fatalf("stdout = %q, want object artifact summary", got.stdout)
	}

	var req externalLLVMGenRequest
	readCapturedExternalLLVMGenRequest(t, capture, &req)
	if req.Path != target {
		t.Fatalf("request path = %q, want %q", req.Path, target)
	}
	if req.Package == nil || len(req.Package.Files) != 1 {
		t.Fatalf("package request = %#v, want single-file package", req.Package)
	}
	if req.Package.Files[0].Source != "fn main() { println(1) }\n" {
		t.Fatalf("request source = %q, want original package source", req.Package.Files[0].Source)
	}
}

func TestRunCLIUsesManagedNativeLLVMGenForBinary(t *testing.T) {
	requireClangForNativeTest(t)

	dir := t.TempDir()
	writeNativeTestFile(t, dir, "osty.toml", "[package]\nname = \"demo\"\nversion = \"0.1.0\"\nedition = \"0.4\"\n")
	target := writeNativeTestFile(t, dir, "main.osty", "fn main() { println(1) }\n")
	fake := buildFakeExternalLLVMGenCLI(t)
	capture := filepath.Join(t.TempDir(), "request.json")

	t.Setenv("OSTY_NATIVE_LLVMGEN_BIN", fake)
	t.Setenv("FAKE_EXTERNAL_LLVMGEN_CAPTURE", capture)
	t.Setenv("FAKE_EXTERNAL_LLVMGEN_RESPONSE", `{"covered":true,"llvmIr":"source_filename = \"fake\"\ndefine i32 @main() { ret i32 0 }\n"}`)

	got := runOstyCLIInDir(t, dir, "run")
	if got.exit != 0 {
		t.Fatalf("osty run exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.TrimSpace(got.stdout) != "" {
		t.Fatalf("stdout = %q, want empty output from fake native llvmgen binary", got.stdout)
	}

	var req externalLLVMGenRequest
	readCapturedExternalLLVMGenRequest(t, capture, &req)
	if req.Path != target {
		t.Fatalf("request path = %q, want %q", req.Path, target)
	}
	if req.Package == nil || len(req.Package.Files) != 1 {
		t.Fatalf("package request = %#v, want single-file package", req.Package)
	}
}

type externalLLVMGenRequest struct {
	Path    string                   `json:"path,omitempty"`
	Source  string                   `json:"source,omitempty"`
	Package *externalLLVMGenPkgInput `json:"package,omitempty"`
}

type externalLLVMGenPkgInput struct {
	Files []externalLLVMGenPackageFile `json:"files,omitempty"`
}

type externalLLVMGenPackageFile struct {
	Path   string `json:"path,omitempty"`
	Name   string `json:"name,omitempty"`
	Source string `json:"source,omitempty"`
}

func buildFakeExternalLLVMGenCLI(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(fakeExternalLLVMGenProgram), 0o644); err != nil {
		t.Fatalf("write fake external llvmgen: %v", err)
	}
	name := "fake-external-llvmgen"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", bin, src)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build fake external llvmgen: %v\n%s", err, out)
	}
	return bin
}

func runOstyCLIInDir(t *testing.T, dir string, args ...string) cliRunResult {
	t.Helper()
	bin := buildOstyCLI(t)
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir

	var stdout strings.Builder
	var stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	result := cliRunResult{}
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run osty %v in %s: %v", args, dir, err)
		}
		result.exit = exitErr.ExitCode()
	}
	result.stdout = stdout.String()
	result.stderr = stderr.String()
	return result
}

func readCapturedExternalLLVMGenRequest(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

const fakeExternalLLVMGenProgram = `package main

import (
	"io"
	"os"
)

func main() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		panic(err)
	}
	if capture := os.Getenv("FAKE_EXTERNAL_LLVMGEN_CAPTURE"); capture != "" {
		if err := os.WriteFile(capture, data, 0o644); err != nil {
			panic(err)
		}
	}
	_, _ = os.Stdout.WriteString(os.Getenv("FAKE_EXTERNAL_LLVMGEN_RESPONSE"))
}
`
