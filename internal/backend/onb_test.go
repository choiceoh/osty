package backend

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/osty/osty/internal/onb"
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

// recordingBackend captures Emit calls so we can assert that ONB delegated to
// LLVM exactly when expected, without dragging the real LLVM lowering pipeline
// into these unit tests.
type recordingBackend struct {
	name   Name
	calls  int
	last   Request
	result *Result
	err    error
}

func (r *recordingBackend) Name() Name { return r.name }
func (r *recordingBackend) Emit(_ context.Context, req Request) (*Result, error) {
	r.calls++
	r.last = req
	if r.result != nil {
		copy := *r.result
		copy.Emit = req.Emit
		return &copy, r.err
	}
	return nil, r.err
}

// onbDevTestEnv guards the OSTY_ONB_* env vars so each test starts from a
// known-clean baseline regardless of what the developer has exported in
// their shell. Using t.Setenv would conflict with t.Parallel here.
func onbDevTestEnv(t *testing.T, strict bool, timing bool) {
	t.Helper()
	prevStrict := os.Getenv(onb.EnvStrict)
	prevTiming := os.Getenv(onb.EnvTiming)
	set := func(key, val string) {
		if val == "" {
			os.Unsetenv(key)
			return
		}
		os.Setenv(key, val)
	}
	if strict {
		set(onb.EnvStrict, "1")
	} else {
		set(onb.EnvStrict, "")
	}
	if timing {
		set(onb.EnvTiming, "1")
	} else {
		set(onb.EnvTiming, "")
	}
	t.Cleanup(func() {
		set(onb.EnvStrict, prevStrict)
		set(onb.EnvTiming, prevTiming)
	})
}

func TestONBBackendFallsBackToLLVMOnUnsupportedShape(t *testing.T) {
	onbDevTestEnv(t, false, false)

	req := newBackendRequest(t, EmitObject, `fn add(a: Int, b: Int) -> Int { a + b }
fn main() { println(add(1, 2)) }`)
	req.Layout.Target = "aarch64-apple-darwin"
	fallbackArtifacts := req.Artifacts(NameLLVM)
	fake := &recordingBackend{
		name: NameLLVM,
		result: &Result{
			Backend:   NameLLVM,
			Artifacts: fallbackArtifacts,
		},
	}
	result, err := ONBBackend{llvmFallback: fake}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit fallback returned error: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("llvm fallback call count = %d, want 1", fake.calls)
	}
	if fake.last.Emit != EmitObject {
		t.Fatalf("llvm fallback emit = %q, want object", fake.last.Emit)
	}
	if result == nil {
		t.Fatal("fallback result is nil")
	}
	if result.Backend != NameLLVM {
		t.Fatalf("fallback Result.Backend = %q, want %q (be honest about what built it)", result.Backend, NameLLVM)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("fallback result should attach an explanatory warning")
	}
	first := result.Warnings[0]
	if first == nil || !strings.Contains(first.Error(), "onb fallback") {
		t.Fatalf("fallback warning = %v, want onb fallback marker", first)
	}
}

func TestONBBackendStrictModeSurfacesShapeError(t *testing.T) {
	onbDevTestEnv(t, true, false)

	req := newBackendRequest(t, EmitObject, `fn add(a: Int, b: Int) -> Int { a + b }
fn main() { println(add(1, 2)) }`)
	req.Layout.Target = "aarch64-apple-darwin"
	fake := &recordingBackend{name: NameLLVM}
	_, err := ONBBackend{llvmFallback: fake}.Emit(context.Background(), req)
	if err == nil {
		t.Fatal("strict mode should surface ONB rejection error")
	}
	if !errors.Is(err, onb.ErrUnsupportedShape) {
		t.Fatalf("strict-mode error = %v, want errors.Is ErrUnsupportedShape", err)
	}
	if fake.calls != 0 {
		t.Fatalf("strict mode delegated to llvm %d times; want 0", fake.calls)
	}
}

func TestONBBackendASMEmitDoesNotFallback(t *testing.T) {
	onbDevTestEnv(t, false, false)

	req := newBackendRequest(t, EmitASM, `fn add(a: Int, b: Int) -> Int { a + b }
fn main() { println(add(1, 2)) }`)
	req.Layout.Target = "aarch64-apple-darwin"
	fake := &recordingBackend{name: NameLLVM}
	_, err := ONBBackend{llvmFallback: fake}.Emit(context.Background(), req)
	if err == nil {
		t.Fatal("ASM emit should hard-fail on shape rejection — user explicitly asked for ONB asm")
	}
	if !errors.Is(err, onb.ErrUnsupportedShape) {
		t.Fatalf("asm-mode error = %v, want errors.Is ErrUnsupportedShape", err)
	}
	if fake.calls != 0 {
		t.Fatalf("asm-mode delegated to llvm %d times; want 0", fake.calls)
	}
}

func TestONBBackendTimingLogged(t *testing.T) {
	onbDevTestEnv(t, false, true)

	req := newBackendRequest(t, EmitASM, `fn main() {}`)
	req.Layout.Target = "aarch64-apple-darwin"
	var buf bytes.Buffer
	_, err := ONBBackend{logSink: &buf}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit returned error: %v", err)
	}
	got := buf.String()
	if !strings.HasPrefix(got, "onb: emit ") {
		t.Fatalf("timing log = %q, want onb: emit prefix", got)
	}
	if !strings.Contains(got, "[native: aarch64-apple-darwin]") {
		t.Fatalf("timing log = %q, want native target tag", got)
	}
}

func TestONBBackendTimingLogsFallback(t *testing.T) {
	onbDevTestEnv(t, false, true)

	req := newBackendRequest(t, EmitObject, `fn add(a: Int, b: Int) -> Int { a + b }
fn main() { println(add(1, 2)) }`)
	req.Layout.Target = "aarch64-apple-darwin"
	fake := &recordingBackend{
		name: NameLLVM,
		result: &Result{
			Backend:   NameLLVM,
			Artifacts: req.Artifacts(NameLLVM),
		},
	}
	var buf bytes.Buffer
	_, err := ONBBackend{llvmFallback: fake, logSink: &buf}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit fallback returned error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "fallback to llvm") {
		t.Fatalf("timing log = %q, want fallback marker", got)
	}
	if !strings.Contains(got, "instruction") {
		t.Fatalf("timing log = %q, want quoted shape reason", got)
	}
}
