package llvmgen

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGoGenerateSupportSnapshotIsFixedPoint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping llvmgen support snapshot go:generate test in short mode")
	}

	repoRoot := filepath.Join("..", "..")
	worktree := filepath.Join(t.TempDir(), "worktree")
	add := exec.Command("git", "worktree", "add", "--detach", worktree, "HEAD")
	add.Dir = repoRoot
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("add temp worktree: %v\n%s", err, bytes.TrimSpace(out))
	}
	defer func() {
		remove := exec.Command("git", "worktree", "remove", worktree, "--force")
		remove.Dir = repoRoot
		_, _ = remove.CombinedOutput()
	}()

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
