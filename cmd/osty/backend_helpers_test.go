package main

import (
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

func TestRequireImplementedBackend(t *testing.T) {
	if err := requireImplementedBackend(backend.NameGo); err != nil {
		t.Fatalf("go backend rejected: %v", err)
	}
	err := requireImplementedBackend(backend.NameLLVM)
	if err == nil {
		t.Fatal("llvm backend accepted; want not-implemented error")
	}
	if !strings.Contains(err.Error(), "not implemented yet") {
		t.Fatalf("unexpected error: %v", err)
	}
}
