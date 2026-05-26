package nativellvmgen

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/resolve"
)

var (
	fakeNativeLLVMGenOnce sync.Once
	fakeNativeLLVMGenPath string
	fakeNativeLLVMGenErr  error
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
		Dir:               "/tmp/demo",
		Name:              "demo",
		RuntimeCapability: true,
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
	if !req.Package.RuntimeCapability {
		t.Fatal("runtime capability was not forwarded")
	}
}

func TestTryPackageLibraryForSymbolsForwardsRequiredSymbols(t *testing.T) {
	bin := buildFakeNativeLLVMGen(t)
	capture := filepath.Join(t.TempDir(), "request.json")

	t.Setenv(Env, bin)
	t.Setenv("FAKE_NATIVE_LLVMGEN_CAPTURE", capture)
	t.Setenv("FAKE_NATIVE_LLVMGEN_RESPONSE", `{"covered":true,"llvmIr":"define i64 @toolchain.frontCheckSourceToWireJson()"}`)

	pkg := &resolve.Package{
		Dir:  "/tmp/toolchain",
		Name: "toolchain",
		Files: []*resolve.PackageFile{
			{Path: "/tmp/toolchain/check_json.osty", Source: []byte("pub fn frontCheckSourceToWireJson(source: String) -> String { \"\" }\n")},
		},
	}
	required := []string{"toolchain.frontCheckSourceToWireJson", "toolchain.frontCheckPackageToWireJson"}

	_, ok, _, err := TryPackageLibraryForSymbols(".", "/tmp/toolchain/check_json.osty", pkg, required)
	if err != nil {
		t.Fatalf("TryPackageLibraryForSymbols error: %v", err)
	}
	if !ok {
		t.Fatal("covered = false, want true")
	}

	var req Request
	decodeCapturedRequest(t, capture, &req)
	if req.Package == nil {
		t.Fatal("request package = nil")
	}
	if !req.Package.LibraryMode {
		t.Fatal("LibraryMode = false, want true")
	}
	if !slices.Equal(req.Package.RequiredSymbols, required) {
		t.Fatalf("RequiredSymbols = %v, want %v", req.Package.RequiredSymbols, required)
	}
}

func TestRequestFromMIREncodesPayload(t *testing.T) {
	sourcePath := "/tmp/mir_main.osty"
	req, err := RequestFromMIR("main", sourcePath, []byte("fn main() -> Int { 7 }\n"), simpleReturnIntMIR(), "x86_64-unknown-linux-gnu")
	if err != nil {
		t.Fatalf("RequestFromMIR error: %v", err)
	}
	if req.Path != sourcePath {
		t.Fatalf("request path = %q, want source path", req.Path)
	}
	if req.Package != nil || req.Source != "" {
		t.Fatalf("request carried source/package path: %#v", req)
	}
	if req.MIR == nil || req.MIR.Module == nil {
		t.Fatalf("request MIR payload missing: %#v", req.MIR)
	}
	if req.MIR.PackageName != "main" || req.MIR.Target != "x86_64-unknown-linux-gnu" {
		t.Fatalf("MIR metadata = %#v", req.MIR)
	}
	if got := req.MIR.Module.Functions[0].Name; got != "main" {
		t.Fatalf("MIR function = %q, want main", got)
	}
	if got := req.MIR.Module.Functions[0].Blocks[0].Instrs[0].Kind; got != "assign" {
		t.Fatalf("MIR instr kind = %q, want assign", got)
	}
}

func simpleReturnIntMIR() *mir.Module {
	fn := &mir.Function{Name: "main", ReturnType: ir.TInt}
	ret := fn.NewLocal("_return", ir.TInt, true, mir.Span{})
	fn.ReturnLocal = ret
	fn.Locals[ret].IsReturn = true
	entry := fn.NewBlock(mir.Span{})
	fn.Entry = entry
	bb := fn.Block(entry)
	bb.Append(&mir.AssignInstr{
		Dest: mir.Place{Local: ret},
		Src:  &mir.UseRV{Op: &mir.ConstOp{Const: &mir.IntConst{Value: 7, T: ir.TInt}, T: ir.TInt}},
	})
	bb.SetTerminator(&mir.ReturnTerm{})
	return &mir.Module{Package: "main", Functions: []*mir.Function{fn}, Layouts: mir.NewLayoutTable()}
}

func buildFakeNativeLLVMGen(t *testing.T) string {
	t.Helper()

	fakeNativeLLVMGenOnce.Do(func() {
		fakeNativeLLVMGenPath, fakeNativeLLVMGenErr = buildFakeNativeLLVMGenOnce()
	})
	if fakeNativeLLVMGenErr != nil {
		t.Fatal(fakeNativeLLVMGenErr)
	}
	return fakeNativeLLVMGenPath
}

func buildFakeNativeLLVMGenOnce() (string, error) {
	dir, err := os.MkdirTemp("", "fake-native-llvmgen-*")
	if err != nil {
		return "", fmt.Errorf("mktemp: %w", err)
	}
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(fakeNativeLLVMGenProgram), 0o644); err != nil {
		return "", fmt.Errorf("write fake native llvmgen: %w", err)
	}
	name := "fake-native-llvmgen"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", bin, src)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go build fake native llvmgen: %w\n%s", err, out)
	}
	return bin, nil
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
