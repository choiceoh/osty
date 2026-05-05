package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/toolchain/selfhostcache"
)

func TestCacheSelfUsageMessage(t *testing.T) {
	got := cacheSelfUsage()
	for _, want := range []string{
		"osty cache-self",
		"--check",
		"--key",
		"--triple",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("usage missing %q:\n%s", want, got)
		}
	}
}

// fixtureRoot stages a synthetic project with a toolchain dir so
// `osty cache-self` can compute a real Key without depending on the
// repository's own toolchain/.
func fixtureRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "osty.toml"), []byte("[package]\nname = \"fake\"\n"), 0o644); err != nil {
		t.Fatalf("osty.toml: %v", err)
	}
	tc := filepath.Join(dir, "toolchain")
	if err := os.MkdirAll(tc, 0o755); err != nil {
		t.Fatalf("toolchain mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tc, "main.osty"), []byte("// stub\n"), 0o644); err != nil {
		t.Fatalf("main.osty: %v", err)
	}
	return dir
}

func TestCacheSelfPathPrintsCanonicalLocation(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)
	root := fixtureRoot(t)

	cmd := exec.Command(ostyBin, "cache-self")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cache-self: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if !strings.Contains(got, ".osty/cache/self-host/") {
		t.Errorf("path missing canonical prefix: %s", got)
	}
	if !strings.HasSuffix(got, selfhostcache.BinaryName()) {
		t.Errorf("path missing binary name suffix: %s", got)
	}
	// Path-only mode succeeds even when the file does not exist.
	if _, err := os.Stat(got); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected path to be absent, got stat err=%v", err)
	}
}

func TestCacheSelfCheckExitsNonZeroOnMiss(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)
	root := fixtureRoot(t)

	cmd := exec.Command(ostyBin, "cache-self", "--check")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected exit error when cache empty\n%s", out)
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("got %v, want ExitError", err)
	}
	if ee.ExitCode() != 1 {
		t.Errorf("exit = %d, want 1", ee.ExitCode())
	}
	if !strings.Contains(string(out), "no cached binary") {
		t.Errorf("output missing diagnostic:\n%s", out)
	}
}

func TestCacheSelfCheckExitsZeroOnHit(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)
	root := fixtureRoot(t)

	// Pre-populate the cache the way `osty install-self` would —
	// compute the key, write the binary at the canonical path.
	key, err := selfhostcache.ComputeKey(root)
	if err != nil {
		t.Fatalf("ComputeKey: %v", err)
	}
	target := selfhostcache.CachePath(root, key)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(target, []byte("fake binary"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	cmd := exec.Command(ostyBin, "cache-self", "--check")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cache-self --check: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != target {
		t.Errorf("path = %q, want %q", got, target)
	}
}

func TestCacheSelfKeyPrintsShaTriple(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)
	root := fixtureRoot(t)

	cmd := exec.Command(ostyBin, "cache-self", "--key")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cache-self --key: %v\n%s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if !strings.Contains(got, "-"+selfhostcache.HostTriple()) {
		t.Errorf("key missing triple suffix: %s", got)
	}
	// Cross-check by recomputing the key in-process.
	want, err := selfhostcache.ComputeKey(root)
	if err != nil {
		t.Fatalf("ComputeKey: %v", err)
	}
	if got != want.String() {
		t.Errorf("key = %q, want %q", got, want.String())
	}
}

func TestCacheSelfTripleNeedsNoToolchainDir(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)
	dir := t.TempDir()

	// No `osty.toml`, no toolchain/. --triple must work anyway —
	// it's purely runtime metadata.
	cmd := exec.Command(ostyBin, "cache-self", "--triple")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("cache-self --triple: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != selfhostcache.HostTriple() {
		t.Errorf("triple = %q, want %q", got, selfhostcache.HostTriple())
	}
}

func TestCacheSelfRejectsKeyAndTripleTogether(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)
	root := fixtureRoot(t)

	cmd := exec.Command(ostyBin, "cache-self", "--key", "--triple")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected exit error\n%s", out)
	}
	if !strings.Contains(string(out), "mutually exclusive") {
		t.Errorf("output missing exclusion note:\n%s", out)
	}
}
