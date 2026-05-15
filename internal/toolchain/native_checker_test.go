package toolchain

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureNativeCheckerBuildsManagedArtifactOnce(t *testing.T) {
	root := t.TempDir()
	oldProjectRoot := managedProjectRootFunc
	oldInstaller := installNativeChecker
	t.Cleanup(func() {
		managedProjectRootFunc = oldProjectRoot
		installNativeChecker = oldInstaller
	})

	managedProjectRootFunc = func(string) (string, error) { return root, nil }
	calls := 0
	installNativeChecker = func(dest string) error {
		calls++
		return os.WriteFile(dest, []byte("managed checker"), 0o755)
	}

	path, err := EnsureNativeChecker(".")
	if err != nil {
		t.Fatalf("EnsureNativeChecker error: %v", err)
	}
	want := filepath.Join(root, ".osty", "toolchain", Version(), NativeCheckerBinaryName())
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if calls != 1 {
		t.Fatalf("installer calls = %d, want 1", calls)
	}

	path2, err := EnsureNativeChecker(".")
	if err != nil {
		t.Fatalf("second EnsureNativeChecker error: %v", err)
	}
	if path2 != want {
		t.Fatalf("second path = %q, want %q", path2, want)
	}
	if calls != 1 {
		t.Fatalf("installer calls after second ensure = %d, want 1", calls)
	}
}

func TestManagedNativeCheckerPathIncludesVersionedToolchainDir(t *testing.T) {
	root := t.TempDir()
	got := ManagedNativeCheckerPath(root)
	want := filepath.Join(root, ".osty", "toolchain", Version(), NativeCheckerBinaryName())
	if got != want {
		t.Fatalf("ManagedNativeCheckerPath = %q, want %q", got, want)
	}
}

func TestInstallManagedBinaryReplacesExistingArtifactAfterRenameFailure(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "tmp-bin")
	dest := filepath.Join(dir, "managed-bin")
	if err := os.WriteFile(tmp, []byte("new"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", tmp, err)
	}
	if err := os.WriteFile(dest, []byte("old"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", dest, err)
	}

	oldRename := renameManagedFile
	oldRemove := removeManagedFile
	t.Cleanup(func() {
		renameManagedFile = oldRename
		removeManagedFile = oldRemove
	})

	renameCalls := 0
	renameManagedFile = func(oldpath, newpath string) error {
		renameCalls++
		if renameCalls == 1 {
			return os.ErrExist
		}
		return os.Rename(oldpath, newpath)
	}
	removeCalls := 0
	removeManagedFile = func(name string) error {
		removeCalls++
		return os.Remove(name)
	}

	if err := installManagedBinary(tmp, dest, "test-tool"); err != nil {
		t.Fatalf("installManagedBinary: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", dest, err)
	}
	if string(got) != "new" {
		t.Fatalf("managed artifact = %q, want new", got)
	}
	if renameCalls != 2 {
		t.Fatalf("rename calls = %d, want 2", renameCalls)
	}
	if removeCalls != 1 {
		t.Fatalf("remove calls = %d, want 1", removeCalls)
	}
}

func TestInstallManagedBinaryPreservesExistingArtifactAfterUnrelatedRenameFailure(t *testing.T) {
	dir := t.TempDir()
	tmp := filepath.Join(dir, "tmp-bin")
	dest := filepath.Join(dir, "managed-bin")
	if err := os.WriteFile(tmp, []byte("new"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", tmp, err)
	}
	if err := os.WriteFile(dest, []byte("old"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", dest, err)
	}

	oldRename := renameManagedFile
	oldRemove := removeManagedFile
	t.Cleanup(func() {
		renameManagedFile = oldRename
		removeManagedFile = oldRemove
	})

	renameCalls := 0
	renameManagedFile = func(string, string) error {
		renameCalls++
		return errors.New("permission denied")
	}
	removeCalls := 0
	removeManagedFile = func(name string) error {
		removeCalls++
		return os.Remove(name)
	}

	if err := installManagedBinary(tmp, dest, "test-tool"); err == nil {
		t.Fatal("installManagedBinary returned nil, want unrelated rename failure")
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", dest, err)
	}
	if string(got) != "old" {
		t.Fatalf("managed artifact = %q, want old artifact preserved", got)
	}
	if renameCalls != 1 {
		t.Fatalf("rename calls = %d, want 1", renameCalls)
	}
	if removeCalls != 0 {
		t.Fatalf("remove calls = %d, want 0", removeCalls)
	}
}
