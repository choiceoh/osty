package toolchain

import (
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
