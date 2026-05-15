package toolchain

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureNativeLIRProtoBuildsManagedArtifactOnce(t *testing.T) {
	root := t.TempDir()
	oldProjectRoot := managedProjectRootFunc
	oldSourceRoot := sourceRepoRootFunc
	oldInstaller := installNativeLIRProto
	t.Cleanup(func() {
		managedProjectRootFunc = oldProjectRoot
		sourceRepoRootFunc = oldSourceRoot
		installNativeLIRProto = oldInstaller
	})

	managedProjectRootFunc = func(string) (string, error) { return root, nil }
	sourceRepoRootFunc = func() (string, error) { return t.TempDir(), nil }
	calls := 0
	installNativeLIRProto = func(dest string) error {
		calls++
		return os.WriteFile(dest, []byte("managed lirproto"), 0o755)
	}

	path, err := EnsureNativeLIRProto(".")
	if err != nil {
		t.Fatalf("EnsureNativeLIRProto error: %v", err)
	}
	want := filepath.Join(root, ".osty", "toolchain", Version(), NativeLIRProtoBinaryName())
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if calls != 1 {
		t.Fatalf("installer calls = %d, want 1", calls)
	}

	path2, err := EnsureNativeLIRProto(".")
	if err != nil {
		t.Fatalf("second EnsureNativeLIRProto error: %v", err)
	}
	if path2 != want {
		t.Fatalf("second path = %q, want %q", path2, want)
	}
	if calls != 1 {
		t.Fatalf("installer calls after second ensure = %d, want 1", calls)
	}
}

func TestManagedNativeLIRProtoPathIncludesVersionedToolchainDir(t *testing.T) {
	root := t.TempDir()
	got := ManagedNativeLIRProtoPath(root)
	want := filepath.Join(root, ".osty", "toolchain", Version(), NativeLIRProtoBinaryName())
	if got != want {
		t.Fatalf("ManagedNativeLIRProtoPath = %q, want %q", got, want)
	}
}

func TestEnsureNativeLIRProtoRebuildsWhenStage0SourcesAreNewer(t *testing.T) {
	root := t.TempDir()
	repo := t.TempDir()
	oldProjectRoot := managedProjectRootFunc
	oldSourceRoot := sourceRepoRootFunc
	oldInstaller := installNativeLIRProto
	t.Cleanup(func() {
		managedProjectRootFunc = oldProjectRoot
		sourceRepoRootFunc = oldSourceRoot
		installNativeLIRProto = oldInstaller
	})

	managedProjectRootFunc = func(string) (string, error) { return root, nil }
	sourceRepoRootFunc = func() (string, error) { return repo, nil }
	stage0Source := filepath.Join(repo, "internal/backend/stage0/emit.go")
	if err := os.MkdirAll(filepath.Dir(stage0Source), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(stage0Source), err)
	}
	if err := os.WriteFile(stage0Source, []byte("fresh stage0 source"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", stage0Source, err)
	}

	path := ManagedNativeLIRProtoPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("stale lirproto"), 0o755); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	oldTime := time.Unix(1, 0)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes(%q): %v", path, err)
	}

	calls := 0
	installNativeLIRProto = func(dest string) error {
		calls++
		return os.WriteFile(dest, []byte("rebuilt lirproto"), 0o755)
	}

	got, err := EnsureNativeLIRProto(".")
	if err != nil {
		t.Fatalf("EnsureNativeLIRProto error: %v", err)
	}
	if got != path {
		t.Fatalf("path = %q, want %q", got, path)
	}
	if calls != 1 {
		t.Fatalf("installer calls = %d, want 1", calls)
	}
}
