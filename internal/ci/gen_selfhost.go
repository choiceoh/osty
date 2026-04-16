//go:build ignore

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root, err := findRepoRoot()
	if err != nil {
		return err
	}
	cmd := exec.Command(
		"go", "run", "-tags", "selfhostgen", "./cmd/osty", "gen",
		"--backend", "go",
		"--emit", "go",
		"--package", "ci",
		"-o", "internal/ci/osty_generated.go",
		"examples/selfhost-core/ci.osty",
	)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("generate selfhost ci core: %w\n%s", err, bytes.TrimSpace(output))
	}
	return patchGeneratedPath(filepath.Join(root, "internal/ci/osty_generated.go"), root)
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find repository root from %s", dir)
		}
		dir = parent
	}
}

func patchGeneratedPath(path, root string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read generated ci core: %w", err)
	}
	prefix := filepath.ToSlash(root) + "/"
	src := strings.ReplaceAll(string(data), prefix, "")
	if src == string(data) {
		return nil
	}
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		return fmt.Errorf("write generated ci core: %w", err)
	}
	return nil
}
