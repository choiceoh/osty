package toolchain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureNativeLLVMGenBuildsManagedArtifactOnce(t *testing.T) {
	root := t.TempDir()
	oldProjectRoot := managedProjectRootFunc
	oldInstaller := installNativeLLVMGen
	t.Cleanup(func() {
		managedProjectRootFunc = oldProjectRoot
		installNativeLLVMGen = oldInstaller
	})

	managedProjectRootFunc = func(string) (string, error) { return root, nil }
	calls := 0
	installNativeLLVMGen = func(dest string) error {
		calls++
		return os.WriteFile(dest, []byte("managed llvmgen"), 0o755)
	}

	path, err := EnsureNativeLLVMGen(".")
	if err != nil {
		t.Fatalf("EnsureNativeLLVMGen error: %v", err)
	}
	want := filepath.Join(root, ".osty", "toolchain", Version(), NativeLLVMGenBinaryName())
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if calls != 1 {
		t.Fatalf("installer calls = %d, want 1", calls)
	}

	path2, err := EnsureNativeLLVMGen(".")
	if err != nil {
		t.Fatalf("second EnsureNativeLLVMGen error: %v", err)
	}
	if path2 != want {
		t.Fatalf("second path = %q, want %q", path2, want)
	}
	if calls != 1 {
		t.Fatalf("installer calls after second ensure = %d, want 1", calls)
	}
}

func TestManagedNativeLLVMGenPathIncludesVersionedToolchainDir(t *testing.T) {
	root := t.TempDir()
	got := ManagedNativeLLVMGenPath(root)
	want := filepath.Join(root, ".osty", "toolchain", Version(), NativeLLVMGenBinaryName())
	if got != want {
		t.Fatalf("ManagedNativeLLVMGenPath = %q, want %q", got, want)
	}
}
