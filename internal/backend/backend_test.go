package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

func TestParseName(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Name
	}{
		{"go", NameGo},
		{"llvm", NameLLVM},
	} {
		got, err := ParseName(tc.in)
		if err != nil {
			t.Fatalf("ParseName(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if _, err := ParseName("wasm"); err == nil {
		t.Fatal("ParseName(wasm) succeeded; want error")
	}
}

func TestNewBackend(t *testing.T) {
	goBackend, err := New(NameGo)
	if err != nil {
		t.Fatalf("New(go): %v", err)
	}
	if goBackend.Name() != NameGo {
		t.Fatalf("New(go).Name() = %q, want %q", goBackend.Name(), NameGo)
	}
	llvmBackend, err := New(NameLLVM)
	if err != nil {
		t.Fatalf("New(llvm): %v", err)
	}
	if llvmBackend.Name() != NameLLVM {
		t.Fatalf("New(llvm).Name() = %q, want %q", llvmBackend.Name(), NameLLVM)
	}
	if _, err := New(Name("bad")); err == nil {
		t.Fatal("New(bad) succeeded; want error")
	}
}

func TestValidateEmit(t *testing.T) {
	valid := []struct {
		name Name
		mode EmitMode
	}{
		{NameGo, EmitGoSource},
		{NameGo, EmitBinary},
		{NameLLVM, EmitLLVMIR},
		{NameLLVM, EmitObject},
		{NameLLVM, EmitBinary},
	}
	for _, tc := range valid {
		if err := ValidateEmit(tc.name, tc.mode); err != nil {
			t.Fatalf("ValidateEmit(%q, %q): %v", tc.name, tc.mode, err)
		}
	}
	invalid := []struct {
		name Name
		mode EmitMode
	}{
		{NameGo, EmitLLVMIR},
		{NameGo, EmitObject},
		{NameLLVM, EmitGoSource},
		{Name("bad"), EmitBinary},
		{NameGo, EmitMode("bad")},
	}
	for _, tc := range invalid {
		if err := ValidateEmit(tc.name, tc.mode); err == nil {
			t.Fatalf("ValidateEmit(%q, %q) succeeded; want error", tc.name, tc.mode)
		}
	}
}

func TestLayoutPaths(t *testing.T) {
	root := filepath.Join("tmp", "project")
	l := Layout{Root: root, Profile: "debug"}

	if got, want := l.Key(), "debug"; got != want {
		t.Fatalf("Key() = %q, want %q", got, want)
	}
	if got, want := l.OutputRoot(), filepath.Join(root, ".osty", "out", "debug"); got != want {
		t.Fatalf("OutputRoot() = %q, want %q", got, want)
	}
	if got, want := l.OutputDir(NameGo), filepath.Join(root, ".osty", "out", "debug", "go"); got != want {
		t.Fatalf("OutputDir(go) = %q, want %q", got, want)
	}
	if got, want := l.CachePath(NameLLVM), filepath.Join(root, ".osty", "cache", "debug", "llvm.json"); got != want {
		t.Fatalf("CachePath(llvm) = %q, want %q", got, want)
	}
	if got, want := l.LegacyCachePath(), filepath.Join(root, ".osty", "cache", "debug.json"); got != want {
		t.Fatalf("LegacyCachePath() = %q, want %q", got, want)
	}
}

func TestLayoutTargetPaths(t *testing.T) {
	root := filepath.Join("tmp", "project")
	l := Layout{Root: root, Profile: "release", Target: "amd64-linux"}

	if got, want := l.Key(), "release-amd64-linux"; got != want {
		t.Fatalf("Key() = %q, want %q", got, want)
	}
	if got, want := l.OutputDir(NameLLVM), filepath.Join(root, ".osty", "out", "release-amd64-linux", "llvm"); got != want {
		t.Fatalf("OutputDir(llvm) = %q, want %q", got, want)
	}
	if got, want := l.CachePath(NameGo), filepath.Join(root, ".osty", "cache", "release-amd64-linux", "go.json"); got != want {
		t.Fatalf("CachePath(go) = %q, want %q", got, want)
	}
}

func TestArtifacts(t *testing.T) {
	root := filepath.Join("tmp", "project")
	l := Layout{Root: root, Profile: "debug"}

	goArtifacts := l.Artifacts(NameGo, "app")
	if got, want := goArtifacts.GoSource, filepath.Join(root, ".osty", "out", "debug", "go", "main.go"); got != want {
		t.Fatalf("GoSource = %q, want %q", got, want)
	}
	if goArtifacts.LLVMIR != "" || goArtifacts.Object != "" || goArtifacts.RuntimeDir != "" {
		t.Fatalf("Go artifacts populated LLVM fields: %+v", goArtifacts)
	}
	if got, want := goArtifacts.Binary, filepath.Join(root, ".osty", "out", "debug", "go", "app"); got != want {
		t.Fatalf("Go Binary = %q, want %q", got, want)
	}

	llvmArtifacts := l.Artifacts(NameLLVM, "app")
	if got, want := llvmArtifacts.LLVMIR, filepath.Join(root, ".osty", "out", "debug", "llvm", "main.ll"); got != want {
		t.Fatalf("LLVMIR = %q, want %q", got, want)
	}
	if got, want := llvmArtifacts.Object, filepath.Join(root, ".osty", "out", "debug", "llvm", "main.o"); got != want {
		t.Fatalf("Object = %q, want %q", got, want)
	}
	if got, want := llvmArtifacts.RuntimeDir, filepath.Join(root, ".osty", "out", "debug", "llvm", "runtime"); got != want {
		t.Fatalf("RuntimeDir = %q, want %q", got, want)
	}
	if llvmArtifacts.GoSource != "" {
		t.Fatalf("LLVM artifacts populated GoSource: %+v", llvmArtifacts)
	}
}

func TestGoBackendEmitWritesSource(t *testing.T) {
	src := []byte(`fn main() {
    println(42)
}
`)
	file, diags := parser.ParseDiagnostics(src)
	if len(diags) != 0 {
		t.Fatalf("parse diagnostics: %v", diags)
	}
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res)

	root := t.TempDir()
	req := Request{
		Layout: Layout{Root: root, Profile: "debug"},
		Emit:   EmitGoSource,
		Entry: Entry{
			PackageName: "main",
			SourcePath:  filepath.Join(root, "main.osty"),
			File:        file,
			Resolve:     res,
			Check:       chk,
		},
		Features: []string{"alpha"},
	}
	result, err := GoBackend{}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	wantPath := filepath.Join(root, ".osty", "out", "debug", "go", "main.go")
	if result.Artifacts.GoSource != wantPath {
		t.Fatalf("GoSource = %q, want %q", result.Artifacts.GoSource, wantPath)
	}
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read emitted source: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"//go:build feat_alpha",
		"package main",
		"// Osty: " + filepath.Join(root, "main.osty") + ":1:1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("emitted source missing %q:\n%s", want, text)
		}
	}
}

func TestLLVMBackendEmitWritesIR(t *testing.T) {
	src := []byte(`fn main() {
    println(40 + 2)
}
`)
	file, diags := parser.ParseDiagnostics(src)
	if len(diags) != 0 {
		t.Fatalf("parse diagnostics: %v", diags)
	}
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res)

	root := t.TempDir()
	req := Request{
		Layout: Layout{Root: root, Profile: "debug", Target: "x86_64-unknown-linux-gnu"},
		Emit:   EmitLLVMIR,
		Entry: Entry{
			PackageName: "main",
			SourcePath:  filepath.Join(root, "main.osty"),
			File:        file,
			Resolve:     res,
			Check:       chk,
		},
	}
	result, err := LLVMBackend{}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if result == nil {
		t.Fatal("Emit returned nil result")
	}
	if result.Backend != NameLLVM || result.Emit != EmitLLVMIR {
		t.Fatalf("unexpected result identity: %+v", result)
	}
	wantIR := filepath.Join(root, ".osty", "out", "debug-x86_64-unknown-linux-gnu", "llvm", "main.ll")
	if result.Artifacts.LLVMIR != wantIR {
		t.Fatalf("LLVMIR = %q, want %q", result.Artifacts.LLVMIR, wantIR)
	}
	data, err := os.ReadFile(wantIR)
	if err != nil {
		t.Fatalf("read LLVM IR: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"; Code generated by osty LLVM backend. DO NOT EDIT.",
		"; Osty: " + filepath.ToSlash(filepath.Join(root, "main.osty")),
		"target triple = \"x86_64-unknown-linux-gnu\"",
		"%t0 = add i64 40, 2",
		"call i32 (ptr, ...) @printf(ptr @.fmt_i64, i64 %t0)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("LLVM IR missing %q:\n%s", want, text)
		}
	}
	if info, err := os.Stat(result.Artifacts.RuntimeDir); err != nil || !info.IsDir() {
		t.Fatalf("runtime dir missing: info=%v err=%v", info, err)
	}
}

func TestLLVMBackendEmitWritesObject(t *testing.T) {
	src := []byte(`fn main() {
    println(40 + 2)
}
`)
	file, diags := parser.ParseDiagnostics(src)
	if len(diags) != 0 {
		t.Fatalf("parse diagnostics: %v", diags)
	}
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res)

	root := t.TempDir()
	fake := &fakeLLVMToolchain{}
	result, err := (LLVMBackend{toolchain: fake}).Emit(context.Background(), Request{
		Layout: Layout{Root: root, Profile: "debug", Target: "x86_64-unknown-linux-gnu"},
		Emit:   EmitObject,
		Entry: Entry{
			PackageName: "main",
			SourcePath:  filepath.Join(root, "main.osty"),
			File:        file,
			Resolve:     res,
			Check:       chk,
		},
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if result == nil {
		t.Fatal("Emit returned nil result")
	}
	if result.Emit != EmitObject {
		t.Fatalf("Emit mode = %q, want %q", result.Emit, EmitObject)
	}
	if _, err := os.Stat(result.Artifacts.LLVMIR); err != nil {
		t.Fatalf("LLVM IR missing: %v", err)
	}
	if _, err := os.Stat(result.Artifacts.Object); err != nil {
		t.Fatalf("object artifact missing: %v", err)
	}
	if len(fake.compileCalls) != 1 {
		t.Fatalf("compile calls = %+v, want one", fake.compileCalls)
	}
	if fake.compileCalls[0].target != "x86_64-unknown-linux-gnu" {
		t.Fatalf("compile target = %q", fake.compileCalls[0].target)
	}
	if fake.compileCalls[0].irPath != result.Artifacts.LLVMIR || fake.compileCalls[0].objectPath != result.Artifacts.Object {
		t.Fatalf("compile call paths = %+v, artifacts = %+v", fake.compileCalls[0], result.Artifacts)
	}
	if len(fake.linkCalls) != 0 {
		t.Fatalf("link calls = %+v, want none", fake.linkCalls)
	}
}

func TestLLVMBackendEmitWritesBinary(t *testing.T) {
	src := []byte(`fn main() {
    println(40 + 2)
}
`)
	file, diags := parser.ParseDiagnostics(src)
	if len(diags) != 0 {
		t.Fatalf("parse diagnostics: %v", diags)
	}
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res)

	root := t.TempDir()
	fake := &fakeLLVMToolchain{}
	result, err := (LLVMBackend{toolchain: fake}).Emit(context.Background(), Request{
		Layout:     Layout{Root: root, Profile: "debug"},
		Emit:       EmitBinary,
		BinaryName: "app",
		Entry: Entry{
			PackageName: "main",
			SourcePath:  filepath.Join(root, "main.osty"),
			File:        file,
			Resolve:     res,
			Check:       chk,
		},
	})
	if err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if result == nil {
		t.Fatal("Emit returned nil result")
	}
	for name, path := range map[string]string{
		"llvm ir": result.Artifacts.LLVMIR,
		"object":  result.Artifacts.Object,
		"binary":  result.Artifacts.Binary,
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s artifact missing at %s: %v", name, path, err)
		}
	}
	if len(fake.compileCalls) != 1 {
		t.Fatalf("compile calls = %+v, want one", fake.compileCalls)
	}
	if len(fake.linkCalls) != 1 {
		t.Fatalf("link calls = %+v, want one", fake.linkCalls)
	}
	if fake.linkCalls[0].objectPath != result.Artifacts.Object || fake.linkCalls[0].binaryPath != result.Artifacts.Binary {
		t.Fatalf("link call paths = %+v, artifacts = %+v", fake.linkCalls[0], result.Artifacts)
	}
}

func TestLLVMBackendUnsupportedFallsBackToSkeleton(t *testing.T) {
	src := []byte(`fn main() {
    println("nope")
}
`)
	file, diags := parser.ParseDiagnostics(src)
	if len(diags) != 0 {
		t.Fatalf("parse diagnostics: %v", diags)
	}
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res)

	root := t.TempDir()
	result, err := LLVMBackend{}.Emit(context.Background(), Request{
		Layout: Layout{Root: root, Profile: "debug"},
		Emit:   EmitLLVMIR,
		Entry: Entry{
			PackageName: "main",
			SourcePath:  filepath.Join(root, "main.osty"),
			File:        file,
			Resolve:     res,
			Check:       chk,
		},
	})
	if !errors.Is(err, ErrLLVMNotImplemented) {
		t.Fatalf("Emit error = %v, want ErrLLVMNotImplemented", err)
	}
	if result == nil {
		t.Fatal("Emit returned nil result")
	}
	data, err := os.ReadFile(result.Artifacts.LLVMIR)
	if err != nil {
		t.Fatalf("read LLVM skeleton: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"; Osty LLVM backend skeleton",
		"; unsupported: LLVM013 expression",
		"source_filename =",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("LLVM skeleton missing %q:\n%s", want, text)
		}
	}
}

func TestLLVMBackendGoFFIFallsBackWithSelfHostedDiagnostic(t *testing.T) {
	src := []byte(`use go "strings" as strings {
    fn Join(elems: List<String>, sep: String) -> String
}

fn main() {
    println(42)
}
`)
	file, diags := parser.ParseDiagnostics(src)
	if len(diags) != 0 {
		t.Fatalf("parse diagnostics: %v", diags)
	}
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res)

	root := t.TempDir()
	result, err := LLVMBackend{}.Emit(context.Background(), Request{
		Layout: Layout{Root: root, Profile: "debug"},
		Emit:   EmitLLVMIR,
		Entry: Entry{
			PackageName: "main",
			SourcePath:  filepath.Join(root, "ffi.osty"),
			File:        file,
			Resolve:     res,
			Check:       chk,
		},
	})
	if !errors.Is(err, ErrLLVMNotImplemented) {
		t.Fatalf("Emit error = %v, want ErrLLVMNotImplemented", err)
	}
	if result == nil {
		t.Fatal("Emit returned nil result")
	}
	data, err := os.ReadFile(result.Artifacts.LLVMIR)
	if err != nil {
		t.Fatalf("read LLVM skeleton: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		"; unsupported: LLVM001 go-only",
		`use go "strings" is only supported by the Go backend`,
		"hint: use --backend=go",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("LLVM skeleton missing %q:\n%s", want, text)
		}
	}
}

func TestLLVMBackendRejectsGoEmit(t *testing.T) {
	_, err := LLVMBackend{}.Emit(context.Background(), Request{
		Layout: Layout{Root: t.TempDir(), Profile: "debug"},
		Emit:   EmitGoSource,
	})
	if err == nil {
		t.Fatal("Emit(go) succeeded; want error")
	}
	if !strings.Contains(err.Error(), `backend "llvm" cannot emit "go"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

type fakeLLVMToolchain struct {
	compileCalls []fakeLLVMCompileCall
	linkCalls    []fakeLLVMLinkCall
}

type fakeLLVMCompileCall struct {
	irPath     string
	objectPath string
	target     string
}

type fakeLLVMLinkCall struct {
	objectPath string
	binaryPath string
	target     string
}

func (f *fakeLLVMToolchain) CompileObject(_ context.Context, irPath, objectPath, target string) error {
	f.compileCalls = append(f.compileCalls, fakeLLVMCompileCall{
		irPath:     irPath,
		objectPath: objectPath,
		target:     target,
	})
	return os.WriteFile(objectPath, []byte("object\n"), 0o644)
}

func (f *fakeLLVMToolchain) LinkBinary(_ context.Context, objectPath, binaryPath, target string) error {
	f.linkCalls = append(f.linkCalls, fakeLLVMLinkCall{
		objectPath: objectPath,
		binaryPath: binaryPath,
		target:     target,
	})
	return os.WriteFile(binaryPath, []byte("binary\n"), 0o755)
}
