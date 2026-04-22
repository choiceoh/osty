package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/osty/osty/internal/check"
)

func TestCheckerCacheValidityUsesEmbeddedCheckerFingerprint(t *testing.T) {
	root := t.TempDir()
	writeCheckerCacheTestFile(t, filepath.Join(root, "internal/selfhost/generated.go"), "package selfhost\n\nconst generated = 1\n")
	writeCheckerCacheTestFile(t, filepath.Join(root, "internal/selfhost/astbridge/generated.go"), "package astbridge\n\nconst generated = 1\n")

	resetCheckerCacheValidityForTest()
	got := checkerCacheValidity(root)
	want := check.EmbeddedCheckerFingerprint(root)
	if want == "" {
		t.Fatal("EmbeddedCheckerFingerprint returned empty fingerprint")
	}
	if got != want {
		t.Fatalf("checkerCacheValidity = %q, want %q", got, want)
	}

	writeCheckerCacheTestFile(t, filepath.Join(root, "internal/selfhost/generated.go"), "package selfhost\n\nconst generated = 2\n")

	resetCheckerCacheValidityForTest()
	got2 := checkerCacheValidity(root)
	want2 := check.EmbeddedCheckerFingerprint(root)
	if got2 != want2 {
		t.Fatalf("checkerCacheValidity after edit = %q, want %q", got2, want2)
	}
	if got2 == got {
		t.Fatalf("checkerCacheValidity did not change after embedded checker edit: %q", got2)
	}
}

func resetCheckerCacheValidityForTest() {
	validityOnce = sync.Once{}
	validity = ""
}

func writeCheckerCacheTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
