package nativellvmgen

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestTrySourceUsesEnvBinaryAndDecodesResponse(t *testing.T) {
	bin := buildFakeNativeLLVMGen(t)
	capture := filepath.Join(t.TempDir(), "request.json")

	t.Setenv(Env, bin)
	t.Setenv("FAKE_NATIVE_LLVMGEN_CAPTURE", capture)
	t.Setenv("FAKE_NATIVE_LLVMGEN_RESPONSE", `{"covered":true,"llvmIr":"define i64 @main()","warnings":["from-external"]}`)

	oldEnsure := ensureManagedBinary
	ensureManagedBinary = func(string) (string, error) {
		t.Fatal("ensureManagedBinary should not be called when env override is set")
		return "", nil
	}
	t.Cleanup(func() { ensureManagedBinary = oldEnsure })

	ir, ok, warnings, err := TrySource(".", "main.osty", []byte("fn main() {}\n"))
	if err != nil {
		t.Fatalf("TrySource error: %v", err)
	}
	if !ok {
		t.Fatal("covered = false, want true")
	}
	if got, want := string(ir), "define i64 @main()"; got != want {
		t.Fatalf("llvm ir = %q, want %q", got, want)
	}
	if len(warnings) != 1 || warnings[0].Error() != "from-external" {
		t.Fatalf("warnings = %#v, want external warning", warnings)
	}

	var req Request
	decodeCapturedRequest(t, capture, &req)
	if req.Path != "main.osty" {
		t.Fatalf("request path = %q, want main.osty", req.Path)
	}
	if req.Source != "fn main() {}\n" {
		t.Fatalf("request source = %q, want original source", req.Source)
	}
	if req.Package != nil {
		t.Fatalf("request package = %#v, want nil", req.Package)
	}
}

func TestTryPackageUsesManagedBinaryWhenEnvUnset(t *testing.T) {
	bin := buildFakeNativeLLVMGen(t)
	capture := filepath.Join(t.TempDir(), "request.json")

	t.Setenv(Env, "")
	t.Setenv("FAKE_NATIVE_LLVMGEN_CAPTURE", capture)
	t.Setenv("FAKE_NATIVE_LLVMGEN_RESPONSE", `{"covered":true,"llvmIr":"define i64 @helper()","warnings":["pkg-warning"]}`)

	oldEnsure := ensureManagedBinary
	ensureManagedBinary = func(string) (string, error) { return bin, nil }
	t.Cleanup(func() { ensureManagedBinary = oldEnsure })

	pkg := &resolve.Package{
		Dir:  "/tmp/demo",
		Name: "demo",
		Files: []*resolve.PackageFile{
			{Path: "/tmp/demo/a.osty", Source: []byte("pub fn helper() -> Int { 1 }\n")},
			{Path: "/tmp/demo/b.osty", Source: []byte("fn main() { println(helper()) }\n")},
		},
	}

	ir, ok, warnings, err := TryPackage(".", "/tmp/demo/b.osty", pkg)
	if err != nil {
		t.Fatalf("TryPackage error: %v", err)
	}
	if !ok {
		t.Fatal("covered = false, want true")
	}
	if got, want := string(ir), "define i64 @helper()"; got != want {
		t.Fatalf("llvm ir = %q, want %q", got, want)
	}
	if len(warnings) != 1 || warnings[0].Error() != "pkg-warning" {
		t.Fatalf("warnings = %#v, want pkg-warning", warnings)
	}

	var req Request
	decodeCapturedRequest(t, capture, &req)
	if req.Path != "/tmp/demo/b.osty" {
		t.Fatalf("request path = %q, want entry path", req.Path)
	}
	if req.Package == nil || len(req.Package.Files) != 2 {
		t.Fatalf("package files = %#v, want 2 files", req.Package)
	}
	if got := req.Package.Files[0].Path; got != "/tmp/demo/a.osty" {
		t.Fatalf("file[0].path = %q, want /tmp/demo/a.osty", got)
	}
	if got := req.Package.Files[1].Name; got != "b.osty" {
		t.Fatalf("file[1].name = %q, want b.osty", got)
	}
}

func buildFakeNativeLLVMGen(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(fakeNativeLLVMGenProgram), 0o644); err != nil {
		t.Fatalf("write fake native llvmgen: %v", err)
	}
	name := "fake-native-llvmgen"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", bin, src)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build fake native llvmgen: %v\n%s", err, out)
	}
	return bin
}

func decodeCapturedRequest(t *testing.T, path string, out any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

const fakeNativeLLVMGenProgram = `package main

import (
	"io"
	"os"
)

func main() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		panic(err)
	}
	if capture := os.Getenv("FAKE_NATIVE_LLVMGEN_CAPTURE"); capture != "" {
		if err := os.WriteFile(capture, data, 0o644); err != nil {
			panic(err)
		}
	}
	if stderr := os.Getenv("FAKE_NATIVE_LLVMGEN_STDERR"); stderr != "" {
		_, _ = os.Stderr.WriteString(stderr)
	}
	if code := os.Getenv("FAKE_NATIVE_LLVMGEN_EXIT"); code != "" && code != "0" {
		os.Exit(1)
	}
	_, _ = os.Stdout.WriteString(os.Getenv("FAKE_NATIVE_LLVMGEN_RESPONSE"))
}
`
