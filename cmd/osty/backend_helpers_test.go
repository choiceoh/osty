package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/backend"
)

func TestParseCLIBackend(t *testing.T) {
	got, err := parseCLIBackend("go")
	if err != nil {
		t.Fatalf("parseCLIBackend(go): %v", err)
	}
	if got != backend.NameGo {
		t.Fatalf("parseCLIBackend(go) = %q, want go", got)
	}
	got, err = parseCLIBackend("llvm")
	if err != nil {
		t.Fatalf("parseCLIBackend(llvm): %v", err)
	}
	if got != backend.NameLLVM {
		t.Fatalf("parseCLIBackend(llvm) = %q, want llvm", got)
	}
	if _, err := parseCLIBackend("wasm"); err == nil {
		t.Fatal("parseCLIBackend(wasm) succeeded; want error")
	}
}

func TestDefaultEmitMode(t *testing.T) {
	if got := defaultEmitMode("gen", backend.NameGo); got != backend.EmitGoSource {
		t.Fatalf("gen/go default emit = %q, want %q", got, backend.EmitGoSource)
	}
	if got := defaultEmitMode("gen", backend.NameLLVM); got != backend.EmitLLVMIR {
		t.Fatalf("gen/llvm default emit = %q, want %q", got, backend.EmitLLVMIR)
	}
	if got := defaultEmitMode("pipeline", backend.NameGo); got != backend.EmitGoSource {
		t.Fatalf("pipeline/go default emit = %q, want %q", got, backend.EmitGoSource)
	}
	if got := defaultEmitMode("pipeline", backend.NameLLVM); got != backend.EmitLLVMIR {
		t.Fatalf("pipeline/llvm default emit = %q, want %q", got, backend.EmitLLVMIR)
	}
	if got := defaultEmitMode("build", backend.NameGo); got != backend.EmitBinary {
		t.Fatalf("build/go default emit = %q, want %q", got, backend.EmitBinary)
	}
}

func TestParseCLIEmitMode(t *testing.T) {
	got, err := parseCLIEmitMode("go")
	if err != nil {
		t.Fatalf("parseCLIEmitMode(go): %v", err)
	}
	if got != backend.EmitGoSource {
		t.Fatalf("parseCLIEmitMode(go) = %q, want go", got)
	}
	got, err = parseCLIEmitMode("llvm-ir")
	if err != nil {
		t.Fatalf("parseCLIEmitMode(llvm-ir): %v", err)
	}
	if got != backend.EmitLLVMIR {
		t.Fatalf("parseCLIEmitMode(llvm-ir) = %q, want llvm-ir", got)
	}
	if _, err := parseCLIEmitMode("wasm"); err == nil {
		t.Fatal("parseCLIEmitMode(wasm) succeeded; want error")
	}
}

func TestValidateCLIEmit(t *testing.T) {
	valid := []struct {
		tool string
		name backend.Name
		mode backend.EmitMode
	}{
		{"gen", backend.NameGo, backend.EmitGoSource},
		{"gen", backend.NameLLVM, backend.EmitLLVMIR},
		{"pipeline", backend.NameGo, backend.EmitGoSource},
		{"pipeline", backend.NameLLVM, backend.EmitLLVMIR},
		{"build", backend.NameGo, backend.EmitGoSource},
		{"build", backend.NameGo, backend.EmitBinary},
		{"build", backend.NameLLVM, backend.EmitObject},
		{"run", backend.NameGo, backend.EmitBinary},
		{"test", backend.NameGo, backend.EmitBinary},
	}
	for _, tc := range valid {
		if err := validateCLIEmit(tc.tool, tc.name, tc.mode); err != nil {
			t.Fatalf("validateCLIEmit(%q, %q, %q): %v", tc.tool, tc.name, tc.mode, err)
		}
	}

	invalid := []struct {
		tool string
		name backend.Name
		mode backend.EmitMode
		want string
	}{
		{"gen", backend.NameGo, backend.EmitBinary, "cannot emit"},
		{"gen", backend.NameLLVM, backend.EmitObject, "cannot emit"},
		{"pipeline", backend.NameGo, backend.EmitBinary, "cannot emit"},
		{"pipeline", backend.NameLLVM, backend.EmitObject, "cannot emit"},
		{"build", backend.NameGo, backend.EmitObject, "cannot emit"},
		{"run", backend.NameGo, backend.EmitGoSource, "requires --emit"},
		{"test", backend.NameLLVM, backend.EmitLLVMIR, "requires --emit"},
	}
	for _, tc := range invalid {
		err := validateCLIEmit(tc.tool, tc.name, tc.mode)
		if err == nil {
			t.Fatalf("validateCLIEmit(%q, %q, %q) succeeded; want error", tc.tool, tc.name, tc.mode)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("validateCLIEmit(%q, %q, %q) = %v, want substring %q", tc.tool, tc.name, tc.mode, err, tc.want)
		}
	}
}

func TestValidateCLIEmitAllowsLLVMSkeletonModes(t *testing.T) {
	for _, mode := range []backend.EmitMode{backend.EmitLLVMIR, backend.EmitObject, backend.EmitBinary} {
		if err := validateCLIEmit("build", backend.NameLLVM, mode); err != nil {
			t.Fatalf("build llvm %q rejected: %v", mode, err)
		}
	}
}

func TestCacheableBuildEmit(t *testing.T) {
	cases := []struct {
		name    backend.Name
		mode    backend.EmitMode
		want    bool
		context string
	}{
		{backend.NameGo, backend.EmitBinary, true, "go binary keeps the existing build cache"},
		{backend.NameGo, backend.EmitGoSource, false, "go source emission stays uncached"},
		{backend.NameLLVM, backend.EmitLLVMIR, true, "llvm-ir is the first successful LLVM build artifact"},
		{backend.NameLLVM, backend.EmitObject, true, "llvm object emission now produces a cacheable artifact"},
		{backend.NameLLVM, backend.EmitBinary, true, "llvm binary will use the build cache once implemented"},
	}
	for _, tc := range cases {
		if got := cacheableBuildEmit(tc.name, tc.mode); got != tc.want {
			t.Fatalf("%s: cacheableBuildEmit(%q, %q) = %v, want %v", tc.context, tc.name, tc.mode, got, tc.want)
		}
	}
}

func TestCachedArtifactsExist(t *testing.T) {
	root := t.TempDir()
	if !cachedArtifactsExist(root, nil) {
		t.Fatal("empty artifact map should preserve legacy cache behavior")
	}
	if err := os.MkdirAll(filepath.Join(root, ".osty", "out", "debug", "llvm", "runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".osty", "out", "debug", "llvm", "main.ll"), []byte("; ll\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	artifacts := map[string]string{
		"llvm_ir":     ".osty/out/debug/llvm/main.ll",
		"runtime_dir": ".osty/out/debug/llvm/runtime",
	}
	if !cachedArtifactsExist(root, artifacts) {
		t.Fatalf("expected artifact map to be present: %+v", artifacts)
	}
	artifacts["object"] = ".osty/out/debug/llvm/main.o"
	if cachedArtifactsExist(root, artifacts) {
		t.Fatalf("missing object artifact should invalidate cache hit: %+v", artifacts)
	}
}
