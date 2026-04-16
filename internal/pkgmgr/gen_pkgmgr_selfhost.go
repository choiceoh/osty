//go:build ignore

package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var sourceFiles = []string{
	"examples/selfhost-core/semver.osty",
	"examples/selfhost-core/semver_req.osty",
	"examples/selfhost-core/pkgmgr.osty",
}

const mergedPath = "/tmp/pkgmgr_selfhost_merged.osty"

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
	merged, err := mergedSource(root)
	if err != nil {
		return err
	}
	if err := os.WriteFile(mergedPath, merged, 0o644); err != nil {
		return fmt.Errorf("write merged pkgmgr source: %w", err)
	}
	defer os.Remove(mergedPath)

	outPath := filepath.Join(root, "internal/pkgmgr/selfhost_generated.go")
	cmd := exec.Command("go", "run", "-tags", "selfhostgen", "./cmd/osty", "gen", "--package", "pkgmgr", "-o", outPath, mergedPath)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("generate pkgmgr selfhost core: %w\n%s", err, bytes.TrimSpace(output))
	}
	return formatGenerated(outPath)
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

func mergedSource(root string) ([]byte, error) {
	var b strings.Builder
	for _, rel := range sourceFiles {
		path := filepath.Join(root, filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", rel, err)
		}
		b.WriteString("// ---- ")
		b.WriteString(rel)
		b.WriteString(" ----\n")
		b.Write(data)
		if len(data) == 0 || data[len(data)-1] != '\n' {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

func formatGenerated(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read generated pkgmgr code: %w", err)
	}
	formatted, err := format.Source(data)
	if err != nil {
		return fmt.Errorf("format generated pkgmgr code: %w", err)
	}
	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		return fmt.Errorf("write generated pkgmgr code: %w", err)
	}
	return nil
}
