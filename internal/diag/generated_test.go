package diag_test

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestErrorCodesMarkdownUpToDate fails when ERROR_CODES.md has drifted
// from the doc comments in codes.go. Regenerate with
// `go generate ./internal/diag/...`.
func TestErrorCodesMarkdownUpToDate(t *testing.T) {
	if testing.Short() {
		t.Skip("skip drift check in short mode — go generate runs the same logic")
	}
	repoRoot := filepath.Join("..", "..")
	cmd := exec.Command(
		"go", "run", "./cmd/codesdoc",
		"-in", "internal/diag/codes.go",
		"-check", "ERROR_CODES.md",
	)
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("ERROR_CODES.md out of date:\n%s\nregenerate: go generate ./internal/diag/...", stderr.String())
	}
}

// TestDiagManifestUpToDate fails when toolchain/diag_manifest.osty has
// drifted from codes.go. The manifest derives from the same source, so
// any diff means it is stale.
func TestDiagManifestUpToDate(t *testing.T) {
	if testing.Short() {
		t.Skip("skip drift check in short mode — go generate runs the same logic")
	}
	repoRoot := filepath.Join("..", "..")
	paths := []string{
		"toolchain/diag_manifest.osty",
	}
	for _, p := range paths {
		p := p
		t.Run(p, func(t *testing.T) {
			cmd := exec.Command(
				"go", "run", "./cmd/codesdoc",
				"-in", "internal/diag/codes.go",
				"-manifest-check", p,
			)
			cmd.Dir = repoRoot
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("%s out of date:\n%s\nregenerate: go generate ./internal/diag/...", p, stderr.String())
			}
		})
	}
}

// TestDiagHarvestCasesUpToDate fails when the generated
// toolchain/diag_examples.osty has drifted from the Example: blocks in
// codes.go. The harvest test consumes this file, so stale cases =
// silently missing coverage.
func TestDiagHarvestCasesUpToDate(t *testing.T) {
	if testing.Short() {
		t.Skip("skip drift check in short mode — go generate runs the same logic")
	}
	repoRoot := filepath.Join("..", "..")
	cmd := exec.Command(
		"go", "run", "./cmd/codesdoc",
		"-in", "internal/diag/codes.go",
		"-harvest-cases-check", "toolchain/diag_examples.osty",
	)
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("toolchain/diag_examples.osty out of date:\n%s\nregenerate: go generate ./internal/diag/...", stderr.String())
	}
}
