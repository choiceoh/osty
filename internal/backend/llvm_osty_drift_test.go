package backend

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/gen"
	"github.com/osty/osty/internal/llvmgen"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

func TestOstyLLVMEmitterMatchesProductionBridge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Osty LLVM emitter production bridge drift check in short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go binary not on PATH; skipping Osty LLVM emitter production bridge drift check")
	}

	root := repoRoot(t)
	expectedMinimal := goLLVMIRFromFixture(t,
		filepath.Join(root, "testdata", "backend", "llvm_smoke", "minimal_print.osty"),
		"/tmp/minimal_print.osty",
	)
	expectedScalar := goLLVMIRFromFixture(t,
		filepath.Join(root, "testdata", "backend", "llvm_smoke", "scalar_arithmetic.osty"),
		"/tmp/scalar_arithmetic.osty",
	)
	expectedControl := goLLVMIRFromFixture(t,
		filepath.Join(root, "testdata", "backend", "llvm_smoke", "control_flow.osty"),
		"/tmp/control_flow.osty",
	)
	expectedBooleans := goLLVMIRFromFixture(t,
		filepath.Join(root, "testdata", "backend", "llvm_smoke", "booleans.osty"),
		"/tmp/booleans.osty",
	)
	expectedSkeleton := string(llvmgen.RenderSkeleton(
		"main",
		"/tmp/unsupported.osty",
		string(EmitLLVMIR),
		"x86_64-unknown-linux-gnu",
		errors.New("llvmgen: unsupported source shape"),
	))
	expectedGoFFIUnsupported := llvmgen.UnsupportedSummary(
		llvmgen.UnsupportedDiagnosticFor("go-ffi", "strings"),
	)
	expectedExpressionUnsupported := llvmgen.UnsupportedSummary(
		llvmgen.UnsupportedDiagnosticFor("expression", "String literal"),
	)

	tmp := t.TempDir()
	src := generateOstyLLVMEmitterGo(t, filepath.Join(root, "examples", "selfhost-core", "llvmgen.osty"))
	writeFile(t, filepath.Join(tmp, "llvmgen.go"), src)
	writeFile(t, filepath.Join(tmp, "llvmgen_drift_test.go"), []byte(fmt.Sprintf(`package main

import "testing"

func TestGeneratedOstyLLVMEmitterMatchesGoBootstrap(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"minimal", llvmSmokeMinimalPrintIR("/tmp/minimal_print.osty"), %q},
		{"scalar", llvmSmokeScalarArithmeticIR("/tmp/scalar_arithmetic.osty"), %q},
		{"control", llvmSmokeControlFlowIR("/tmp/control_flow.osty"), %q},
		{"booleans", llvmSmokeBooleansIR("/tmp/booleans.osty"), %q},
		{"skeleton", llvmRenderSkeleton("main", "/tmp/unsupported.osty", "llvm-ir", "x86_64-unknown-linux-gnu", "llvmgen: unsupported source shape"), %q},
		{"go-ffi-unsupported", llvmUnsupportedSummary(llvmUnsupportedDiagnostic("go-ffi", "strings")), %q},
		{"expression-unsupported", llvmUnsupportedSummary(llvmUnsupportedDiagnostic("expression", "String literal")), %q},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Fatalf("%%s drift\n--- got ---\n%%s\n--- want ---\n%%s", tc.name, tc.got, tc.want)
		}
	}
}
`, expectedMinimal, expectedScalar, expectedControl, expectedBooleans, expectedSkeleton, expectedGoFFIUnsupported, expectedExpressionUnsupported)))
	writeFile(t, filepath.Join(tmp, "go.mod"), []byte("module ostyllvmdrift\n\ngo 1.22\n"))

	cmd := exec.Command("go", "test", ".")
	cmd.Dir = tmp
	combined, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Osty LLVM emitter production bridge drift check failed: %v\n%s", err, combined)
	}
}

func goLLVMIRFromFixture(t *testing.T, path, sourcePath string) string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	file, diags := parser.ParseDiagnostics(src)
	if len(diags) != 0 {
		t.Fatalf("parse %s: %v", path, diags)
	}
	ir, err := llvmgen.Generate(file, llvmgen.Options{SourcePath: sourcePath})
	if err != nil {
		t.Fatalf("llvmgen.Generate(%s): %v", path, err)
	}
	return string(ir)
}

func generateOstyLLVMEmitterGo(t *testing.T, path string) []byte {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	file, diags := parser.ParseDiagnostics(src)
	if len(diags) != 0 {
		t.Fatalf("parse %s: %v", path, diags)
	}
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res)
	out, genErr := gen.GenerateMapped("main", file, res, chk, path)
	if genErr != nil {
		t.Fatalf("generate Go for %s: %v", path, genErr)
	}
	return out
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
