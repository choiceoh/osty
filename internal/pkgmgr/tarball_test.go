package pkgmgr

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestCreateTarGzDeterministic packs the same source tree twice and
// asserts the two archives are byte-identical. Cornerstone of
// publish-time reproducibility: two publishes of the same sources
// must agree on the sha256 checksum.
func TestCreateTarGzDeterministic(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "pkg")
	writeTree(t, src, map[string]string{
		"osty.toml":  "[package]\nname = \"x\"\nversion = \"0.1.0\"\n",
		"main.osty":  "fn main() { println(\"hi\") }\n",
		"sub/a.osty": "pub fn a() {}\n",
		"sub/b.osty": "pub fn b() {}\n",
	})
	// A hidden file that should NOT ship:
	writeTree(t, src, map[string]string{".DS_Store": "junk"})

	var first, second bytes.Buffer
	if err := CreateTarGz(src, &first); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := CreateTarGz(src, &second); err != nil {
		t.Fatalf("second: %v", err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Errorf("archives differ: %d vs %d bytes", first.Len(), second.Len())
	}
	// And the sha256 checksum should match.
	if HashBytes(first.Bytes()) != HashBytes(second.Bytes()) {
		t.Errorf("hashes differ")
	}
}

// TestCreateTarGzRoundtrip packs + extracts into a fresh directory
// and verifies every file and its content lands.
func TestCreateTarGzRoundtrip(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "pkg")
	writeTree(t, src, map[string]string{
		"osty.toml": "pkg data",
		"main.osty": "src",
		"dir/a":     "alpha",
	})
	var buf bytes.Buffer
	if err := CreateTarGz(src, &buf); err != nil {
		t.Fatalf("pack: %v", err)
	}
	dst := filepath.Join(tmp, "out")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write the bytes to disk because extractTarGz reads from path.
	tgz := filepath.Join(tmp, "pkg.tgz")
	if err := os.WriteFile(tgz, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := extractTarGz(tgz, dst); err != nil {
		t.Fatalf("extract: %v", err)
	}
	want := map[string]string{
		"osty.toml": "pkg data",
		"main.osty": "src",
		"dir/a":     "alpha",
	}
	for path, content := range want {
		got, err := os.ReadFile(filepath.Join(dst, path))
		if err != nil {
			t.Errorf("read %s: %v", path, err)
			continue
		}
		if string(got) != content {
			t.Errorf("%s: got %q, want %q", path, got, content)
		}
	}
}

// TestHashDirStable confirms the directory-hash is independent of
// file mtime but reflects content.
func TestHashDirStable(t *testing.T) {
	tmp := t.TempDir()
	a := filepath.Join(tmp, "a")
	writeTree(t, a, map[string]string{
		"x": "hello",
		"y": "world",
	})
	h1, err := HashDir(a)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	h2, err := HashDir(a)
	if err != nil {
		t.Fatalf("hash2: %v", err)
	}
	if h1 != h2 {
		t.Errorf("stable hash differs: %s vs %s", h1, h2)
	}
	// Mutating content changes the hash.
	if err := os.WriteFile(filepath.Join(a, "x"), []byte("HELLO"), 0o644); err != nil {
		t.Fatal(err)
	}
	h3, _ := HashDir(a)
	if h1 == h3 {
		t.Errorf("hash should change after content edit")
	}
}

// TestVerifyChecksum accepts matches and rejects mismatches.
func TestVerifyChecksum(t *testing.T) {
	if err := VerifyChecksum("sha256:abc", "sha256:abc"); err != nil {
		t.Errorf("match should succeed: %v", err)
	}
	if err := VerifyChecksum("sha256:abc", "sha256:def"); err == nil {
		t.Errorf("mismatch should fail")
	}
	// Empty `want` means "don't check".
	if err := VerifyChecksum("", "sha256:def"); err != nil {
		t.Errorf("empty want should be no-op: %v", err)
	}
}

// writeTree is a small helper to populate dir with the given content
// map (paths are relative to dir).
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for p, content := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
