package selfhostcache

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scaffoldToolchain creates a minimal `toolchain/` tree under
// `projectRoot` and returns the absolute toolchain dir path.
func scaffoldToolchain(t *testing.T, projectRoot string, files map[string]string) string {
	t.Helper()
	tcDir := filepath.Join(projectRoot, "toolchain")
	if err := os.MkdirAll(tcDir, 0o755); err != nil {
		t.Fatalf("mkdir toolchain: %v", err)
	}
	for name, body := range files {
		full := filepath.Join(tcDir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
	return tcDir
}

func TestComputeKeyDeterministic(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{
		"main.osty": "fn main() {}\n",
		"lib.osty":  "fn helper() -> Int { 42 }\n",
	})
	a, err := ComputeKey(root)
	if err != nil {
		t.Fatalf("ComputeKey: %v", err)
	}
	b, err := ComputeKey(root)
	if err != nil {
		t.Fatalf("ComputeKey 2nd: %v", err)
	}
	if a != b {
		t.Fatalf("keys differ across runs: %v vs %v", a, b)
	}
	if len(a.ToolchainSHA) != 64 {
		t.Fatalf("expected 64-hex SHA, got %q", a.ToolchainSHA)
	}
	if !strings.Contains(a.Triple, "-") {
		t.Fatalf("triple has unexpected shape: %q", a.Triple)
	}
}

func TestComputeKeyChangesOnSourceEdit(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{"main.osty": "fn main() {}\n"})
	first, err := ComputeKey(root)
	if err != nil {
		t.Fatalf("ComputeKey first: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "toolchain", "main.osty"), []byte("fn main() { /* changed */ }\n"), 0o644); err != nil {
		t.Fatalf("rewrite main.osty: %v", err)
	}
	second, err := ComputeKey(root)
	if err != nil {
		t.Fatalf("ComputeKey second: %v", err)
	}
	if first.ToolchainSHA == second.ToolchainSHA {
		t.Fatalf("SHA did not change after source edit: %s", first.ToolchainSHA)
	}
}

func TestComputeKeyChangesOnNewFile(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{"main.osty": "fn main() {}\n"})
	first, _ := ComputeKey(root)
	if err := os.WriteFile(filepath.Join(root, "toolchain", "extra.osty"), []byte("fn extra() {}\n"), 0o644); err != nil {
		t.Fatalf("write extra.osty: %v", err)
	}
	second, _ := ComputeKey(root)
	if first.ToolchainSHA == second.ToolchainSHA {
		t.Fatalf("SHA did not change after adding file")
	}
}

func TestComputeKeyIgnoresGeneratedDir(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{
		"main.osty":                           "fn main() {}\n",
		filepath.Join(".osty", "out", "junk"): "ignored binary",
	})
	first, err := ComputeKey(root)
	if err != nil {
		t.Fatalf("ComputeKey first: %v", err)
	}
	// Mutate something inside the ignored dir.
	if err := os.WriteFile(filepath.Join(root, "toolchain", ".osty", "out", "junk"), []byte("changed"), 0o644); err != nil {
		t.Fatalf("rewrite junk: %v", err)
	}
	second, err := ComputeKey(root)
	if err != nil {
		t.Fatalf("ComputeKey second: %v", err)
	}
	if first.ToolchainSHA != second.ToolchainSHA {
		t.Fatalf("SHA changed after mutation under ignored .osty dir: %s vs %s", first.ToolchainSHA, second.ToolchainSHA)
	}
}

func TestComputeKeyEmptyToolchain(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "toolchain"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, err := ComputeKey(root)
	if err == nil {
		t.Fatal("expected error for empty toolchain")
	}
}

func TestResolveBinaryEnvOverride(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{"main.osty": "fn main() {}\n"})

	bin := filepath.Join(root, "external-osty-self")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho external\n"), 0o755); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	t.Setenv(SelfBinEnv, bin)

	got, key, err := ResolveBinary(root)
	if err != nil {
		t.Fatalf("ResolveBinary: %v", err)
	}
	if got != bin {
		t.Fatalf("got = %q, want env override %q", got, bin)
	}
	if key.String() != "" {
		t.Fatalf("expected zero key for env override, got %q", key.String())
	}
}

func TestResolveBinaryInTreeBuild(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{"main.osty": "fn main() {}\n"})
	t.Setenv(SelfBinEnv, "")

	debugBin := filepath.Join(root, "toolchain", ".osty", "out", "debug", "llvm", BinaryName())
	if err := os.MkdirAll(filepath.Dir(debugBin), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(debugBin, []byte("debug bin"), 0o755); err != nil {
		t.Fatalf("write debug bin: %v", err)
	}
	got, key, err := ResolveBinary(root)
	if err != nil {
		t.Fatalf("ResolveBinary: %v", err)
	}
	if got != debugBin {
		t.Fatalf("got = %q, want debug bin %q", got, debugBin)
	}
	if key.String() != "" {
		t.Fatalf("in-tree builds should report zero key, got %q", key.String())
	}
}

func TestResolveBinaryFallsThroughToCache(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{"main.osty": "fn main() {}\n"})
	t.Setenv(SelfBinEnv, "")

	key, err := ComputeKey(root)
	if err != nil {
		t.Fatalf("ComputeKey: %v", err)
	}
	cachedBin := CachePath(root, key)
	if err := os.MkdirAll(filepath.Dir(cachedBin), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cachedBin, []byte("cached bin"), 0o755); err != nil {
		t.Fatalf("write cached bin: %v", err)
	}

	got, gotKey, err := ResolveBinary(root)
	if err != nil {
		t.Fatalf("ResolveBinary: %v", err)
	}
	if got != cachedBin {
		t.Fatalf("got = %q, want cached %q", got, cachedBin)
	}
	if gotKey != key {
		t.Fatalf("returned key mismatch: got %v, want %v", gotKey, key)
	}
}

func TestResolveBinaryReturnsErrNotCached(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{"main.osty": "fn main() {}\n"})
	t.Setenv(SelfBinEnv, "")

	_, _, err := ResolveBinary(root)
	if !errors.Is(err, ErrNotCached) {
		t.Fatalf("err = %v, want ErrNotCached", err)
	}
}

// TestResolveBinaryReturnsErrNotCachedWithoutToolchain verifies that
// the lookup degrades to ErrNotCached even when there is no
// `toolchain/` directory at all — ComputeKey would otherwise error
// out, but for the resolver's purposes the absence is just another
// "no usable binary" outcome.
func TestResolveBinaryReturnsErrNotCachedWithoutToolchain(t *testing.T) {
	root := t.TempDir()
	t.Setenv(SelfBinEnv, "")

	_, _, err := ResolveBinary(root)
	if !errors.Is(err, ErrNotCached) {
		t.Fatalf("err = %v, want ErrNotCached for missing toolchain dir", err)
	}
}

func TestLocateProjectRootFindsManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "osty.toml"), []byte("[package]\nname=\"x\"\nversion=\"0.0.0\"\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	sub := filepath.Join(root, "deep", "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got, err := LocateProjectRoot(sub)
	if err != nil {
		t.Fatalf("LocateProjectRoot: %v", err)
	}
	gotRel, _ := filepath.Rel(root, got)
	wantRel, _ := filepath.Rel(root, root)
	if gotRel != wantRel {
		t.Fatalf("LocateProjectRoot = %q, want repo root %q", got, root)
	}
}

func TestLocateProjectRootFallsBackToAbs(t *testing.T) {
	root := t.TempDir()
	got, err := LocateProjectRoot(root)
	if err != nil {
		t.Fatalf("LocateProjectRoot: %v", err)
	}
	abs, _ := filepath.Abs(root)
	if got != abs {
		t.Fatalf("fallback path = %q, want abs of %q", got, abs)
	}
}

func TestInstallRoundTrip(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{"main.osty": "fn main() {}\n"})
	t.Setenv(SelfBinEnv, "")

	srcBin := filepath.Join(root, "built-osty-self")
	if err := os.WriteFile(srcBin, []byte("real binary bytes"), 0o755); err != nil {
		t.Fatalf("write src: %v", err)
	}

	key, err := ComputeKey(root)
	if err != nil {
		t.Fatalf("ComputeKey: %v", err)
	}
	if err := Install(root, key, srcBin); err != nil {
		t.Fatalf("Install: %v", err)
	}

	got, gotKey, err := ResolveBinary(root)
	if err != nil {
		t.Fatalf("ResolveBinary post-install: %v", err)
	}
	if got != CachePath(root, key) {
		t.Fatalf("expected resolved path to match CachePath, got %q", got)
	}
	if gotKey != key {
		t.Fatalf("key mismatch after install: %v vs %v", gotKey, key)
	}
	body, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read installed: %v", err)
	}
	if string(body) != "real binary bytes" {
		t.Fatalf("installed body = %q, want round-trip", body)
	}
}

func TestInstallRejectsZeroKey(t *testing.T) {
	root := t.TempDir()
	if err := Install(root, Key{}, "anything"); err == nil {
		t.Fatal("expected Install to reject zero key")
	}
}

func TestInstallRejectsMissingSource(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{"main.osty": "fn main() {}\n"})
	key, _ := ComputeKey(root)
	missing := filepath.Join(root, "does-not-exist")
	if err := Install(root, key, missing); err == nil {
		t.Fatal("expected Install to reject missing source")
	}
}

func TestKeyStringComponents(t *testing.T) {
	k := Key{ToolchainSHA: "deadbeef", Triple: "linux-amd64"}
	if got := k.String(); got != "deadbeef-linux-amd64" {
		t.Fatalf("Key.String = %q, want %q", got, "deadbeef-linux-amd64")
	}
	if (Key{}).String() != "" {
		t.Fatal("zero Key.String must be empty")
	}
}
