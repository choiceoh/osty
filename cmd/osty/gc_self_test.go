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

func TestGCSelfUsageMessage(t *testing.T) {
	got := gcSelfUsage()
	for _, want := range []string{
		"osty gc-self",
		"--keep",
		"--older-than",
		"--dry-run",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("usage missing %q:\n%s", want, got)
		}
	}
}

// gcCLIFixture stages a project root with `entries` synthetic cache
// directories. Each binary is `body` bytes so size accounting can
// be checked precisely.
func gcCLIFixture(t *testing.T, entries int) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "osty.toml"), []byte("[package]\nname=\"fake\"\n"), 0o644); err != nil {
		t.Fatalf("osty.toml: %v", err)
	}
	tcDir := filepath.Join(root, "toolchain")
	if err := os.MkdirAll(tcDir, 0o755); err != nil {
		t.Fatalf("toolchain mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tcDir, "main.osty"), []byte("// stub\n"), 0o644); err != nil {
		t.Fatalf("main.osty: %v", err)
	}
	for i := 0; i < entries; i++ {
		name := strings.Repeat(string('a'+rune(i)), 64) + "-" + selfhostcache.HostTriple()
		dir := filepath.Join(root, selfhostcache.CacheDirName, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir entry: %v", err)
		}
		bin := filepath.Join(dir, selfhostcache.BinaryName())
		if err := os.WriteFile(bin, []byte("body"), 0o755); err != nil {
			t.Fatalf("write bin: %v", err)
		}
	}
	return root
}

func TestRunGCSelfDryRunDoesNotDelete(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)
	root := gcCLIFixture(t, 8)

	cmd := exec.Command(ostyBin, "gc-self", "--keep", "2", "--dry-run")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gc-self: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "would remove:") {
		t.Errorf("dry-run output missing would-remove marker:\n%s", out)
	}
	// Confirm nothing on disk was touched.
	dirEntries, err := os.ReadDir(filepath.Join(root, selfhostcache.CacheDirName))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(dirEntries) != 8 {
		t.Errorf("dry-run touched cache: %d entries remain, want 8", len(dirEntries))
	}
}

func TestRunGCSelfDeletesEntries(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)
	root := gcCLIFixture(t, 8)

	cmd := exec.Command(ostyBin, "gc-self", "--keep", "2")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gc-self: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "removed:") {
		t.Errorf("output missing removed marker:\n%s", out)
	}
	dirEntries, err := os.ReadDir(filepath.Join(root, selfhostcache.CacheDirName))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(dirEntries) != 2 {
		t.Errorf("after gc: %d entries, want 2", len(dirEntries))
	}
}

func TestRunGCSelfRejectsBadOlderThan(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)
	root := gcCLIFixture(t, 1)

	cmd := exec.Command(ostyBin, "gc-self", "--older-than", "not-a-duration")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected exit error\n%s", out)
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("got %v, want ExitError", err)
	}
	if ee.ExitCode() != 2 {
		t.Errorf("exit = %d, want 2", ee.ExitCode())
	}
	if !strings.Contains(string(out), "invalid --older-than") {
		t.Errorf("output missing diagnostic:\n%s", out)
	}
}

func TestRunGCSelfNoCacheDirIsHarmless(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "osty.toml"), []byte("[package]\nname=\"fake\"\n"), 0o644); err != nil {
		t.Fatalf("osty.toml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "toolchain"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "toolchain", "main.osty"), []byte("// stub\n"), 0o644); err != nil {
		t.Fatalf("main.osty: %v", err)
	}

	cmd := exec.Command(ostyBin, "gc-self")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gc-self: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "summary:") {
		t.Errorf("output missing summary line:\n%s", out)
	}
}

func TestFormatBytesRendersHumanReadable(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0B"},
		{500, "500B"},
		{1500, "1.5KB"},
		{2 * 1024 * 1024, "2.0MB"},
		{int64(3.5 * 1024 * 1024 * 1024), "3.5GB"},
	}
	for _, tc := range cases {
		if got := formatBytes(tc.n); got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
