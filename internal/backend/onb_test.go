package backend

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

type fakeONBLinker struct {
	links []linkCall
}

func (f *fakeONBLinker) LinkBinary(_ context.Context, objectPaths []string, binaryPath, target string, linkLibraries []string) error {
	f.links = append(f.links, linkCall{
		objectPaths:   append([]string(nil), objectPaths...),
		binaryPath:    binaryPath,
		target:        target,
		linkLibraries: append([]string(nil), linkLibraries...),
	})
	return os.WriteFile(binaryPath, []byte("onb fake binary"), 0o755)
}

func TestONBBackendRegisteredAsDevBackend(t *testing.T) {
	t.Parallel()

	name, err := ParseName("onb")
	if err != nil {
		t.Fatalf("ParseName(onb) returned error: %v", err)
	}
	if name != NameONB {
		t.Fatalf("ParseName(onb) = %q, want %q", name, NameONB)
	}
	if !EmitBinary.ValidFor(NameONB) || !EmitObject.ValidFor(NameONB) {
		t.Fatal("ONB should accept object and binary emission")
	}
	if !EmitASM.ValidFor(NameONB) {
		t.Fatal("ONB should accept assembly emission")
	}
	if EmitLLVMIR.ValidFor(NameONB) {
		t.Fatal("ONB must not claim LLVM IR emission")
	}
}

func TestONBBackendEmitAssemblyWritesArtifact(t *testing.T) {
	t.Parallel()

	req := newBackendRequest(t, EmitASM, `fn main() {}`)
	req.Layout.Target = "aarch64-apple-darwin"
	result, err := ONBBackend{}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit(asm) returned error: %v", err)
	}
	if result == nil || result.Artifacts.Assembly == "" {
		t.Fatalf("ONBBackend.Emit(asm) result = %+v", result)
	}
	data, err := os.ReadFile(result.Artifacts.Assembly)
	if err != nil {
		t.Fatalf("ReadFile(%q) returned error: %v", result.Artifacts.Assembly, err)
	}
	text := string(data)
	for _, want := range []string{"_main:", "\tmov w0, #0", "\tret"} {
		if !strings.Contains(text, want) {
			t.Fatalf("assembly missing %q:\n%s", want, text)
		}
	}
}

func TestONBBackendEmitAssemblyForPrintlnLiteral(t *testing.T) {
	t.Parallel()

	req := newBackendRequest(t, EmitASM, `fn main() { println("hi") }`)
	req.Layout.Target = "aarch64-apple-darwin"
	result, err := ONBBackend{}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit(asm) returned error: %v", err)
	}
	data, err := os.ReadFile(result.Artifacts.Assembly)
	if err != nil {
		t.Fatalf("ReadFile(%q) returned error: %v", result.Artifacts.Assembly, err)
	}
	text := string(data)
	for _, want := range []string{"\tbl _puts", ".section __TEXT,__cstring,cstring_literals", "\t.asciz \"hi\""} {
		if !strings.Contains(text, want) {
			t.Fatalf("assembly missing %q:\n%s", want, text)
		}
	}
}

func TestONBBackendEmitAssemblyForPrintlnIntLiteral(t *testing.T) {
	t.Parallel()

	req := newBackendRequest(t, EmitASM, `fn main() { println(123) }`)
	req.Layout.Target = "aarch64-apple-darwin"
	result, err := ONBBackend{}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit(asm) returned error: %v", err)
	}
	data, err := os.ReadFile(result.Artifacts.Assembly)
	if err != nil {
		t.Fatalf("ReadFile(%q) returned error: %v", result.Artifacts.Assembly, err)
	}
	text := string(data)
	for _, want := range []string{"\tmovz x1, #123", "\tstr x1, [sp]", "\tbl _printf", "\t.asciz \"%lld\\n\""} {
		if !strings.Contains(text, want) {
			t.Fatalf("assembly missing %q:\n%s", want, text)
		}
	}
}

func TestONBBackendEmitObjectWritesMachOArtifact(t *testing.T) {
	t.Parallel()

	req := newBackendRequest(t, EmitObject, `fn main() {}`)
	req.Layout.Target = "aarch64-apple-darwin"
	result, err := ONBBackend{}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit(object) returned error: %v", err)
	}
	if result == nil {
		t.Fatal("ONBBackend.Emit() returned nil result")
	}
	if result.Backend != NameONB {
		t.Fatalf("result.Backend = %q, want %q", result.Backend, NameONB)
	}
	if result.Artifacts.Object == "" {
		t.Fatal("ONB object artifact path is empty")
	}
	if _, err := os.Stat(result.Artifacts.Object); err != nil {
		t.Fatalf("ONB object artifact was not written: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("ONB result should include the phase plan warning")
	}
}

func TestONBBackendEmitBinaryInvokesHostLinker(t *testing.T) {
	t.Parallel()

	req := newBackendRequest(t, EmitBinary, `fn main() {}`)
	req.Layout.Target = "aarch64-apple-darwin"
	linker := &fakeONBLinker{}
	result, err := (ONBBackend{linker: linker}).Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit(binary) returned error: %v", err)
	}
	if result == nil || result.Artifacts.Object == "" || result.Artifacts.Binary == "" {
		t.Fatalf("ONBBackend.Emit(binary) result = %+v", result)
	}
	if _, err := os.Stat(result.Artifacts.Object); err != nil {
		t.Fatalf("ONB binary path should leave object artifact for debugging: %v", err)
	}
	if _, err := os.Stat(result.Artifacts.Binary); err != nil {
		t.Fatalf("ONB binary artifact was not written: %v", err)
	}
	if len(linker.links) != 1 {
		t.Fatalf("link calls = %d, want 1", len(linker.links))
	}
	if got := linker.links[0].objectPaths; len(got) != 1 || got[0] != result.Artifacts.Object {
		t.Fatalf("link object paths = %v, want [%s]", got, result.Artifacts.Object)
	}
}

func TestONBBackendBinaryRunsUnitMainOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB phase 1.0 executable smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	req := newBackendRequest(t, EmitBinary, `fn main() {}`)
	req.Layout.Target = "aarch64-apple-darwin"
	result, err := ONBBackend{}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit(binary) returned error: %v", err)
	}
	if result == nil || result.Artifacts.Binary == "" {
		t.Fatalf("ONBBackend.Emit(binary) result = %+v", result)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ONB binary returned error: %v\n%s", err, out)
	}
}

func TestONBBackendBinaryRunsPrintlnLiteralOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB println executable smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	req := newBackendRequest(t, EmitBinary, `fn main() { println("hi") }`)
	req.Layout.Target = "aarch64-apple-darwin"
	result, err := ONBBackend{}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit(binary) returned error: %v", err)
	}
	if result == nil || result.Artifacts.Binary == "" {
		t.Fatalf("ONBBackend.Emit(binary) result = %+v", result)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ONB binary returned error: %v\n%s", err, out)
	}
	if string(out) != "hi\n" {
		t.Fatalf("ONB binary output = %q, want hi newline", out)
	}
}

func TestONBBackendBinaryRunsPrintlnIntLiteralOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB println executable smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	req := newBackendRequest(t, EmitBinary, `fn main() { println(123) }`)
	req.Layout.Target = "aarch64-apple-darwin"
	result, err := ONBBackend{}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit(binary) returned error: %v", err)
	}
	if result == nil || result.Artifacts.Binary == "" {
		t.Fatalf("ONBBackend.Emit(binary) result = %+v", result)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ONB binary returned error: %v\n%s", err, out)
	}
	if string(out) != "123\n" {
		t.Fatalf("ONB binary output = %q, want 123 newline", out)
	}
}
