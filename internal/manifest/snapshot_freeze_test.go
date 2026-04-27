package manifest

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSnapshotFreezeStamp is the drift gate for validate_snapshot.go.
// toolchain/manifest_validation.osty is the authoritative source for the
// validation policy rendered in that file. The transpiler that used to
// regenerate it was retired in #854, so the freeze policy is: edits to
// the .osty file MUST be hand-ported into validate_snapshot.go in the
// same change. Once both are aligned, update the expected hash here so
// the gate passes again.
//
// On mismatch the failure message prints the live hash so the operator
// can paste it back here after they've completed the hand-port. Skipping
// this gate without updating the snapshot is what the FROZEN SEED note
// at the top of validate_snapshot.go is warning about.
func TestSnapshotFreezeStamp(t *testing.T) {
	root := repoRoot(t)
	wants := map[string]string{
		"toolchain/manifest_validation.osty": "12e221ded6a62915fc98a1fd5bdd4490324d8b3e2dd6bef6bd7662dc5072ab65",
	}
	for rel, want := range wants {
		got := hashFile(t, filepath.Join(root, rel))
		if got != want {
			t.Errorf("%s drifted from snapshot stamp\n  want %s\n  got  %s\n  hand-port the change into internal/manifest/validate_snapshot.go (FROZEN SEED note),\n  then update the expected hash in this test.", rel, want, got)
		}
	}
}

func hashFile(t *testing.T, path string) string {
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
