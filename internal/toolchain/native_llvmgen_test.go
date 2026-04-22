package toolchain

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureNativeLLVMGenBuildsManagedArtifactOnce(t *testing.T) {
	root := t.TempDir()
	oldProjectRoot := managedProjectRootFunc
	oldSourceRoot := sourceRepoRootFunc
	oldInstaller := installNativeLLVMGen
	t.Cleanup(func() {
		managedProjectRootFunc = oldProjectRoot
		sourceRepoRootFunc = oldSourceRoot
		installNativeLLVMGen = oldInstaller
	})

	managedProjectRootFunc = func(string) (string, error) { return root, nil }
	sourceRepoRootFunc = func() (string, error) { return t.TempDir(), nil }
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

func TestEnsureNativeLLVMGenRebuildsWhenSourcesAreNewer(t *testing.T) {
	root := t.TempDir()
	repo := t.TempDir()
	oldProjectRoot := managedProjectRootFunc
	oldSourceRoot := sourceRepoRootFunc
	oldInstaller := installNativeLLVMGen
	t.Cleanup(func() {
		managedProjectRootFunc = oldProjectRoot
		sourceRepoRootFunc = oldSourceRoot
		installNativeLLVMGen = oldInstaller
	})

	managedProjectRootFunc = func(string) (string, error) { return root, nil }
	sourceRepoRootFunc = func() (string, error) { return repo, nil }
	for _, rel := range []string{
		"cmd/osty-native-llvmgen/main.go",
		"internal/backend/llvm.go",
		"internal/llvmgen/generator.go",
		"internal/nativellvmgen/exec.go",
		"go.mod",
	} {
		path := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", path, err)
		}
		if err := os.WriteFile(path, []byte("fresh source"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}
	path := ManagedNativeLLVMGenPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("stale llvmgen"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	oldTime := time.Unix(1, 0)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes(%q): %v", path, err)
	}

	calls := 0
	installNativeLLVMGen = func(dest string) error {
		calls++
		return os.WriteFile(dest, []byte("rebuilt llvmgen"), 0o755)
	}

	got, err := EnsureNativeLLVMGen(".")
	if err != nil {
		t.Fatalf("EnsureNativeLLVMGen error: %v", err)
	}
	if got != path {
		t.Fatalf("path = %q, want %q", got, path)
	}
	if calls != 1 {
		t.Fatalf("installer calls = %d, want 1", calls)
	}
}
