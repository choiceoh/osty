package selfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/selfhost/bundle"
)

func TestToolchainLLVMGenSourcesTranspile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping self-host llvmgen transpile smoke test in short mode")
	}

	repoRoot := filepath.Join("..", "..")
	merged, err := bundle.MergeToolchainLLVMGen(repoRoot)
	if err != nil {
		t.Fatalf("merge toolchain llvmgen sources: %v", err)
	}

	tmpDir := t.TempDir()
	mergedPath := filepath.Join(tmpDir, "toolchain_llvmgen_merged.osty")
	if err := os.WriteFile(mergedPath, merged, 0o644); err != nil {
		t.Fatalf("write merged source: %v", err)
	}
	generatedPath := filepath.Join(tmpDir, "toolchain_llvmgen_generated.go")
	goModPath := filepath.Join(tmpDir, "go.mod")

	cmd := exec.Command(
		"go", "run", "./cmd/osty-bootstrap-gen",
		"--package", "llvmgenselfhostsmoke",
		"-o", generatedPath,
		mergedPath,
	)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("transpile merged toolchain llvmgen: %v\n%s", err, bytes.TrimSpace(out))
	}

	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	if len(generated) == 0 {
		t.Fatal("generated file is empty")
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
		bytes.Contains(generated, []byte("llvmStrings.trimPrefix(")) ||
		bytes.Contains(generated, []byte("llvmStrings.hasPrefix(")) {
		t.Fatal("generated file still contains unlowered std.strings helper calls")
	}

	if err := os.WriteFile(goModPath, []byte("module llvmgenselfhostsmoke\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatalf("write temp go.mod: %v", err)
	}
	buildCmd := exec.Command("go", "test", ".")
	buildCmd.Dir = tmpDir
	buildOut, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compile generated llvmgen smoke bridge: %v\n%s", err, bytes.TrimSpace(buildOut))
	}
}
