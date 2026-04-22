package selfhost_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/bootstrap/seedgen"
	"github.com/osty/osty/internal/selfhost/bundle"
)

// TestToolchainHirSourcesTranspile verifies the HIR bundle (ty +
// hir + hir_clone + pmcompile + hir_lower) goes through the bootstrap
// seedgen transpiler and the resulting Go compiles. Mirrors the
// llvmgen smoke test so regressions in any HIR file surface here.
func TestToolchainHirSourcesTranspile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping self-host hir transpile smoke test in short mode")
	}

	repoRoot := filepath.Join("..", "..")
	merged, err := bundle.MergeToolchainHir(repoRoot)
	if err != nil {
		t.Fatalf("merge toolchain hir sources: %v", err)
	}

	tmpDir := t.TempDir()
	mergedPath := filepath.Join(tmpDir, "toolchain_hir_merged.osty")
	if err := os.WriteFile(mergedPath, merged, 0o644); err != nil {
		t.Fatalf("write merged source: %v", err)
	}
	generatedPath := filepath.Join(tmpDir, "toolchain_hir_generated.go")
	goModPath := filepath.Join(tmpDir, "go.mod")

	generated, err := seedgen.Generate(seedgen.Config{
		SourcePath:  mergedPath,
		PackageName: "hirselfhostsmoke",
		RepoRoot:    repoRoot,
	})
	if err != nil {
		t.Fatalf("transpile merged toolchain hir: %v", err)
	}
	if err := os.WriteFile(generatedPath, generated, 0o644); err != nil {
		t.Fatalf("write generated file: %v", err)
	}
	if len(generated) == 0 {
		t.Fatal("generated file is empty")
	}
	// Spot-check the HIR lowerer entry points and annotation extractors
	// made it through transpile.
	for _, needle := range []string{
		"func hirLowerer(",
		"func hirLowerModule(",
		"func hirLowerTyToHirType(",
		"func hirLowerExtractInlineMode(",
		"func hirLowerExtractVectorizeArgs(",
		"func hirLowerParseAnnotationInt(",
	} {
		if !bytes.Contains(generated, []byte(needle)) {
			t.Errorf("generated hir bridge missing %q", needle)
		}
	}

	if err := os.WriteFile(goModPath, []byte("module hirselfhostsmoke\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatalf("write temp go.mod: %v", err)
	}
	buildCmd := exec.Command("go", "test", ".")
	buildCmd.Dir = tmpDir
	buildOut, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compile generated hir smoke bridge: %v\n%s", err, bytes.TrimSpace(buildOut))
	}
}

// TestToolchainMirSourcesTranspile verifies the MIR bundle (HIR
// surface + mir + mir_lower + mir_optimize + mir_validator) goes
// through the bootstrap seedgen transpiler and compiles as Go.
func TestToolchainMirSourcesTranspile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping self-host mir transpile smoke test in short mode")
	}

	repoRoot := filepath.Join("..", "..")
	merged, err := bundle.MergeToolchainMir(repoRoot)
	if err != nil {
		t.Fatalf("merge toolchain mir sources: %v", err)
	}

	tmpDir := t.TempDir()
	mergedPath := filepath.Join(tmpDir, "toolchain_mir_merged.osty")
	if err := os.WriteFile(mergedPath, merged, 0o644); err != nil {
		t.Fatalf("write merged source: %v", err)
	}
	generatedPath := filepath.Join(tmpDir, "toolchain_mir_generated.go")
	goModPath := filepath.Join(tmpDir, "go.mod")

	generated, err := seedgen.Generate(seedgen.Config{
		SourcePath:  mergedPath,
		PackageName: "mirselfhostsmoke",
		RepoRoot:    repoRoot,
	})
	if err != nil {
		t.Fatalf("transpile merged toolchain mir: %v", err)
	}
	if err := os.WriteFile(generatedPath, generated, 0o644); err != nil {
		t.Fatalf("write generated file: %v", err)
	}
	if dump := os.Getenv("DUMP_MIR_GENERATED"); dump != "" {
		if err := os.WriteFile(dump, generated, 0o644); err != nil {
			t.Logf("dump write failed: %v", err)
		}
	}
	if len(generated) == 0 {
		t.Fatal("generated file is empty")
	}
	for _, needle := range []string{
		"func mirLowerModule(",
		"func mirLowerStmt(",
		"func mirLowerExprToRValue(",
		"func mirLowerMatchStmt(",
	} {
		if !bytes.Contains(generated, []byte(needle)) {
			t.Errorf("generated mir bridge missing %q", needle)
		}
	}

	if err := os.WriteFile(goModPath, []byte("module mirselfhostsmoke\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatalf("write temp go.mod: %v", err)
	}
	buildCmd := exec.Command("go", "test", ".")
	buildCmd.Dir = tmpDir
	buildOut, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compile generated mir smoke bridge: %v\n%s", err, bytes.TrimSpace(buildOut))
	}
}
