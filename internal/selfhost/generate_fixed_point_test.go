package selfhost

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/osty/osty/internal/selfhost/bundle"
)

func TestGoGenerateSelfhostLeavesGeneratedArtifactsClean(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping regen fixed-point test in short mode")
	}

	repoRoot := filepath.Join("..", "..")
	worktree := filepath.Join(t.TempDir(), "worktree")
	runRepoCmd(t, repoRoot, "git", "worktree", "add", "--detach", worktree, "HEAD")
	t.Cleanup(func() {
		runRepoCmd(t, repoRoot, "git", "worktree", "remove", "--force", worktree)
	})
	overlaySelfhostGenerateInputs(t, repoRoot, worktree)

	artifacts := []string{
		"internal/selfhost/generated.go",
		"internal/selfhost/astbridge/generated.go",
	}
	originals := make(map[string][]byte, len(artifacts))
	for _, rel := range artifacts {
		originals[rel] = copyGeneratedArtifact(t, repoRoot, worktree, rel)
	}

	parserPath := filepath.Join(worktree, "toolchain", "parser.osty")
	now := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(parserPath, now, now); err != nil {
		t.Fatalf("mark parser source stale: %v", err)
	}

	runWorktreeCmd(t, worktree, "go", "generate", "./internal/selfhost")

	for _, rel := range artifacts {
		got, err := os.ReadFile(filepath.Join(worktree, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read regenerated %s: %v", rel, err)
		}
		if bytes.Equal(got, originals[rel]) {
			continue
		}
		diff := runDiffNoIndex(t, filepath.Join(repoRoot, filepath.FromSlash(rel)), filepath.Join(worktree, filepath.FromSlash(rel)))
		if len(diff) > 4000 {
			diff = diff[:4000]
		}
		t.Fatalf("go generate ./internal/selfhost changed %s:\n%s", rel, bytes.TrimSpace(diff))
	}
}

func runRepoCmd(t *testing.T, repoRoot string, name string, args ...string) []byte {
	t.Helper()
	return runCmd(t, repoRoot, name, args...)
}

func runWorktreeCmd(t *testing.T, worktree string, name string, args ...string) []byte {
	t.Helper()
	return runCmd(t, worktree, name, args...)
}

func runCmd(t *testing.T, dir string, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, bytes.TrimSpace(out))
	}
	return out
}

func copyGeneratedArtifact(t *testing.T, repoRoot, worktree, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	dst := filepath.Join(worktree, filepath.FromSlash(rel))
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return data
}

func overlaySelfhostGenerateInputs(t *testing.T, repoRoot, worktree string) {
	t.Helper()
	paths := []string{
		"toolchain",
		"internal/selfhost",
		"internal/bootstrap/gen",
		"cmd/osty-bootstrap-gen",
		"internal/ast/ast.go",
		"internal/token/token.go",
	}
	for _, rel := range bundle.ToolchainCheckerFiles() {
		paths = append(paths, rel)
	}
	seen := make(map[string]struct{}, len(paths))
	for _, rel := range paths {
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		copyPathIntoWorktree(t, repoRoot, worktree, rel)
	}
}

func copyPathIntoWorktree(t *testing.T, repoRoot, worktree, rel string) {
	t.Helper()
	src := filepath.Join(repoRoot, filepath.FromSlash(rel))
	info, err := os.Stat(src)
	if err != nil {
		t.Fatalf("stat %s: %v", rel, err)
	}
	dst := filepath.Join(worktree, filepath.FromSlash(rel))
	if !info.IsDir() {
		copyFileIntoWorktree(t, src, dst)
		return
	}
	if err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, relPath)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		copyFileIntoWorktree(t, path, target)
		return nil
	}); err != nil {
		t.Fatalf("overlay %s: %v", rel, err)
	}
}

func copyFileIntoWorktree(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

func runDiffNoIndex(t *testing.T, left, right string) []byte {
	t.Helper()
	cmd := exec.Command("git", "diff", "--no-index", "--", left, right)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return out
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("git diff --no-index %s %s failed: %v\n%s", left, right, err, bytes.TrimSpace(out))
	}
	if exitErr.ExitCode() != 1 {
		t.Fatalf("git diff --no-index %s %s failed: %v\n%s", left, right, err, bytes.TrimSpace(out))
	}
	return out
}
