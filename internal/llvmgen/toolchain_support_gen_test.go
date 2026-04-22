package llvmgen

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestToolchainLlvmgenSupportSourcesTranspile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping llvmgen support transpile smoke test in short mode")
	}

	repoRoot := filepath.Join("..", "..")
	merged, err := MergeToolchainSupport(repoRoot)
	if err != nil {
		t.Fatalf("merge llvmgen support sources: %v", err)
	}

	tmpDir := t.TempDir()
	mergedPath := filepath.Join(tmpDir, "llvmgen_support_merged.osty")
	if err := os.WriteFile(mergedPath, merged, 0o644); err != nil {
		t.Fatalf("write merged source: %v", err)
	}
	if bytes.Contains(merged, []byte("pub fn llvmNativeEmitModule(")) {
		t.Fatal("merged llvmgen support still contains the native entry slice")
	}
	generatedPath := filepath.Join(tmpDir, "llvmgen_support_generated.go")
	goModPath := filepath.Join(tmpDir, "go.mod")

	cmd := exec.Command(
		"go", "run", "./cmd/osty-bootstrap-gen",
		"--package", "llvmgensmoke",
		"-o", generatedPath,
		mergedPath,
	)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("transpile merged llvmgen support: %v\n%s", err, bytes.TrimSpace(out))
	}

	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	if len(generated) == 0 {
		t.Fatal("generated file is empty")
	}
	if err := os.WriteFile(goModPath, []byte("module llvmgensmoke\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatalf("write temp go.mod: %v", err)
	}
	buildCmd := exec.Command("go", "test", ".")
	buildCmd.Dir = tmpDir
	buildOut, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compile generated llvmgen support: %v\n%s", err, bytes.TrimSpace(buildOut))
	}
	if !bytes.Contains(generated, []byte("func llvmRenderModule(")) {
		t.Fatal("generated file does not include llvm module render helpers")
	}
	if !bytes.Contains(generated, []byte("func llvmUnsupportedDiagnostic(")) {
		t.Fatal("generated file does not include unsupported diagnostic helpers")
	}
	if !bytes.Contains(generated, []byte("func llvmSmokeMinimalPrintIR(")) {
		t.Fatal("generated file does not include llvm smoke IR helpers")
	}
	if bytes.Contains(generated, []byte("llvmStrings.join(")) ||
		bytes.Contains(generated, []byte("llvmStrings.split(")) ||
		bytes.Contains(generated, []byte("llvmStrings.trimPrefix(")) {
		t.Fatal("generated file still contains unlowered std.strings helper calls")
	}
}
