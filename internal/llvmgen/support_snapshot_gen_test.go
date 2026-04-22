package llvmgen

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoGenerateSupportSnapshotIsFixedPoint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping llvmgen support snapshot go:generate test in short mode")
	}

	repoRoot := filepath.Join("..", "..")
	worktree := filepath.Join(t.TempDir(), "worktree")
	copyCurrentCheckout(t, repoRoot, worktree)

	runGenerate := func() {
		t.Helper()
		cmd := exec.Command("go", "generate", "./internal/llvmgen")
		cmd.Dir = worktree
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go generate ./internal/llvmgen: %v\n%s", err, bytes.TrimSpace(out))
		}
	}

	snapshotPath := filepath.Join(worktree, "internal/llvmgen/support_snapshot.go")
	runGenerate()
	first, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("read generated snapshot: %v", err)
	}
	runGenerate()
	second, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("read fixed-point snapshot: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("support snapshot go:generate is not a fixed point")
	}

	build := exec.Command("go", "test", "-run", "^$", "./internal/llvmgen")
	build.Dir = worktree
	out, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("compile generated support snapshot: %v\n%s", err, bytes.TrimSpace(out))
	}
}

func copyCurrentCheckout(t *testing.T, repoRoot, dstRoot string) {
	t.Helper()
	list := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	list.Dir = repoRoot
	out, err := list.CombinedOutput()
	if err != nil {
		t.Fatalf("git ls-files: %v\n%s", err, bytes.TrimSpace(out))
	}
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" {
			continue
		}
		srcPath := filepath.Join(repoRoot, rel)
		info, err := os.Lstat(srcPath)
		if err != nil {
			t.Fatalf("stat %s: %v", rel, err)
		}
		dstPath := filepath.Join(dstRoot, rel)
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(rel), err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(srcPath)
			if err != nil {
				t.Fatalf("readlink %s: %v", rel, err)
			}
			if err := os.Symlink(target, dstPath); err != nil {
				t.Fatalf("symlink %s: %v", rel, err)
			}
			continue
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if err := os.WriteFile(dstPath, data, info.Mode().Perm()); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}
