package profile

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCacheWriteReadRoundtrip covers the common incremental-build
// path: write a fingerprint, read it back, assert equality.
func TestCacheWriteReadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	fp := &Fingerprint{
		Profile:     NameDebug,
		Target:      "",
		ToolVersion: "test",
		Sources:     map[string]string{"main.osty": "abc123"},
		Features:    []string{"alpha"},
	}
	if err := fp.Write(dir); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, CacheDirName, NameDebug, "go.json")); err != nil {
		t.Fatalf("backend-aware cache path missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, CacheDirName, NameDebug+".json")); !os.IsNotExist(err) {
		t.Fatalf("legacy cache path should not be written, stat err=%v", err)
	}
	got, err := ReadFingerprint(dir, NameDebug, "")
	if err != nil {
		t.Fatalf("ReadFingerprint: %v", err)
	}
	if got == nil {
		t.Fatalf("nil fingerprint")
	}
	if !got.Equal(fp) {
		t.Errorf("roundtrip lost data: %+v vs %+v", got, fp)
	}
}

// TestFingerprintEqualDetectsSourceDrift flips one hash and asserts
// the fingerprint comparison reports inequality — this is what
// triggers a rebuild in `osty build`.
func TestFingerprintEqualDetectsSourceDrift(t *testing.T) {
	a := &Fingerprint{
		ToolVersion: "v1",
		Sources:     map[string]string{"x.osty": "h1"},
	}
	b := &Fingerprint{
		ToolVersion: "v1",
		Sources:     map[string]string{"x.osty": "h2"},
	}
	if a.Equal(b) {
		t.Errorf("different hashes should compare unequal")
	}
}

// TestFingerprintEqualToolVersion makes sure upgrading the toolchain
// invalidates every cached artifact.
func TestFingerprintEqualToolVersion(t *testing.T) {
	a := &Fingerprint{
		ToolVersion: "v1",
		Sources:     map[string]string{"x.osty": "h"},
	}
	b := &Fingerprint{
		ToolVersion: "v2",
		Sources:     map[string]string{"x.osty": "h"},
	}
	if a.Equal(b) {
		t.Errorf("different tool versions should compare unequal")
	}
}

// TestReadFingerprintMissing returns (nil, nil) for a cold cache
// rather than surfacing an error — `osty build` uses that to decide
// "cold build, proceed".
func TestReadFingerprintMissing(t *testing.T) {
	dir := t.TempDir()
	fp, err := ReadFingerprint(dir, NameDebug, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fp != nil {
		t.Errorf("expected nil for missing cache, got %+v", fp)
	}
}

// TestReadFingerprintIgnoresLegacyPath confirms old <key>.json records do not
// make migrated builds look fresh.
func TestReadFingerprintIgnoresLegacyPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, CacheDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := LegacyCachePath(dir, NameDebug, "")
	body := []byte(`{"profile":"debug","tool_version":"old","sources":{"main.osty":"abc"}}`)
	if err := os.WriteFile(legacy, body, 0o644); err != nil {
		t.Fatal(err)
	}
	fp, err := ReadFingerprint(dir, NameDebug, "")
	if err != nil {
		t.Fatalf("ReadFingerprint: %v", err)
	}
	if fp != nil {
		t.Fatalf("ReadFingerprint returned legacy cache: %+v", fp)
	}
	legacyFP, err := ReadLegacyFingerprint(dir, NameDebug, "")
	if err != nil {
		t.Fatalf("ReadLegacyFingerprint: %v", err)
	}
	if legacyFP == nil || legacyFP.ToolVersion != "old" {
		t.Fatalf("legacy read failed: %+v", legacyFP)
	}
}

// TestListCacheAndClean exercises the cache-visualisation helpers.
// After writing two fingerprints, ListCache should return both
// entries sorted; CleanCache should remove them + any .osty/out
// tree.
func TestListCacheAndClean(t *testing.T) {
	dir := t.TempDir()
	a := &Fingerprint{Profile: NameRelease, ToolVersion: "t", Sources: map[string]string{"a": "x"}}
	b := &Fingerprint{Profile: NameDebug, ToolVersion: "t", Sources: map[string]string{"b": "y"}}
	if err := a.Write(dir); err != nil {
		t.Fatalf("Write a: %v", err)
	}
	if err := b.Write(dir); err != nil {
		t.Fatalf("Write b: %v", err)
	}
	if err := os.WriteFile(LegacyCachePath(dir, "legacy", ""), []byte(`{"profile":"legacy","tool_version":"t","sources":{}}`), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	// Seed an out directory so CleanCache has something to remove.
	outDir := OutputDir(dir, NameDebug, "")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir out: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("seed out: %v", err)
	}

	entries, err := ListCache(dir)
	if err != nil {
		t.Fatalf("ListCache: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(entries), entries)
	}
	if entries[0].Profile != NameDebug {
		t.Errorf("entries not sorted by profile name: %+v", entries)
	}
	if entries[0].Backend != "go" || entries[1].Backend != "go" {
		t.Errorf("backend not inferred from cache path: %+v", entries)
	}

	total, err := CleanCache(dir)
	if err != nil {
		t.Fatalf("CleanCache: %v", err)
	}
	if total == 0 {
		t.Errorf("CleanCache reported 0 bytes reclaimed")
	}
	if _, err := os.Stat(filepath.Join(dir, CacheDirName)); !os.IsNotExist(err) {
		t.Errorf("cache dir not removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, OutDirName)); !os.IsNotExist(err) {
		t.Errorf("out dir not removed: %v", err)
	}
}

// TestHashSources smoke-tests the walker by seeding two .osty files
// plus one non-osty file and confirming only the .osty entries land
// in the map.
func TestHashSources(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.osty"), []byte("fn a() {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.osty"), []byte("fn b() {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	// .osty/cache should be skipped so the fingerprint doesn't feed
	// back into itself.
	hidden := filepath.Join(dir, ".osty", "cache")
	if err := os.MkdirAll(hidden, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hidden, "skip.osty"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := HashSources(dir, func(n string) bool {
		return filepath.Ext(n) == ".osty"
	})
	if err != nil {
		t.Fatalf("HashSources: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("want 2 .osty files hashed, got %d: %+v", len(got), got)
	}
	if _, ok := got["a.osty"]; !ok {
		t.Errorf("a.osty missing from %+v", got)
	}
	if _, ok := got["sub/b.osty"]; !ok {
		t.Errorf("sub/b.osty missing from %+v", got)
	}
	for name := range got {
		if name == "README.md" {
			t.Errorf("non-osty file included")
		}
	}
}
