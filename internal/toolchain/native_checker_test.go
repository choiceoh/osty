package toolchain

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestEnsureNativeCheckerReturnsRecursionGuardError(t *testing.T) {
	t.Setenv(RecursionGuardEnv, "1")
	_, err := EnsureNativeChecker(".")
	if err == nil {
		t.Fatal("EnsureNativeChecker returned nil, want recursion guard error")
	}
	if !strings.Contains(err.Error(), RecursionGuardEnv) {
		t.Fatalf("error %q does not mention %q", err, RecursionGuardEnv)
	}
}

func TestFilterEnvRemovesInheritedRecursionGuard(t *testing.T) {
	env := []string{
		"PATH=/usr/bin",
		RecursionGuardEnv + "=0",
		"HOME=/home/test",
		RecursionGuardEnv + "=anything",
	}
	got := filterEnv(env, RecursionGuardEnv)
	for _, kv := range got {
		if strings.HasPrefix(kv, RecursionGuardEnv+"=") {
			t.Fatalf("filterEnv left a %s entry: %v", RecursionGuardEnv, got)
		}
	}
	final := append(got, RecursionGuardEnv+"=1")
	seen := 0
	for _, kv := range final {
		if strings.HasPrefix(kv, RecursionGuardEnv+"=") {
			if kv != RecursionGuardEnv+"=1" {
				t.Fatalf("unexpected guard entry %q in %v", kv, final)
			}
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("expected exactly one %s=1 entry after append, got %d in %v", RecursionGuardEnv, seen, final)
	}
}

func TestBuildNativeCheckerFailsWhenOstySelfCacheMisses(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, NativeCheckerBinaryName())

	oldRepoRoot := sourceRepoRootFunc
	oldVerify := verifyOstySelfCached
	t.Cleanup(func() {
		sourceRepoRootFunc = oldRepoRoot
		verifyOstySelfCached = oldVerify
	})

	sourceRepoRootFunc = func() (string, error) { return root, nil }
	verifyOstySelfCached = func(string) error {
		return errors.New("osty-self not cached: run `osty install-self` first")
	}

	err := buildNativeChecker(dest)
	if err == nil {
		t.Fatal("buildNativeChecker returned nil, want osty-self cache-miss error")
	}
	if !strings.Contains(err.Error(), "osty-self not cached") {
		t.Fatalf("error %q does not propagate cache-miss reason", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("dest %q must not exist after failed build (stat err: %v)", dest, statErr)
	}
}

// TestResolveNativeCheckerLLVMHonorsEnvOverride exercises the env-var
// escape hatch the bootstrap-recursion story relies on: when a
// prebuilt LLVM-target binary is staged outside the worktree and
// `OSTY_NATIVE_CHECKER_LLVM_BIN` points at it, the resolver returns
// that path without ever touching the in-tree build output. Mirrors
// the OSTY_SELF_BIN precedent for osty-self. Complements
// `RecursionGuardEnv` (set by `buildNativeChecker` on the
// subprocess) — that flag aborts an in-progress nested build, while
// this env var prevents the build from being triggered in the first
// place.
func TestResolveNativeCheckerLLVMHonorsEnvOverride(t *testing.T) {
	dir := t.TempDir()
	prebuilt := filepath.Join(dir, "prebuilt-checker")
	if err := os.WriteFile(prebuilt, []byte("prebuilt"), 0o755); err != nil {
		t.Fatalf("stage prebuilt: %v", err)
	}
	t.Setenv(NativeCheckerLLVMBinEnv, prebuilt)
	// A different project root so the in-tree path is definitely
	// absent — guarantees the env override is what produced the hit.
	otherRoot := t.TempDir()
	got := ResolveNativeCheckerLLVM(otherRoot)
	if got != prebuilt {
		t.Fatalf("ResolveNativeCheckerLLVM = %q, want %q (env override)", got, prebuilt)
	}
}

// TestResolveNativeCheckerLLVMFallsBackToInTreeBuild covers the
// default path: with the env var unset (or pointing at a missing
// binary), the resolver looks at the conventional in-tree build
// output `<project>/cmd/osty-native-checker/.osty/out/debug/llvm/`.
// That is the location `osty build --backend llvm
// cmd/osty-native-checker/` writes to per the README, AND the
// location `buildNativeChecker` reads from when promoting the
// artifact into the managed slot — so this fallback keeps the two
// paths in lockstep.
func TestResolveNativeCheckerLLVMFallsBackToInTreeBuild(t *testing.T) {
	root := t.TempDir()
	inTree := ManagedNativeCheckerLLVMPath(root)
	if err := os.MkdirAll(filepath.Dir(inTree), 0o755); err != nil {
		t.Fatalf("mkdir in-tree: %v", err)
	}
	if err := os.WriteFile(inTree, []byte("in-tree"), 0o755); err != nil {
		t.Fatalf("write in-tree: %v", err)
	}
	t.Setenv(NativeCheckerLLVMBinEnv, "")
	got := ResolveNativeCheckerLLVM(root)
	if got != inTree {
		t.Fatalf("ResolveNativeCheckerLLVM = %q, want %q (in-tree fallback)", got, inTree)
	}
}

// TestResolveNativeCheckerLLVMReturnsEmptyWhenNothingStaged is the
// "no usable binary" signal callers branch on to decide between
// "build it" and "fall back to the Go-built variant". The empty
// string is a deliberate sentinel — no error type — so callers do
// not have to match on `os.ErrNotExist`.
func TestResolveNativeCheckerLLVMReturnsEmptyWhenNothingStaged(t *testing.T) {
	root := t.TempDir()
	t.Setenv(NativeCheckerLLVMBinEnv, "")
	got := ResolveNativeCheckerLLVM(root)
	if got != "" {
		t.Fatalf("ResolveNativeCheckerLLVM = %q, want empty (no binary staged)", got)
	}
}

// TestResolveNativeCheckerLLVMIgnoresMissingEnvPath guards against
// a silent skip when the env var points at a stale path (e.g. the
// user's shell profile pins the var across worktrees and the binary
// gets cleaned). The resolver must fall through to the in-tree
// search instead of returning the stale path.
func TestResolveNativeCheckerLLVMIgnoresMissingEnvPath(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(t.TempDir(), "missing-checker")
	t.Setenv(NativeCheckerLLVMBinEnv, stale)
	inTree := ManagedNativeCheckerLLVMPath(root)
	if err := os.MkdirAll(filepath.Dir(inTree), 0o755); err != nil {
		t.Fatalf("mkdir in-tree: %v", err)
	}
	if err := os.WriteFile(inTree, []byte("in-tree"), 0o755); err != nil {
		t.Fatalf("write in-tree: %v", err)
	}
	got := ResolveNativeCheckerLLVM(root)
	if got != inTree {
		t.Fatalf("ResolveNativeCheckerLLVM = %q, want %q (env path missing, fall back to in-tree)", got, inTree)
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
