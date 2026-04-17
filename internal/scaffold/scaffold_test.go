package scaffold

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCreateHappyPath(t *testing.T) {
	parent := t.TempDir()
	path, d := Create(Options{Parent: parent, Name: "myproj", Kind: KindBin})
	if d != nil {
		t.Fatalf("Create returned diagnostic: %v", d)
	}
	if path == "" {
		t.Fatal("Create returned empty path")
	}
	for _, f := range []string{"osty.toml", "main.osty", "main_test.osty", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(path, f)); err != nil {
			t.Errorf("expected %s to exist: %v", f, err)
		}
	}
}

func TestInitHappyPath(t *testing.T) {
	parent := t.TempDir()
	_, d := Init(Options{Parent: parent, Name: "myproj", Kind: KindLib})
	if d != nil {
		t.Fatalf("Init returned diagnostic: %v", d)
	}
	for _, f := range []string{"osty.toml", "lib.osty", "lib_test.osty", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(parent, f)); err != nil {
			t.Errorf("expected %s to exist: %v", f, err)
		}
	}
}

// TestCreateRollbackOnMkdirFailure verifies that when MkdirAll fails
// (read-only parent dir) no partial outer directory is left behind.
func TestCreateRollbackOnMkdirFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod semantics differ on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatalf("chmod parent: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	path, d := Create(Options{Parent: parent, Name: "myproj", Kind: KindBin})
	if d == nil {
		t.Fatalf("expected diagnostic from read-only parent, got none (path=%q)", path)
	}
	target := filepath.Join(parent, "myproj")
	if _, err := os.Stat(target); err == nil {
		t.Errorf("outer dir %s exists after failed Create", target)
	} else if !os.IsNotExist(err) {
		t.Errorf("unexpected stat err on %s: %v", target, err)
	}
}

// TestInitRollbackOnMidWriteFailure induces a WriteFile failure on the
// second file by staging a dangling symlink in its place. The symlink
// target doesn't exist, so checkNoConflicts treats the slot as free
// (stat follows links), but the subsequent WriteFile fails. The
// transaction should roll back the first file and leave the filesystem
// in its pre-Init state.
func TestInitRollbackOnMidWriteFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	parent := t.TempDir()
	// KindBin writes: osty.toml, main.osty, main_test.osty, .gitignore.
	// Place a dangling symlink at main.osty so file #2 fails.
	dangling := filepath.Join(parent, "main.osty")
	if err := os.Symlink(filepath.Join(parent, "nonexistent", "target"), dangling); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	_, d := Init(Options{Parent: parent, Name: "myproj", Kind: KindBin})
	if d == nil {
		t.Fatal("expected diagnostic from dangling-symlink write, got none")
	}

	// osty.toml was written then rolled back.
	if _, err := os.Stat(filepath.Join(parent, "osty.toml")); err == nil {
		t.Errorf("osty.toml still present after rollback")
	} else if !os.IsNotExist(err) {
		t.Errorf("unexpected stat err on osty.toml: %v", err)
	}
	// Later files were never attempted.
	for _, name := range []string{"main_test.osty", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(parent, name)); err == nil {
			t.Errorf("%s present but should not have been written", name)
		}
	}
	// The dangling symlink itself is pre-existing and must be left alone.
	if fi, err := os.Lstat(dangling); err != nil {
		t.Errorf("pre-existing symlink removed by rollback: %v", err)
	} else if fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("main.osty is no longer a symlink after rollback")
	}
}

// TestInitWorkspaceRollbackOnMidWriteFailure exercises the multi-dir
// case: a workspace scaffold writes root files, then enters the member
// directory. A failing slot inside the member dir must trigger
// rollback of the root files and the member osty.toml that had already
// been written.
func TestInitWorkspaceRollbackOnMidWriteFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	parent := t.TempDir()
	// Workspace writes root osty.toml, root .gitignore, then inside
	// core/: osty.toml, main.osty, main_test.osty, .gitignore.
	// Pre-create memberDir and plant a dangling symlink at main.osty
	// so the write inside the member dir fails. memberDir pre-exists
	// here (we had to put the symlink somewhere), so tx.mkdir won't
	// claim ownership of it — the rollback scope is the files written
	// inside it, which is what this test asserts.
	memberDir := filepath.Join(parent, "core")
	if err := os.MkdirAll(memberDir, 0o755); err != nil {
		t.Fatalf("mkdir memberDir: %v", err)
	}
	if err := os.Symlink(filepath.Join(parent, "nonexistent", "target"),
		filepath.Join(memberDir, "main.osty")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	_, d := Init(Options{Parent: parent, Kind: KindWorkspace, WorkspaceMember: "core"})
	if d == nil {
		t.Fatal("expected diagnostic from workspace mid-write failure")
	}
	// Root files written before the failure should be rolled back.
	for _, name := range []string{"osty.toml", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(parent, name)); err == nil {
			t.Errorf("%s still present after rollback", name)
		}
	}
	// The member osty.toml written just before the failure must be gone.
	if _, err := os.Stat(filepath.Join(memberDir, "osty.toml")); err == nil {
		t.Errorf("core/osty.toml still present after rollback")
	}
}

// TestWriteTxMkdirSkipsExisting verifies that an existing directory is
// not recorded for rollback, so a preexisting directory survives
// rollback.
func TestWriteTxMkdirSkipsExisting(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("pre-mkdir: %v", err)
	}
	tx := &writeTx{}
	if err := tx.mkdir(sub); err != nil {
		t.Fatalf("tx.mkdir on existing: %v", err)
	}
	if len(tx.createdDirs) != 0 {
		t.Errorf("existing dir recorded for rollback: %v", tx.createdDirs)
	}
	tx.rollback()
	if _, err := os.Stat(sub); err != nil {
		t.Errorf("preexisting dir removed by rollback: %v", err)
	}
}

// TestWriteTxRollbackOrderFilesBeforeDirs verifies that rollback
// removes files before their containing directories, so rmdir sees an
// empty directory.
func TestWriteTxRollbackOrderFilesBeforeDirs(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")

	tx := &writeTx{}
	if err := tx.mkdir(sub); err != nil {
		t.Fatalf("tx.mkdir: %v", err)
	}
	if d := tx.writeFiles([]fileSpec{
		{filepath.Join(sub, "a.txt"), "a"},
		{filepath.Join(sub, "b.txt"), "b"},
	}); d != nil {
		t.Fatalf("tx.writeFiles: %v", d)
	}
	tx.rollback()

	if _, err := os.Stat(sub); err == nil {
		t.Errorf("sub dir still present after rollback")
	} else if !os.IsNotExist(err) {
		t.Errorf("unexpected stat err: %v", err)
	}
}
