package pkgmgr

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSnapshotFreezeStamp is the drift gate for golegacy_generated.go.
// The Osty sources below are the authoritative authors of every selfPkg*
// / Sem* function rendered into the snapshot. The transpiler that used
// to regenerate them was retired in #854, so the freeze policy is:
// edits to the .osty files MUST be hand-ported into golegacy_generated.go
// in the same change. Once both are aligned, update the expected hash
// here so the gate passes again.
//
// On mismatch the failure message prints the live hash so the operator
// can paste it back here after they've completed the hand-port. Skipping
// this gate without updating the snapshot is what the FROZEN SEED note
// at the top of golegacy_generated.go is warning about.
func TestSnapshotFreezeStamp(t *testing.T) {
	root := repoRoot(t)
	wants := map[string]string{
		"toolchain/pkgmgr.osty":       "fc97ce5621f9a31558b497da8f7e4f24953970a922694c75c67b1e1129a22dfc",
		"toolchain/semver.osty":       "5e318a536f857fea19dfa8fdef16358e7bb972f4494b2a2e68dfa7ff89e1a714",
		"toolchain/semver_parse.osty": "97ca51ea19f696fba5f8e707b4a7bc2c0ab771d110ebc753d0b004b2d51f67d4",
		"toolchain/semver_req.osty":   "baefe843ae4c72ba661c0f0ed92c4a0fdb61f52c74622d47e89f5d01fbde8eca",
	}
	for rel, want := range wants {
		got := hashSnapshotSource(t, filepath.Join(root, rel))
		if got != want {
			t.Errorf("%s drifted from snapshot stamp\n  want %s\n  got  %s\n  hand-port the change into internal/pkgmgr/golegacy_generated.go (FROZEN SEED note),\n  then update the expected hash in this test.", rel, want, got)
		}
	}
}

func hashSnapshotSource(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// repoRoot walks up from this test file's directory until it finds the
// repo root (identified by the presence of go.mod). Tests run with cwd
// set to the package dir, so we can't rely on a relative path to find
// toolchain/.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod found walking up from %s", file)
		}
		dir = parent
	}
}
