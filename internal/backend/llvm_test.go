package backend

import (
	"context"
	"errors"
	"fmt"
	stdast "go/ast"
	stdparser "go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

type compileCall struct {
	sourcePath string
	objectPath string
	target     string
}

type linkCall struct {
	objectPaths   []string
	binaryPath    string
	target        string
	linkLibraries []string
}

type fakeLLVMToolchain struct {
	irCompiles []compileCall
	cCompiles  []compileCall
	links      []linkCall
}

func (f *fakeLLVMToolchain) CompileObject(_ context.Context, irPath, objectPath, target, _ string) error {
	f.irCompiles = append(f.irCompiles, compileCall{
		sourcePath: irPath,
		objectPath: objectPath,
		target:     target,
	})
	return nil
}

func (f *fakeLLVMToolchain) CompileCObject(_ context.Context, sourcePath, objectPath, target, _ string) error {
	f.cCompiles = append(f.cCompiles, compileCall{
		sourcePath: sourcePath,
		objectPath: objectPath,
		target:     target,
	})
	return nil
}

func (f *fakeLLVMToolchain) LinkBinary(_ context.Context, objectPaths []string, binaryPath, target, _ string, linkLibraries []string) error {
	f.links = append(f.links, linkCall{
		objectPaths:   append([]string(nil), objectPaths...),
		binaryPath:    binaryPath,
		target:        target,
		linkLibraries: append([]string(nil), linkLibraries...),
	})
	return nil
}

func parseBackendFile(t *testing.T, src string) (*ast.File, *resolve.Result, *check.Result) {
	t.Helper()

	file, diags := parser.ParseDiagnostics([]byte(src))
	if len(diags) != 0 {
		t.Fatalf("ParseDiagnostics returned %d diagnostics: %v", len(diags), diags)
	}
	if file == nil {
		t.Fatal("ParseDiagnostics returned nil file")
	}
	res := resolve.ResolveFileSourceDefault([]byte(src), file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{

		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
	})
	return file, res, chk
}

func newBackendRequest(t *testing.T, emit EmitMode, src string) Request {
	t.Helper()

	root := t.TempDir()
	file, res, chk := parseBackendFile(t, src)
	entry, err := PrepareEntry(
		"main",
		filepath.Join(root, "main.osty"),
		file,
		res,
		chk,
	)
	if err != nil {
		t.Fatalf("PrepareEntry returned error: %v", err)
	}
	entry.Source = []byte(src)
	return Request{
		Layout: Layout{
			Root:    root,
			Profile: "debug",
		},
		Emit:       emit,
		Entry:      entry,
		BinaryName: "app",
	}
}

func requireClangForBackendTest(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("clang backend integration skipped in -short")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}
}

func parallelClangBackendTest(t *testing.T) {
	t.Helper()
	requireClangForBackendTest(t)
	t.Parallel()
}

func TestLLVMBackendEmitBinaryBuildsBundledRuntime(t *testing.T) {
	installNativeMIRPayloadStub(t)

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
		"osty_rt_map_new",
		"osty_rt_map_clear",
		"osty_rt_map_values",
		"osty_rt_set_new",
		"osty_rt_set_clear",
		"osty_rt_list_push_bytes_v1",
		"osty_rt_list_push_bytes_roots_v1",
		"osty_rt_list_get_bytes_v1",
		"osty_rt_strings_Equal",
		"osty_rt_crypto_sha256",
		"osty_rt_crypto_random_bytes",
		"osty_rt_result_unwrap_err",
		"osty.gc.pre_write_v1",
		"osty.gc.load_v1",
		"osty.gc.root_bind_v1",
		"osty.gc.safepoint_v1",
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

func TestLLVMBackendPassesRequestLinkLibraries(t *testing.T) {
	installNativeMIRPayloadStub(t)

	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    println(1)
}
`)
	req.LinkLibraries = []string{"osty_qt", "WebView2Loader", "user32"}

	if _, err := backend.Emit(context.Background(), req); err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	if len(tc.links) != 1 {
		t.Fatalf("link count = %d, want 1", len(tc.links))
	}
	if got, want := tc.links[0].linkLibraries, req.LinkLibraries; !slices.Equal(got, want) {
		t.Fatalf("link libraries = %v, want %v", got, want)
	}
}

func TestClangLinkLibraryArgs(t *testing.T) {
	t.Parallel()

	got := clangLinkLibraryArgs([]string{"osty_qt", " WebView2Loader ", "", "-framework", "/opt/libcustom.a", "foo.lib"})
	want := []string{"-losty_qt", "-lWebView2Loader", "-framework", "/opt/libcustom.a", "foo.lib"}
	if !slices.Equal(got, want) {
		t.Fatalf("clang link library args = %v, want %v", got, want)
	}
}

func TestClangPlatformRuntimeLinkArgs(t *testing.T) {
	t.Parallel()

	hostWant := []string{"-lm"}
	if runtime.GOOS == "darwin" {
		hostWant = []string{"-lm", "-framework", "Security", "-framework", "CoreFoundation"}
	} else if runtime.GOOS == "windows" {
		hostWant = []string{"-ladvapi32"}
	}
	cases := []struct {
		name   string
		target string
		want   []string
	}{
		{
			name:   "darwin",
			target: "arm64-apple-darwin",
			want:   []string{"-lm", "-framework", "Security", "-framework", "CoreFoundation"},
		},
		{
			name:   "windows",
			target: "x86_64-pc-windows-msvc",
			want:   []string{"-ladvapi32"},
		},
		{
			name:   "linux",
			target: "x86_64-unknown-linux-gnu",
			want:   []string{"-lm"},
		},
		{
			name:   "host",
			target: "",
			want:   hostWant,
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := clangPlatformRuntimeLinkArgs(tt.target); !slices.Equal(got, tt.want) {
				t.Fatalf("clangPlatformRuntimeLinkArgs(%q) = %v, want %v", tt.target, got, tt.want)
			}
		})
	}
}

func TestLLVMBackendEmitLLVMIRSkipsToolchain(t *testing.T) {
	installNativeMIRPayloadStub(t)

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

func TestLLVMBackendEmitLLVMIRFromIRWithoutASTFallback(t *testing.T) {
	t.Parallel()
	requireRealLLVMEmission(t)

	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    let mut i = 0
    for i < 2 {
        println(i)
        i = i + 1
    }
}
`)
	req.Entry.File = nil

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error with IR-only entry: %v", err)
	}
	if result == nil {
		t.Fatal("Emit returned nil result")
	}
	data, readErr := os.ReadFile(result.Artifacts.LLVMIR)
	if readErr != nil {
		t.Fatalf("ReadFile(%q): %v", result.Artifacts.LLVMIR, readErr)
	}
	got := string(data)
	// Updated assertion (post-PR #2002): the legacy backend that
	// emitted `@printf` for `println(i)` was retired. The LIR Proto
	// path lowers `println(Int)` through `osty_rt_int_to_string` +
	// `osty_rt_io_write`. Match the runtime intrinsic shape; both
	// pieces must appear so we're sure we're going through the
	// Int-to-String → I/O path and not some no-op skipping the call.
	for _, want := range []string{
		"@osty_rt_int_to_string",
		"@osty_rt_io_write",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("IR-only backend output missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "for.cond") && !strings.Contains(got, "bb1:") {
		t.Fatalf("IR-only backend output missing loop label from either legacy or MIR path:\n%s", got)
	}
}

func TestEmitLLVMIRTextMatchesBackendArtifactOutput(t *testing.T) {
	t.Parallel()

	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    println(1)
}
`)
	req.Features = []string{"mir-backend"}
	withNativeMIRPayloadEmitter(t, func(Entry, string) ([]byte, bool, []error, error) {
		return []byte("declare i32 @printf(ptr, ...)\ndefine i32 @main() { ret i32 0 }\n"), true, nil, nil
	})

	got, _, directErr := EmitLLVMIRText(req.Entry, "", req.Features)
	if directErr != nil {
		t.Fatalf("EmitLLVMIRText returned error: %v", directErr)
	}
	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("backend.Emit returned error: %v", err)
	}
	want, readErr := os.ReadFile(result.Artifacts.LLVMIR)
	if readErr != nil {
		t.Fatalf("ReadFile(%q): %v", result.Artifacts.LLVMIR, readErr)
	}
	if string(got) != string(want) {
		t.Fatalf("direct IR and artifact IR differ\ndirect:\n%s\nartifact:\n%s", got, want)
	}
	if !strings.Contains(string(got), "define i32 @main()") || !strings.Contains(string(got), "@printf") {
		t.Fatalf("emitted IR missing expected main/printf body:\n%s", got)
	}
}

func TestEmitLLVMIRTextUsesNativeMIRPayloadWhenCovered(t *testing.T) {
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    println(1)
}
`)

	withNativeMIRPayloadEmitter(t, func(entry Entry, target string) ([]byte, bool, []error, error) {
		if entry.MIR == nil {
			t.Fatal("native-owned route did not receive MIR payload")
		}
		if entry.PackageName != "main" || entry.SourcePath != req.Entry.SourcePath || target != "wasm32-unknown-unknown" {
			t.Fatalf("native-owned route metadata = (%q, %q, %q), want request metadata", entry.PackageName, entry.SourcePath, target)
		}
		return []byte("; native mir payload llvm ir\n"), true, []error{errors.New("native warning")}, nil
	})

	got, warnings, err := EmitLLVMIRText(req.Entry, "wasm32-unknown-unknown", nil)
	if err != nil {
		t.Fatalf("EmitLLVMIRText returned error: %v", err)
	}
	if string(got) != "; native mir payload llvm ir\n" {
		t.Fatalf("EmitLLVMIRText did not use native MIR payload output: %q", got)
	}
	if len(warnings) != 1 || warnings[0].Error() != "native warning" {
		t.Fatalf("warnings = %#v, want native warning", warnings)
	}
}

func TestEmitLLVMIRTextFallsBackWhenNativeMIRPayloadDeclines(t *testing.T) {
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    println(1)
}
`)

	withNativeMIRPayloadEmitter(t, func(Entry, string) ([]byte, bool, []error, error) {
		return nil, false, []error{errors.New("declined")}, nil
	})

	got, _, err := EmitLLVMIRText(req.Entry, "", nil)
	if err == nil {
		t.Fatal("EmitLLVMIRText should return an error when native declines and Go MIR emitter fallback is removed")
	}
	if !strings.Contains(string(got), "LLVM000") {
		t.Fatalf("skeleton should contain LLVM000 diagnostic, got: %s", got)
	}
	if !strings.Contains(string(got), "mir-direct") {
		t.Fatalf("skeleton should mention route mir-direct: %s", got)
	}
}

func withNativeMIRPayloadEmitter(t *testing.T, fn func(Entry, string) ([]byte, bool, []error, error)) {
	t.Helper()
	nativeMIRPayloadEmitterTestMu.Lock()
	oldTry := tryNativeOwnedMIRPayloadLLVMIRText
	tryNativeOwnedMIRPayloadLLVMIRText = fn
	t.Cleanup(func() {
		tryNativeOwnedMIRPayloadLLVMIRText = oldTry
		nativeMIRPayloadEmitterTestMu.Unlock()
	})
}

func TestEmitPrebuiltLLVMIRBuildsArtifactsFromProvidedIR(t *testing.T) {
	t.Parallel()

	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	root := t.TempDir()
	req := Request{
		Layout: Layout{
			Root:    root,
			Profile: "debug",
		},
		Emit:       EmitBinary,
		BinaryName: "app",
	}
	warnings := []error{os.ErrExist}
	result, err := backend.emitPrebuiltIR(context.Background(), req, []byte("source_filename = \"fake\"\ndefine i32 @main() { ret i32 0 }\n"), warnings)
	if err != nil {
		t.Fatalf("emitPrebuiltIR returned error: %v", err)
	}
	if result == nil {
		t.Fatal("emitPrebuiltIR returned nil result")
	}
	got, readErr := os.ReadFile(result.Artifacts.LLVMIR)
	if readErr != nil {
		t.Fatalf("ReadFile(%q): %v", result.Artifacts.LLVMIR, readErr)
	}
	if want := "source_filename = \"fake\"\ndefine i32 @main() { ret i32 0 }\n"; string(got) != want {
		t.Fatalf("llvm ir = %q, want %q", got, want)
	}
	if len(result.Warnings) != 1 || result.Warnings[0] != os.ErrExist {
		t.Fatalf("warnings = %#v, want propagated warnings", result.Warnings)
	}
	if len(tc.irCompiles) != 1 {
		t.Fatalf("ir compile count = %d, want 1", len(tc.irCompiles))
	}
	if len(tc.cCompiles) != 1 {
		t.Fatalf("runtime compile count = %d, want 1", len(tc.cCompiles))
	}
	if len(tc.links) != 1 {
		t.Fatalf("link count = %d, want 1", len(tc.links))
	}
}

func TestEmitLLVMIRTextPrefersNativeOwnedFastPathWhenCovered(t *testing.T) {
	req := newBackendRequest(t, EmitLLVMIR, `fn pick(flag: Bool) -> Int {
    if flag {
        42
    } else {
        0
    }
}

fn main() {
    let mut i = 0
    let mut sum = 0
    for i < 3 {
        sum = sum + pick(i == 2)
        i = i + 1
    }
    println(sum)
}
`)
	want := []byte("; native primitive-slice mir payload llvm ir\n")
	warnings := []error{errors.New("native primitive-slice warning")}

	withNativeMIRPayloadEmitter(t, func(entry Entry, target string) ([]byte, bool, []error, error) {
		if entry.MIR == nil {
			t.Fatal("native-owned route did not receive MIR payload")
		}
		return want, true, warnings, nil
	})

	got, gotWarnings, err := EmitLLVMIRText(req.Entry, "", nil)
	if err != nil {
		t.Fatalf("EmitLLVMIRText returned error: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("EmitLLVMIRText did not use native fast path output\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if len(gotWarnings) != len(warnings) {
		t.Fatalf("warning count = %d, want %d", len(gotWarnings), len(warnings))
	}
}

func TestEmitLLVMIRTextPrefersNativeOwnedFastPathForStructFieldAssign(t *testing.T) {
	req := newBackendRequest(t, EmitLLVMIR, `struct Pair { left: Int, right: Int }

fn main() {
    let mut pair = Pair { left: 1, right: 2 }
    pair.left = 3
    println(pair.left)
}
`)
	want := []byte("; native struct-field mir payload llvm ir\n")
	warnings := []error{errors.New("native struct-field warning")}

	withNativeMIRPayloadEmitter(t, func(entry Entry, target string) ([]byte, bool, []error, error) {
		if entry.MIR == nil {
			t.Fatal("native-owned route did not receive MIR payload")
		}
		return want, true, warnings, nil
	})

	got, gotWarnings, err := EmitLLVMIRText(req.Entry, "", nil)
	if err != nil {
		t.Fatalf("EmitLLVMIRText returned error: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("EmitLLVMIRText did not use native struct-field fast path output\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if len(gotWarnings) != len(warnings) {
		t.Fatalf("warning count = %d, want %d", len(gotWarnings), len(warnings))
	}
}

func TestEmitLLVMIRTextPrefersNativeOwnedFastPathForListIndex(t *testing.T) {
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    let xs = [1, 2]
    println(xs[0])
}
`)
	want := []byte("; native list-index mir payload llvm ir\n")
	warnings := []error{errors.New("native list-index warning")}

	withNativeMIRPayloadEmitter(t, func(entry Entry, target string) ([]byte, bool, []error, error) {
		if entry.MIR == nil {
			t.Fatal("native-owned route did not receive MIR payload")
		}
		return want, true, warnings, nil
	})

	got, gotWarnings, err := EmitLLVMIRText(req.Entry, "", nil)
	if err != nil {
		t.Fatalf("EmitLLVMIRText returned error: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("EmitLLVMIRText did not use native list fast path output\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if len(gotWarnings) != len(warnings) {
		t.Fatalf("warning count = %d, want %d", len(gotWarnings), len(warnings))
	}
}

func TestLLVMBackendEmitBinaryPrefersNativeOwnedFastPathWhenCovered(t *testing.T) {
	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitBinary, `fn pick(flag: Bool) -> Int {
    if flag {
        42
    } else {
        0
    }
}

fn main() {
    let mut i = 0
    let mut sum = 0
    for i < 3 {
        sum = sum + pick(i == 2)
        i = i + 1
    }
    println(sum)
}
`)
	want := []byte("; native binary primitive-slice mir payload llvm ir\n")
	warnings := []error{errors.New("native binary primitive-slice warning")}

	withNativeMIRPayloadEmitter(t, func(entry Entry, target string) ([]byte, bool, []error, error) {
		if entry.MIR == nil {
			t.Fatal("native-owned route did not receive MIR payload")
		}
		return want, true, warnings, nil
	})

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	got, readErr := os.ReadFile(result.Artifacts.LLVMIR)
	if readErr != nil {
		t.Fatalf("ReadFile(%q): %v", result.Artifacts.LLVMIR, readErr)
	}
	if string(got) != string(want) {
		t.Fatalf("Emit binary did not use native fast path output\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if len(result.Warnings) != len(warnings) {
		t.Fatalf("warning count = %d, want %d", len(result.Warnings), len(warnings))
	}
}

func TestLLVMBackendEmitBinaryPrefersNativeOwnedFastPathForStructFieldAssign(t *testing.T) {
	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitBinary, `struct Pair { left: Int, right: Int }

fn main() {
    let mut pair = Pair { left: 1, right: 2 }
    pair.left = 3
    println(pair.left)
}
`)
	want := []byte("; native binary struct-field mir payload llvm ir\n")
	warnings := []error{errors.New("native binary struct-field warning")}

	withNativeMIRPayloadEmitter(t, func(entry Entry, target string) ([]byte, bool, []error, error) {
		if entry.MIR == nil {
			t.Fatal("native-owned route did not receive MIR payload")
		}
		return want, true, warnings, nil
	})

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	got, readErr := os.ReadFile(result.Artifacts.LLVMIR)
	if readErr != nil {
		t.Fatalf("ReadFile(%q): %v", result.Artifacts.LLVMIR, readErr)
	}
	if string(got) != string(want) {
		t.Fatalf("Emit binary did not use native struct-field fast path output\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if len(result.Warnings) != len(warnings) {
		t.Fatalf("warning count = %d, want %d", len(result.Warnings), len(warnings))
	}
}

func TestLLVMBackendEmitBinaryPrefersNativeOwnedFastPathForListIndex(t *testing.T) {
	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let xs = [1, 2]
    println(xs[0])
}
`)
	want := []byte("; native binary list-index mir payload llvm ir\n")
	warnings := []error{errors.New("native binary list-index warning")}

	withNativeMIRPayloadEmitter(t, func(entry Entry, target string) ([]byte, bool, []error, error) {
		if entry.MIR == nil {
			t.Fatal("native-owned route did not receive MIR payload")
		}
		return want, true, warnings, nil
	})

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	got, readErr := os.ReadFile(result.Artifacts.LLVMIR)
	if readErr != nil {
		t.Fatalf("ReadFile(%q): %v", result.Artifacts.LLVMIR, readErr)
	}
	if string(got) != string(want) {
		t.Fatalf("Emit binary did not use native list fast path output\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if len(result.Warnings) != len(warnings) {
		t.Fatalf("warning count = %d, want %d", len(result.Warnings), len(warnings))
	}
}

func TestUseNativeOwnedLLVMIRDefaultsEnabled(t *testing.T) {
	t.Parallel()

	if !useNativeOwnedLLVMIR(nil, EmitLLVMIR) {
		t.Fatal("useNativeOwnedLLVMIR(nil, EmitLLVMIR) = false, want true")
	}
	if !useNativeOwnedLLVMIR(nil, EmitBinary) {
		t.Fatal("useNativeOwnedLLVMIR(nil, EmitBinary) = false, want true")
	}
	if !useNativeOwnedLLVMIR(nil, EmitObject) {
		t.Fatal("useNativeOwnedLLVMIR(nil, EmitObject) = false, want true")
	}
}

func TestUseNativeOwnedLLVMIRFeatureOverrides(t *testing.T) {
	t.Parallel()

	if useNativeOwnedLLVMIR([]string{"mir-backend"}, EmitLLVMIR) {
		t.Fatal("useNativeOwnedLLVMIR(mir-backend, EmitLLVMIR) = true, want false")
	}
	if useNativeOwnedLLVMIR([]string{"mir-backend"}, EmitObject) {
		t.Fatal("useNativeOwnedLLVMIR(mir-backend, EmitObject) = true, want false")
	}
}

// TestLLVMBackendRefusesNilIR confirms the dispatcher's IR-only
// contract: without req.Entry.IR the backend must reject the request
// rather than silently fall through to any AST-based path (no such
// path exists any more).
func TestLLVMBackendRefusesNilIR(t *testing.T) {
	t.Parallel()

	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitLLVMIR, `fn main() { println(1) }`)
	req.Entry.IR = nil
	// File is still populated; if the dispatcher secretly consulted it
	// the test would pass by accident — we want a hard reject instead.
	_, err := backend.Emit(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when IR is nil, got none")
	}
	if !strings.Contains(err.Error(), "missing lowered IR entry") {
		t.Fatalf("expected IR-missing diagnostic, got: %v", err)
	}
}

func TestLLVMBackendUnsupportedSkeletonIncludesDispatchDebug(t *testing.T) {
	t.Parallel()

	backend := LLVMBackend{toolchain: &fakeLLVMToolchain{}}
	req := newBackendRequest(t, EmitLLVMIR, `use go "strings" as strings {
    fn ToUpper(s: String) -> String
}

fn main() {
    println(1)
}
`)

	result, err := backend.Emit(context.Background(), req)
	if !errors.Is(err, ErrLLVMNotImplemented) {
		t.Fatalf("Emit error = %v, want ErrLLVMNotImplemented", err)
	}
	if result == nil {
		t.Fatal("Emit result is nil")
	}
	gotBytes, readErr := os.ReadFile(result.Artifacts.LLVMIR)
	if readErr != nil {
		t.Fatalf("ReadFile(%q): %v", result.Artifacts.LLVMIR, readErr)
	}
	got := string(gotBytes)
	for _, want := range []string{
		"Osty LLVM backend skeleton",
		"LLVM001 foreign-ffi",
		"backend-route: unsupported-preflight",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("unsupported skeleton missing %q:\n%s", want, got)
		}
	}
	if warningContaining(result.Warnings, "backend-route: unsupported-preflight") == nil {
		t.Fatalf("warnings = %v, want backend route detail", result.Warnings)
	}
}

func TestLLVMBackendDispatchTraceReportsSelectedRoute(t *testing.T) {
	backend := LLVMBackend{toolchain: &fakeLLVMToolchain{}}
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    let s = "abc"
    println(s.len())
}
`)
	req.Features = []string{"mir-backend"}
	withNativeMIRPayloadEmitter(t, func(Entry, string) ([]byte, bool, []error, error) {
		return []byte("; mir-direct covered by test stub\n"), true, nil, nil
	})

	result, emitErr, trace := captureLLVMBackendTrace(t, backend, req)
	if emitErr != nil {
		t.Fatalf("Emit returned error: %v", emitErr)
	}
	if result == nil {
		t.Fatal("Emit returned nil result")
	}
	for _, want := range []string{
		"backend trace: llvm mir-direct emit",
		"backend trace: llvm mir-direct succeeded",
	} {
		if !strings.Contains(trace, want) {
			t.Fatalf("dispatch trace missing %q:\n%s", want, trace)
		}
	}
	if strings.Contains(trace, "mir-direct unsupported") {
		t.Fatalf("dispatch trace unexpectedly reported unsupported route:\n%s", trace)
	}
}

// TestLLVMBackendMissingMIRDoesNotRetryLegacyIRBridge locks the MIR-full
// coverage contract at the dispatcher boundary. A malformed Entry with IR but
// no MIR must surface as an unsupported MIR skeleton rather than silently
// retrying GenerateModule on the legacy HIR bridge.
func TestLLVMBackendMissingMIRDoesNotRetryLegacyIRBridge(t *testing.T) {
	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    println(1)
}
`)
	req.Features = []string{"mir-backend"}
	req.Entry.MIR = nil

	result, emitErr, trace := captureLLVMBackendTrace(t, backend, req)
	if !errors.Is(emitErr, ErrLLVMNotImplemented) {
		t.Fatalf("Emit error = %v, want ErrLLVMNotImplemented", emitErr)
	}
	if len(tc.irCompiles) != 0 || len(tc.cCompiles) != 0 || len(tc.links) != 0 {
		t.Fatalf("toolchain should not run for LLVMIR skeleton emit: %+v %+v %+v", tc.irCompiles, tc.cCompiles, tc.links)
	}
	if result == nil || result.Artifacts.LLVMIR == "" {
		t.Fatalf("result missing LLVMIR skeleton artifact: %+v", result)
	}
	for _, want := range []string{
		"backend trace: llvm native-owned skipped",
		"backend trace: llvm mir-direct emit",
		"backend trace: llvm mir-direct unsupported",
	} {
		if !strings.Contains(trace, want) {
			t.Fatalf("dispatch trace missing %q:\n%s", want, trace)
		}
	}
	if strings.Contains(trace, "legacy-ir-bridge") {
		t.Fatalf("missing-MIR entry unexpectedly used legacy bridge route:\n%s", trace)
	}
	irBytes, readErr := os.ReadFile(result.Artifacts.LLVMIR)
	if readErr != nil {
		t.Fatalf("ReadFile(%q): %v", result.Artifacts.LLVMIR, readErr)
	}
	ir := string(irBytes)
	if strings.Contains(ir, "define i32 @main") {
		t.Fatalf("missing MIR unexpectedly retried legacy IR bridge:\n%s", ir)
	}
	for _, want := range []string{
		"nil MIR module",
		"backend-route: mir-direct",
	} {
		if !strings.Contains(ir, want) {
			t.Fatalf("skeleton missing %q:\n%s", want, ir)
		}
	}
}

func TestLLVMBackendDocsMentionDispatchRoutes(t *testing.T) {
	root := repoRoot(t)
	docPath := filepath.Join(root, "docs", "mir_design.md")
	doc := readTextFile(t, docPath)
	backendDoc := readTextFile(t, filepath.Join(root, "internal", "backend", "doc.go"))

	for _, want := range []string{
		"## Backend dispatch trace",
		"## MIR-direct coverage ledger",
		"Stage 5 checklist",
		"Removal ledger",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("docs/mir_design.md missing backend dispatch sync marker %q", want)
		}
	}

	sourceRoutes := collectLLVMDispatchRoutesFromSource(t, filepath.Join(root, "internal", "backend", "llvm.go"))
	docRoutes := map[string]bool{}
	for _, line := range strings.Split(doc, "\n") {
		cells := splitMarkdownTableRow(line)
		if len(cells) == 0 || !strings.HasPrefix(cells[0], "`") || !strings.HasSuffix(cells[0], "`") {
			continue
		}
		route := strings.Trim(cells[0], "`")
		if strings.Contains(route, "-") {
			docRoutes[route] = true
		}
	}

	routeGuards := map[string][]string{
		string(llvmDispatchUnsupportedPreflight): {
			"UnsupportedDiagnosticForModule",
			"TestLLVMBackendUnsupportedSkeletonIncludesDispatchDebug",
		},
		string(llvmDispatchNativeOwned): {
			"nativellvmgen.TryMIR",
			"TestEmitLLVMIRTextPrefersNativeOwnedFastPathWhenCovered",
		},
		string(llvmDispatchMIRDirect): {
			"nativellvmgen.TryMIR",
			"TestLLVMBackendDispatchTraceReportsSelectedRoute",
			"TestLLVMBackendMissingMIRDoesNotRetryLegacyIRBridge",
			"TestLLVMBackendEmitLLVMIRMIRBackendStringIntrinsics",
			"TestNativeToolchainMergedMIRPipelineIsClean",
		},
	}
	for _, route := range sourceRoutes {
		guards, ok := routeGuards[route]
		if !ok {
			t.Fatalf("dispatch route %q from internal/backend/llvm.go needs doc-sync expectations", route)
		}
		if !docRoutes[route] {
			t.Fatalf("docs/mir_design.md route table missing dispatch route %q from internal/backend/llvm.go", route)
		}
		row := markdownTableRowForFirstCell(t, doc, "`"+route+"`")
		for _, want := range guards {
			if !strings.Contains(row, want) {
				t.Fatalf("docs/mir_design.md route row %q missing %q:\n%s", route, want, row)
			}
		}
		if !strings.Contains(backendDoc, route) {
			t.Fatalf("internal/backend/doc.go missing dispatch route %q", route)
		}
	}
	for route := range docRoutes {
		if !containsString(sourceRoutes, route) {
			t.Fatalf("docs/mir_design.md documents stale dispatch route %q not found in internal/backend/llvm.go", route)
		}
	}

	testFuncs := collectGoFunctionNames(t,
		filepath.Join(root, "internal", "backend", "llvm_test.go"),
	)
	for _, name := range []string{
		"TestLLVMBackendUnsupportedSkeletonIncludesDispatchDebug",
		"TestEmitLLVMIRTextPrefersNativeOwnedFastPathWhenCovered",
		"TestLLVMBackendDispatchTraceReportsSelectedRoute",
		"TestLLVMBackendMissingMIRDoesNotRetryLegacyIRBridge",
		"TestLLVMBackendEmitLLVMIRMIRBackendStringIntrinsics",
		"TestLLVMBackendDocsMentionDispatchRoutes",
	} {
		if !functionNameOrPrefixExists(testFuncs, name) {
			t.Fatalf("docs/mir_design.md names guard %q but no matching test exists", name)
		}
	}

	backendFuncs := collectGoFunctionNames(t, filepath.Join(root, "internal", "backend", "llvm.go"))
	_ = collectGoFunctionNames(t, filepath.Join(root, "internal", "llvmabi", "api.go"))
	ledgerRows := []struct {
		gate    string
		anchors map[string]map[string]bool
	}{
		{
			gate: "Backend route",
			anchors: map[string]map[string]bool{
				"backend": {"generateLLVMIR": true, "emitLLVMFallback": true},
			},
		},
		{
			gate: "Module / function envelope",
			anchors: map[string]map[string]bool{
				"mir": {"checkSupported": true, "checkFunctionSupported": true},
			},
		},
		{
			gate: "Type surface",
			anchors: map[string]map[string]bool{
				"mir": {"typeSupported": true, "allowUnusedErrLocal": true},
			},
		},
		{
			gate: "Place / projection surface",
			anchors: map[string]map[string]bool{
				"mir": {"checkProjectionsSupported": true},
			},
		},
		{
			gate: "RValue surface",
			anchors: map[string]map[string]bool{
				"mir": {"checkRValueSupported": true},
			},
		},
		{
			gate: "Instruction / terminator surface",
			anchors: map[string]map[string]bool{
				"mir": {"checkInstrSupported": true, "checkTermSupported": true},
			},
		},
		{
			gate: "Intrinsic families",
			anchors: map[string]map[string]bool{
				"mir": {"isSupportedIntrinsic": true},
			},
		},
	}
	for _, row := range ledgerRows {
		line := markdownTableRowForFirstCell(t, doc, row.gate)
		for group, names := range row.anchors {
			for name := range names {
				if !strings.Contains(line, name) {
					t.Fatalf("coverage ledger row %q missing code anchor %q:\n%s", row.gate, name, line)
				}
				switch group {
				case "backend":
					if !backendFuncs[name] {
						t.Fatalf("coverage ledger anchor %q for row %q does not exist in internal/backend/llvm.go", name, row.gate)
					}
				case "mir":
				}
			}
		}
	}
}

func captureLLVMBackendTrace(t *testing.T, backend LLVMBackend, req Request) (*Result, error, string) {
	t.Helper()
	t.Setenv("OSTY_BACKEND_TRACE", "1")
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	os.Stderr = w
	defer func() {
		os.Stderr = oldStderr
	}()
	result, emitErr := backend.Emit(context.Background(), req)
	if closeErr := w.Close(); closeErr != nil {
		t.Fatalf("close stderr pipe: %v", closeErr)
	}
	os.Stderr = oldStderr
	traceBytes, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("read trace pipe: %v", readErr)
	}
	if closeErr := r.Close(); closeErr != nil {
		t.Fatalf("close trace pipe: %v", closeErr)
	}
	return result, emitErr, string(traceBytes)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	return root
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	return string(data)
}

func markdownTableRowForFirstCell(t *testing.T, doc, firstCell string) string {
	t.Helper()
	for _, line := range strings.Split(doc, "\n") {
		cells := splitMarkdownTableRow(line)
		if len(cells) > 0 && cells[0] == firstCell {
			return strings.TrimSpace(line)
		}
	}
	t.Fatalf("markdown table row with first cell %q not found", firstCell)
	return ""
}

func splitMarkdownTableRow(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
		return nil
	}
	line = strings.Trim(line, "|")
	parts := strings.Split(line, "|")
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		cells = append(cells, strings.TrimSpace(part))
	}
	return cells
}

func collectLLVMDispatchRoutesFromSource(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := stdparser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("ParseFile(%q): %v", path, err)
	}
	var routes []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*stdast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			valueSpec, ok := spec.(*stdast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range valueSpec.Names {
				if !strings.HasPrefix(name.Name, "llvmDispatch") || i >= len(valueSpec.Values) {
					continue
				}
				lit, ok := valueSpec.Values[i].(*stdast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				route, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote dispatch route %s in %q: %v", lit.Value, path, err)
				}
				routes = append(routes, route)
			}
		}
	}
	if len(routes) == 0 {
		t.Fatalf("no llvmDispatch route constants found in %q", path)
	}
	return routes
}

func collectGoFunctionNames(t *testing.T, paths ...string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	fset := token.NewFileSet()
	for _, path := range paths {
		file, err := stdparser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("ParseFile(%q): %v", path, err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*stdast.FuncDecl)
			if !ok || fn.Name == nil {
				continue
			}
			out[fn.Name.Name] = true
		}
	}
	return out
}

func functionNameOrPrefixExists(names map[string]bool, want string) bool {
	if strings.HasSuffix(want, "*") {
		prefix := strings.TrimSuffix(want, "*")
		for name := range names {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
		return false
	}
	return names[want]
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestLLVMBackendEmitLLVMIRMIRBackendStringIntrinsics — Stage 5
// originally tested the post-MIR/pre-LIR-Proto "MIR backend" path
// gated on the `mir-backend` feature flag. That feature flag is still
// observed by `toolchain/mir_generator.osty` (and other tests assert
// the `osty LLVM MIR backend` header), but for THIS specific test
// the routing dispatch in `internal/backend/llvm.go` now consumes
// `mir-backend` and dispatches to the LIR Proto subprocess (the
// `mir-direct` route in `llvmDispatchMIRDirect`). So the expected
// header for this test is `osty LIR Proto` rather than the old
// `osty LLVM MIR backend`. The runtime symbols
// (`@osty_rt_strings_Chars` / `Bytes` / `ByteLen`) still land in
// the emitted text via the LIR Proto path, so the substantive
// coverage is preserved. LIR Proto emits `define void @main()`
// because the runtime startup wrapper handles the C-ABI return —
// the original `define i32 @main()` + `ret i32 0` asserts now
// belong to the runtime wrapper, not the user `main`.
//
// Test name kept as-is because two other tests
// (`TestLLVMBackendDispatchTraceReportsSelectedRoute` route map +
// `TestLLVMBackendDocsMentionDispatchRoutes` docs guard) reference
// it by name in `llvmDispatchMIRDirect`. Renaming would require
// updating both references plus the docs/mir_design.md guard; a
// follow-up cleanup could rename in one sweep.
func TestLLVMBackendEmitLLVMIRMIRBackendStringIntrinsics(t *testing.T) {
	t.Parallel()
	requireRealLLVMEmission(t)

	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    let s = "abc"
    println(s.chars().len())
    println(s.bytes().len())
    println(s.len())
    if s.isEmpty() {
        println(1)
    } else {
        println(0)
    }
}
`)
	req.Features = []string{"mir-backend"}

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	irBytes, readErr := os.ReadFile(result.Artifacts.LLVMIR)
	if readErr != nil {
		t.Fatalf("ReadFile(%q): %v", result.Artifacts.LLVMIR, readErr)
	}
	ir := string(irBytes)
	if !strings.Contains(ir, "osty LIR Proto") {
		t.Fatalf("LIR Proto backend did not reach emitter (header missing):\n%s", ir)
	}
	for _, want := range []string{
		"@osty_rt_strings_Chars",
		"@osty_rt_strings_Bytes",
		"@osty_rt_strings_ByteLen",
		"define void @main()",
	} {
		if !strings.Contains(ir, want) {
			t.Fatalf("expected IR to contain %q, got:\n%s", want, ir)
		}
	}
}

// TestLLVMBackendBinaryMIRBackendStringCharsBytes — Stage 5 prep
// parity check. On binary emission (MIR-first by default),
// `.chars()` / `.bytes()` / `.len()` / `.isEmpty()` on a String must
// lower through the MIR-direct emitter (no silent fallback to the
// legacy AST bridge). The emitted IR header is the only stable tell of
// which path ran; this test locks in both that signal AND the linked
// binary's output so a regression on either side surfaces immediately.
func TestLLVMBackendBinaryMIRBackendStringCharsBytes(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let s = "abc"
    println(s.chars().len())
    println(s.bytes().len())
    println(s.len())
    if s.isEmpty() {
        println(1)
    } else {
        println(0)
    }
}
`)
	req.Features = []string{"mir-backend"}

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	irBytes, readErr := os.ReadFile(result.Artifacts.LLVMIR)
	if readErr != nil {
		t.Fatalf("ReadFile(%q): %v", result.Artifacts.LLVMIR, readErr)
	}
	ir := string(irBytes)
	if !strings.Contains(ir, "osty LLVM MIR backend") {
		t.Fatalf("mir-backend feature did not reach MIR emitter (header missing):\n%s", ir)
	}
	for _, want := range []string{
		"@osty_rt_strings_Chars",
		"@osty_rt_strings_Bytes",
		"@osty_rt_strings_ByteLen",
	} {
		if !strings.Contains(ir, want) {
			t.Fatalf("expected IR to contain %q, got:\n%s", want, ir)
		}
	}

	cmd := exec.Command(result.Artifacts.Binary)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "3\n3\n3\n0\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryRunsBundledRuntime(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

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

func TestLLVMBackendBinaryStdIoOutputFamily(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.io as io

fn main() {
    print(1)
    io.println(" apples")
    eprint(true)
    io.eprintln(" pears")
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
	if got, want := string(output), "1 apples\ntrue pears\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryStdIoReadLine(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.io as io

fn main() {
    let first = io.readLine()
    let second = io.readLine()
    println(first)
    println(second)
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	cmd.Stdin = strings.NewReader("alpha\nbeta\n")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "alpha\nbeta\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryStdCompressGzipRoundTrip(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.compress as compress

fn main() {
    let payload = Bytes.from("hello".bytes())
    let encoded = compress.gzip.encode(payload)
    match compress.gzip.decode(encoded) {
        Ok(decoded) -> println(decoded.len()),
        Err(_) -> println(0),
    }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "5\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryStdEnvGetReadsProcessEnv(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.env

fn main() {
    println(env.get("OSTY_ENV_GET_TEST") ?? "missing")
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	cmd.Env = append(os.Environ(), "OSTY_ENV_GET_TEST=configured")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "configured\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryStdEnvVarsReadsProcessEnv(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.env

fn main() {
    let vars = env.vars()
    println(vars.containsKey("OSTY_ENV_VARS_TEST"))
    println(vars.get("OSTY_ENV_VARS_TEST") ?? "missing")
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	cmd.Env = append(os.Environ(),
		"OSTY_ENV_VARS_TEST=configured",
		"OSTY_GC_THRESHOLD_BYTES=1",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "true\nconfigured\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryStdEnvRequireReadsProcessEnv(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.env

fn main() {
    match env.require("OSTY_ENV_REQUIRE_TEST") {
        Ok(value) -> println(value),
        Err(err) -> println(err.message()),
    }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	filteredEnv := []string{}
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "OSTY_ENV_REQUIRE_TEST=") {
			continue
		}
		filteredEnv = append(filteredEnv, entry)
	}
	cmd.Env = append(filteredEnv, "OSTY_ENV_REQUIRE_TEST=configured")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "configured\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryStdEnvRequireReportsMissingKey(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.env

fn main() {
    match env.require("OSTY_ENV_REQUIRE_TEST") {
        Ok(value) -> println(value),
        Err(err) -> {
            println(err.message())
            match err.source() {
                Some(inner) -> println(inner.message()),
                None -> println("none"),
            }
        },
    }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	filteredEnv := []string{}
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "OSTY_ENV_REQUIRE_TEST=") {
			continue
		}
		filteredEnv = append(filteredEnv, entry)
	}
	cmd.Env = filteredEnv
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "environment variable not set: OSTY_ENV_REQUIRE_TEST\nnone\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryStdEnvCurrentDirReadsProcessWorkingDir(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.env

fn main() {
    match env.currentDir() {
        Ok(dir) -> println(dir),
        Err(err) -> println(err.message()),
    }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	wantDir := filepath.Clean(t.TempDir())
	cmd := exec.Command(result.Artifacts.Binary)
	cmd.Dir = wantDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	got := string(output)
	want := wantDir + "\n"
	if got == want {
		return
	}
	if resolved, err := filepath.EvalSymlinks(wantDir); err == nil && got == filepath.Clean(resolved)+"\n" {
		return
	}
	t.Fatalf("binary stdout = %q, want %q (or symlink-resolved equivalent)", got, want)
}

func TestLLVMBackendBinaryStdEnvSetCurrentDirChangesProcessWorkingDir(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	targetDir := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(targetDir, 0o755); err != nil {
		t.Fatalf("Mkdir(%q): %v", targetDir, err)
	}

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, fmt.Sprintf(`use std.env

fn main() {
    match env.setCurrentDir(%q) {
        Ok(_) -> {
            match env.currentDir() {
                Ok(dir) -> println(dir),
                Err(err) -> println(err.message()),
            }
        },
        Err(err) -> println(err.message()),
    }
}
`, targetDir))

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	got := string(output)
	want := filepath.Clean(targetDir) + "\n"
	if got == want {
		return
	}
	if resolved, err := filepath.EvalSymlinks(targetDir); err == nil && got == filepath.Clean(resolved)+"\n" {
		return
	}
	t.Fatalf("binary stdout = %q, want %q (or symlink-resolved equivalent)", got, want)
}

func TestLLVMBackendBinaryStdEnvSetCurrentDirReportsMissingPath(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	missingDir := filepath.Join(t.TempDir(), "missing")
	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, fmt.Sprintf(`use std.env

fn main() {
    match env.setCurrentDir(%q) {
        Ok(_) -> println("ok"),
        Err(err) -> println(err.message()),
    }
}
`, missingDir))

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	got := string(output)
	if !strings.HasPrefix(got, "failed to set current directory: ") {
		t.Fatalf("binary stdout = %q, want prefix %q", got, "failed to set current directory: ")
	}
}

func TestLLVMBackendBinaryStdEnvSetMutatesProcessEnv(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.env

fn main() {
    match env.set("OSTY_ENV_SET_TEST", "configured") {
        Ok(_) -> println(env.get("OSTY_ENV_SET_TEST") ?? "missing"),
        Err(err) -> println(err.message()),
    }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	filteredEnv := []string{}
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "OSTY_ENV_SET_TEST=") {
			continue
		}
		filteredEnv = append(filteredEnv, entry)
	}
	cmd.Env = filteredEnv
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "configured\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryStdEnvUnsetMutatesProcessEnv(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.env

fn main() {
    match env.unset("OSTY_ENV_UNSET_TEST") {
        Ok(_) -> println(env.get("OSTY_ENV_UNSET_TEST") ?? "missing"),
        Err(err) -> println(err.message()),
    }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	filteredEnv := []string{}
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "OSTY_ENV_UNSET_TEST=") {
			continue
		}
		filteredEnv = append(filteredEnv, entry)
	}
	cmd.Env = append(filteredEnv, "OSTY_ENV_UNSET_TEST=configured")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "missing\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryTestingPropertyGeneratorSubset(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use std.testing as testing
use std.testing.gen as gen

fn main() {
    testing.property(
        "bool generator produces bools",
        gen.bool(),
        |b: Bool| b || !(b),
    )
    testing.property(
        "float generator stays in unit range",
        gen.float(),
        |f: Float64| f >= 0.0 && f < 1.0,
    )
    testing.property(
        "char generator stays printable ASCII",
        gen.char(),
        |c: Char| c.toInt() >= 32 && c.toInt() < 127,
    )
    testing.property(
        "byte generator stays byte-sized",
        gen.byte(),
        |b: Byte| b.toInt() >= 0 && b.toInt() < 256,
    )
    testing.property(
        "intRange stays inside bounds",
        gen.intRange(-1000, 1000),
        |n: Int| n >= -1000 && n < 1000,
    )
    testing.property(
        "ascii strings honor maxLen",
        gen.asciiString(12),
        |s: String| s.len() <= 12,
    )
    testing.property(
        "pair generator keeps both ranges",
        gen.pair(gen.intRange(-3, 3), gen.intRange(10, 20)),
        |(a, b): (Int, Int)| a >= -3 && a < 3 && b >= 10 && b < 20,
    )
    testing.property(
        "triple generator keeps all ranges",
        gen.triple(gen.intRange(0, 2), gen.intRange(3, 5), gen.intRange(6, 8)),
        |(a, b, c): (Int, Int, Int)| a >= 0 && a < 2 && b >= 3 && b < 5 && c >= 6 && c < 8,
    )
    testing.property(
        "list generator honors max length",
        gen.list(gen.intRange(0, 5), 4),
        |xs: List<Int>| xs.len() <= 4,
    )
    testing.property(
        "listOfSize generator honors exact length",
        gen.listOfSize(gen.bool(), 3),
        |xs: List<Bool>| xs.len() == 3,
    )
    testing.property(
        "option generator makes valid options",
        gen.option(gen.intRange(0, 5)),
        |x: Int?| x.isNone() || x.unwrap() >= 0,
    )
    testing.property(
        "result generator makes valid results",
        gen.result(gen.intRange(0, 5), gen.constant("err")),
        |r: Result<Int, String>| r.isOk() || r.isErr(),
    )
    testing.property(
        "map generator transforms samples",
        gen.map(gen.intRange(0, 5), |n: Int| n + 1),
        |n: Int| n >= 1 && n <= 5,
    )
    testing.property(
        "filter generator retries until predicate matches",
        gen.filter(gen.intRange(-8, 8), |n: Int| n >= 0),
        |n: Int| n >= 0,
    )
    testing.property(
        "oneOf chooses from the provided literal pool",
        gen.oneOf(["aa", "bbb", "cccc"]),
        |s: String| s == "aa" || s == "bbb" || s == "cccc",
    )
    testing.property(
        "oneOfGens chooses from generator pool",
        gen.oneOfGens([gen.constant(1), gen.intRange(2, 4)]),
        |n: Int| n == 1 || (n >= 2 && n < 4),
    )
    println("ok")
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
	if got, want := string(output), "ok\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinarySafepointsKeepManagedRootsAlive(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

struct Bucket {
    items: List<String>
}

fn touch() {}

fn localCount() -> Int {
    let parts = strings.Split("gc,llvm", ",")
    touch()
    parts.len()
}

fn bucketCount(bucket: Bucket) -> Int {
    touch()
    bucket.items.len()
}

fn main() {
    println(localCount())
    let bucket = Bucket { items: strings.Split("one,two", ",") }
    println(bucketCount(bucket))
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	cmd.Env = append(os.Environ(), "OSTY_GC_STRESS=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "2\n2\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryAutoCollectsOnPressure(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

fn touch() {}

fn localCount() -> Int {
    let parts = strings.Split("gc,llvm", ",")
    touch()
    parts.len()
}

fn main() {
    println(localCount())
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	cmd.Env = append(os.Environ(), "OSTY_GC_THRESHOLD_BYTES=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "2\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryKeepsMapKeysSortedAliveUnderGC(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn sortedCount(words: List<String>) -> Int {
    let mut index: Map<String, Int> = {:}
    for word in words.sorted() {
        if index.containsKey(word) {
            continue
        }
        index.insert(word, 1)
    }
    index.keys().sorted().len()
}

fn main() {
    let words = ["gamma", "alpha", "beta", "alpha", "gamma", "delta", "beta", "delta"]
    let mut total = 0
    let mut i = 0
    while i < 50 {
        total = total + sortedCount(words)
        i = i + 1
    }
    println(total)
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	cmd.Env = append(os.Environ(), "OSTY_GC_STRESS=1", "OSTY_GC_THRESHOLD_BYTES=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "200\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendIRElidesMapKeysSortedLenChain(t *testing.T) {
	// The optimizer elides `map.keys().sorted().len()` to `map.len()`
	// when `keys()` and `sorted()` stay as runtime intrinsics. With
	// always-on stdlib body injection those calls route through bodied
	// Osty sources, so the intrinsic-chain shape is no longer visible.
	t.Skip("needs rewrite for always-on stdlib body injection (optimizer bodied-path elision)")
	requireRealLLVMEmission(t)
	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitLLVMIR, `fn sortedCount(words: List<String>) -> Int {
    let mut index: Map<String, Int> = {:}
    for word in words {
        index.insert(word, 1)
    }
    index.keys().sorted().len()
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	out, err := os.ReadFile(result.Artifacts.LLVMIR)
	if err != nil {
		t.Fatalf("ReadFile(%q) failed: %v", result.Artifacts.LLVMIR, err)
	}
	got := string(out)
	if !strings.Contains(got, "call i64 @osty_rt_map_len(") {
		t.Fatalf("optimized llvm-ir missing map.len call:\n%s", got)
	}
	for _, unwanted := range []string{
		"call ptr @osty_rt_map_keys(",
		"call ptr @osty_rt_list_sorted_string(",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("optimized llvm-ir unexpectedly kept %q:\n%s", unwanted, got)
		}
	}
}

func TestLLVMBackendBinaryForInOverTemporaryManagedListSurvivesPressure(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

fn main() {
    for item in strings.Split("a,b", ",") {
        println(item)
    }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	cmd.Env = append(os.Environ(), "OSTY_GC_THRESHOLD_BYTES=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "a\nb\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryManagedTemporaryCallArgSurvivesPressure(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

fn count() -> Int {
    1
}

fn take(items: List<String>, n: Int) -> Int {
    items.len() + n
}

fn main() {
    println(take(strings.Split("a,b", ","), count()))
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	cmd.Env = append(os.Environ(), "OSTY_GC_THRESHOLD_BYTES=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "3\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryRunsBitwiseIntOps(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    println(~-43)
    println((1 << 5) | (1 << 3) | 2)
    println((255 >> 2) ^ 21)
    println(58 & 43)
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "42\n42\n42\n42\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryMutReceiverMethodWritesBackToCaller(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `struct Counter {
    value: Int,

    fn add(mut self, delta: Int) -> Int {
        self.value = self.value + delta
        self.value
    }

    fn get(self) -> Int {
        self.value
    }
}

fn main() {
    let mut counter = Counter { value: 1 }
    println(counter.add(2))
    println(counter.get())
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
	if got, want := string(output), "3\n3\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryCollectionsUseRuntimeContainers(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `struct Pair {
    left: Int
    right: Int
}

fn main() {
    let mut pairs: Map<Int, Pair> = {:}
    pairs.insert(1, Pair { left: 2, right: 3 })
    if pairs.containsKey(1) {
        println(1)
    } else {
        println(0)
    }
    let pair = pairs[1]
    println(pair.left + pair.right)

    let keys = pairs.keys().sorted()
    println(keys[0])

    let pairValues = pairs.values()
    let firstPair = pairValues[0]
    println(firstPair.left + firstPair.right)

    match pairs.remove(1) {
        Some(old) -> println(old.left + old.right),
        None -> println(0),
    }
    println(pairs.len())
    pairs.insert(1, Pair { left: 2, right: 3 })

    let mut values: List<Pair> = []
    values.push(Pair { left: 4, right: 6 })
    let value = values[0]
    println(value.left + value.right)

    let mut nums: List<Int> = [1]
    nums[0] = 9
    println(nums[0])

    let empty: List<Int> = []
    let mut seen = empty.toSet()
    seen.insert(7)
    seen.insert(7)
    println(seen.len())
    let ids = seen.toList()
    println(ids[0])

    seen.clear()
    println(seen.len())
    pairs.clear()
    println(pairs.len())
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
	if got, want := string(output), "1\n5\n1\n5\n5\n0\n10\n9\n1\n7\n0\n0\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryManagedAggregateContainersSurvivePressureGC(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `struct Bucket {
    ids: List<Int>
}

fn main() {
    let ids: List<Int> = [7]
    let mut buckets: Map<String, Bucket> = {:}
    buckets.insert("root", Bucket { ids: ids })
    let bucket = buckets["root"]

    let empty: List<Int> = []
    let mut seen = empty.toSet()
    seen.insert(7)

    println(bucket.ids[0])
    println(seen.len())
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	cmd.Env = append(os.Environ(), "OSTY_GC_THRESHOLD_BYTES=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "7\n1\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryExtendedListSortedAndToSet(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let words: List<String> = ["pear", "apple", "banana", "apple"]
    let wordSet = words.sorted().toSet()
    println(wordSet.len())
    println(wordSet.toList().sorted()[0])

    let values: List<Float> = [3.5, 1.5, 2.5, 1.5]
    let sortedValues = values.sorted()
    println(sortedValues[0])
    let uniqueValues = sortedValues.toSet()
    println(uniqueValues.len())

    let flags: List<Bool> = [true, false, true]
    let sortedFlags = flags.sorted()
    if sortedFlags[0] {
        println(1)
    } else {
        println(0)
    }
    let uniqueFlags = flags.toSet()
    println(uniqueFlags.len())
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "3\napple\n1.500000\n3\n0\n2\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryPtrBackedListToSetAndBoolPrint(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

fn main() {
    let item = strings.Split("a,b", ",")
    let items: List<List<String>> = [item, item]
    let seen = items.toSet()

    println(seen.len())
    println(seen.contains(item))
    println(seen.len() == 1)
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "1\ntrue\ntrue\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryGenericEnumVariantFromLetContext(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `enum Maybe<T> { Some(T), None }

fn main() {
    let value: Maybe<Int> = Maybe.Some(42)
    if let Maybe.Some(x) = value {
        println(x)
    } else {
        println(0)
    }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "42\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryGenericEnumVariantInferredFromPayload(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `enum Maybe<T> { Some(T), None }

fn main() {
    let value = Maybe.Some(42)
    if let Maybe.Some(x) = value {
        println(x)
    } else {
        println(0)
    }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "42\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryGenericEnumPayloadFreeVariantFromLetContext(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `enum Maybe<T> { Some(T), None }

fn main() {
    let value: Maybe<Int> = Maybe.None
    if let Maybe.None = value {
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
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "1\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryBuiltinResultFieldConstructors(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn main() {
    let ok: Result<Int, String> = Result.Ok(42)
    let err: Result<Int, String> = Result.Err("x")
    println(1)
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "1\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryBuiltinResultConstructorsTrackLocalContext(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `struct Holder {
    ok: Result<Int, String>
    flag: Result<Bool, String>
}

fn wrap(value: Int) -> Result<Int, String> {
    return Result.Ok(value)
}

fn consume(value: Result<Bool, String>) -> Int {
    1
}

fn main() {
    let ok: Result<Int, String> = Result.Ok(42)
    let flag: Result<Bool, String> = Result.Ok(true)
    let holder = Holder { ok: Result.Err("bad"), flag: Result.Ok(true) }
    let wrapped = wrap(7)
    println(consume(Result.Ok(true)))
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "1\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryResultUnitErrUsesReturnContext(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn fail() -> Result<(), String> {
    return Err("nope")
}

fn main() {
    match fail() {
        Ok(_) -> println(0),
        Err(msg) -> println(msg),
    }
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "nope\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

func TestLLVMBackendBinaryLetStructPatternDestructuring(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

struct Pair {
    first: Int
    second: Int
}

struct Bucket {
    pair: Pair
    items: List<String>
}

fn main() {
    let bucket @ Bucket {
        pair: Pair { first, second },
        items,
    } = Bucket {
        pair: Pair { first: 1, second: 2 },
        items: strings.Split("pear,apple", ","),
    }
    println(first)
    println(second)
    println(items.sorted()[0])
    println(bucket.pair.first)
}
`)

	result, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	output, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", result.Artifacts.Binary, err, output)
	}
	if got, want := string(output), "1\n2\napple\n1\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

// TestLLVMBackendBinaryRunsGenericIdentity covers the generic
// monomorphization path Phase 1 introduced, all the way through clang
// to an executable. The monomorphizer must produce `_Z2idIlEl`, clang
// must link and run it, and the process must print the forwarded
// value. Complements the IR-only smoke in
// `internal/llvmgen/ir_module_test.go::TestGenerateModuleGenericIdentityMonomorphized`.
func TestLLVMBackendBinaryRunsGenericIdentity(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `fn id<T>(x: T) -> T { x }

fn main() {
    println(id::<Int>(42))
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
	if got, want := string(output), "42\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

// TestLLVMBackendBinaryRunsGenericOwnerMethodTurbofish covers the Phase D
// handoff where IR method-local monomorphization rewrites `pick::<Int>` into
// a concrete method name, then MIR and LIR must keep the owner-qualified
// method symbol pointing at the same local definition instead of declaring a
// missing cross-module call.
func TestLLVMBackendBinaryRunsGenericOwnerMethodTurbofish(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `struct Box<T> {
    value: T,

    fn pick<U>(self, fallback: U) -> U {
        fallback
    }
}

fn main() {
    let b: Box<Int> = Box { value: 7 }
    println(b.pick::<Int>(42))
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
	if got, want := string(output), "42\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}

// TestLLVMBackendBinaryRunsInterfaceBoxingDispatch exercises the full
// Phase 6a-6e interface pipeline end-to-end: a struct's method set
// structurally satisfies an interface, the concrete value is boxed
// into a `%osty.iface` fat pointer at the `let` site, and the
// subsequent method call is lowered to a vtable indirect call that
// the linked binary actually executes. Complements the IR-only
// smokes `TestGenerateModuleInterfaceBoxingDispatch` and friends by
// confirming the emitted `insertvalue` / `extractvalue` / `load ptr`
// / indirect-call sequence survives clang and runs correctly.
func TestLLVMBackendBinaryRunsInterfaceBoxingDispatch(t *testing.T) {
	parallelClangBackendTest(t)
	requireRealLLVMEmission(t)

	backend := LLVMBackend{}
	req := newBackendRequest(t, EmitBinary, `interface Sized {
    fn size(self) -> Int
}

struct Vec {
    count: Int,

    fn size(self) -> Int {
        self.count
    }
}

fn main() {
    let v = Vec { count: 3 }
    let s: Sized = v
    println(s.size())
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
	if got, want := string(output), "3\n"; got != want {
		t.Fatalf("binary stdout = %q, want %q", got, want)
	}
}
