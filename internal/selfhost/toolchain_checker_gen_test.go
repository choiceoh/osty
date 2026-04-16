package selfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/selfhost/bundle"
)

func TestToolchainCheckerSourcesTranspile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping toolchain checker transpile smoke test in short mode")
	}

	repoRoot := filepath.Join("..", "..")
	merged, err := bundle.MergeToolchainChecker(repoRoot)
	if err != nil {
		t.Fatalf("merge toolchain checker sources: %v", err)
	}

	tmpDir := t.TempDir()
	mergedPath := filepath.Join(tmpDir, "toolchain_checker_merged.osty")
	if err := os.WriteFile(mergedPath, merged, 0o644); err != nil {
		t.Fatalf("write merged source: %v", err)
	}
	generatedPath := filepath.Join(tmpDir, "toolchain_checker_generated.go")

	cmd := exec.Command(
		"go", "run", "-tags", "selfhostgen", "./cmd/osty", "gen",
		"--backend", "go",
		"--emit", "go",
		"--package", "selfhostsmoke",
		"-o", generatedPath,
		mergedPath,
	)
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("transpile merged toolchain checker: %v\n%s", err, bytes.TrimSpace(out))
	}

	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	if len(generated) == 0 {
		t.Fatalf("generated file is empty")
	}
	if !bytes.Contains(generated, []byte("func frontendCheckSourceStructured(")) {
		t.Fatalf("generated file does not include checker entrypoints")
	}
	if !bytes.Contains(generated, []byte("func elabInfer(")) {
		t.Fatalf("generated file does not include elaborator entrypoints")
	}
}
