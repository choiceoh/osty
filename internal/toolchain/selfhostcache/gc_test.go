package selfhostcache

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// gcFixture builds a synthetic project root with `n` cached
// entries. Each entry's binary mtime is set to `baseTime + i*step`
// so tests can assert deterministic LRU ordering. The current
// toolchain SHA (computed from the synthesized toolchain/) is
// returned so tests know which entry the policy will pin.
func gcFixture(t *testing.T, entries int, baseTime time.Time, step time.Duration) (string, string) {
	t.Helper()
	root := t.TempDir()

	tcDir := filepath.Join(root, "toolchain")
	if err := os.MkdirAll(tcDir, 0o755); err != nil {
		t.Fatalf("mkdir toolchain: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tcDir, "main.osty"), []byte("// stub\n"), 0o644); err != nil {
		t.Fatalf("write main.osty: %v", err)
	}

	currentKey, err := ComputeKey(root)
	if err != nil {
		t.Fatalf("ComputeKey: %v", err)
	}

	for i := 0; i < entries; i++ {
		// Synthesize SHA-shaped names so the parser doesn't object;
		// they don't have to be valid hex SHA outputs since
		// ListEntries doesn't enforce that.
		name := strings.Repeat(string('a'+rune(i)), 64) + "-" + HostTriple()
		dir := filepath.Join(root, CacheDirName, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir entry: %v", err)
		}
		bin := filepath.Join(dir, BinaryName())
		if err := os.WriteFile(bin, []byte("body"), 0o755); err != nil {
			t.Fatalf("write bin: %v", err)
		}
		mt := baseTime.Add(time.Duration(i) * step)
		if err := os.Chtimes(bin, mt, mt); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
	return root, currentKey.ToolchainSHA
}

func TestListEntriesReturnsEmptyWhenNoCache(t *testing.T) {
	root := t.TempDir()
	got, err := ListEntries(root)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("entries = %v, want empty", got)
	}
}

func TestListEntriesIncludesCurrentSHAFlag(t *testing.T) {
	root, currentSHA := gcFixture(t, 3, time.Now(), time.Second)

	// Drop one extra entry whose SHA matches the live toolchain so
	// ListEntries flags it.
	currentDir := filepath.Join(root, CacheDirName, currentSHA+"-"+HostTriple())
	if err := os.MkdirAll(currentDir, 0o755); err != nil {
		t.Fatalf("mkdir current: %v", err)
	}
	if err := os.WriteFile(filepath.Join(currentDir, BinaryName()), []byte("body"), 0o755); err != nil {
		t.Fatalf("write current bin: %v", err)
	}

	entries, err := ListEntries(root)
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("entries len = %d, want 4", len(entries))
	}
	flagged := 0
	for _, e := range entries {
		if e.CurrentSHA {
			flagged++
			if !strings.HasPrefix(e.Key, currentSHA+"-") {
				t.Errorf("flagged entry %q does not match current SHA prefix %q", e.Key, currentSHA)
			}
		}
	}
	if flagged != 1 {
		t.Errorf("flagged = %d, want 1", flagged)
	}
}

func TestPlanKeepsCurrentSHARegardlessOfLRU(t *testing.T) {
	root, currentSHA := gcFixture(t, DefaultGCKeep+3, time.Now().Add(-1*time.Hour), time.Minute)

	// Add the live-toolchain entry as the *oldest* one. Plan must
	// still keep it.
	currentDir := filepath.Join(root, CacheDirName, currentSHA+"-"+HostTriple())
	if err := os.MkdirAll(currentDir, 0o755); err != nil {
		t.Fatalf("mkdir current: %v", err)
	}
	bin := filepath.Join(currentDir, BinaryName())
	if err := os.WriteFile(bin, []byte("body"), 0o755); err != nil {
		t.Fatalf("write current: %v", err)
	}
	old := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(bin, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	plan, err := Plan(root, GCOptions{Keep: 1})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	keptCurrent := false
	for _, e := range plan.Kept {
		if e.CurrentSHA {
			keptCurrent = true
		}
	}
	if !keptCurrent {
		t.Errorf("plan did not keep current-SHA entry")
	}
	for _, e := range plan.Removed {
		if e.CurrentSHA {
			t.Errorf("plan removed current-SHA entry %q", e.Key)
		}
	}
}

func TestPlanRespectsLRUCap(t *testing.T) {
	// 8 entries with strictly increasing mtimes; current SHA does
	// not appear among them so every entry competes for LRU slots.
	root, _ := gcFixture(t, 8, time.Now().Add(-8*time.Minute), time.Minute)

	plan, err := Plan(root, GCOptions{Keep: 3})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Kept) != 3 {
		t.Errorf("kept = %d, want 3", len(plan.Kept))
	}
	if len(plan.Removed) != 5 {
		t.Errorf("removed = %d, want 5", len(plan.Removed))
	}

	// Newest-first: kept entries' min mtime should exceed removed
	// entries' max mtime.
	keptMin := plan.Kept[0].ModTime
	for _, e := range plan.Kept {
		if e.ModTime.Before(keptMin) {
			keptMin = e.ModTime
		}
	}
	for _, e := range plan.Removed {
		if !e.ModTime.Before(keptMin) {
			t.Errorf("removed entry %q has mtime %v ≥ kept min %v", e.Key, e.ModTime, keptMin)
		}
	}
}

func TestPlanOlderThanRemovesOldEntriesEvenWithinLRUCap(t *testing.T) {
	// 5 entries: 2 fresh (within 1h), 3 old (>24h ago).
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "toolchain"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "toolchain", "main.osty"), []byte("// stub\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	now := time.Now()
	mtimes := []time.Time{
		now.Add(-30 * time.Minute), // fresh
		now.Add(-10 * time.Minute), // fresh
		now.Add(-25 * time.Hour),   // old
		now.Add(-30 * time.Hour),   // old
		now.Add(-72 * time.Hour),   // old
	}
	for i, mt := range mtimes {
		name := strings.Repeat(string('a'+rune(i)), 64) + "-" + HostTriple()
		dir := filepath.Join(root, CacheDirName, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir entry: %v", err)
		}
		bin := filepath.Join(dir, BinaryName())
		if err := os.WriteFile(bin, []byte("body"), 0o755); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := os.Chtimes(bin, mt, mt); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	plan, err := Plan(root, GCOptions{Keep: 10, OlderThan: 24 * time.Hour, Now: now})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Kept) != 2 {
		t.Errorf("kept = %d, want 2", len(plan.Kept))
	}
	if len(plan.Removed) != 3 {
		t.Errorf("removed = %d, want 3", len(plan.Removed))
	}
}

func TestRunGCDeletesEntries(t *testing.T) {
	root, _ := gcFixture(t, 6, time.Now().Add(-6*time.Minute), time.Minute)

	result, err := RunGC(root, GCOptions{Keep: 2})
	if err != nil {
		t.Fatalf("RunGC: %v", err)
	}
	if len(result.Removed) != 4 {
		t.Errorf("removed = %d, want 4", len(result.Removed))
	}

	// The removed dirs should be gone from disk.
	for _, e := range result.Removed {
		if _, err := os.Stat(e.Path); !os.IsNotExist(err) {
			t.Errorf("entry %q still on disk; stat err = %v", e.Path, err)
		}
	}
	// The kept dirs should still be present.
	for _, e := range result.Kept {
		if _, err := os.Stat(e.Path); err != nil {
			t.Errorf("kept entry %q missing from disk: %v", e.Path, err)
		}
	}
}

func TestRunGCDryRunDoesNotTouchDisk(t *testing.T) {
	root, _ := gcFixture(t, 4, time.Now().Add(-4*time.Minute), time.Minute)

	result, err := RunGC(root, GCOptions{Keep: 1, DryRun: true})
	if err != nil {
		t.Fatalf("RunGC dry-run: %v", err)
	}
	if len(result.Removed) != 3 {
		t.Errorf("would-remove = %d, want 3", len(result.Removed))
	}
	for _, e := range result.Removed {
		if _, err := os.Stat(e.Path); err != nil {
			t.Errorf("dry-run touched disk for %q: %v", e.Path, err)
		}
	}
}

func TestPlanKeepNegativeMeansUnlimited(t *testing.T) {
	root, _ := gcFixture(t, 6, time.Now().Add(-6*time.Minute), time.Minute)

	plan, err := Plan(root, GCOptions{Keep: -1})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Removed) != 0 {
		t.Errorf("removed = %d, want 0 (unlimited keep)", len(plan.Removed))
	}
	if len(plan.Kept) != 6 {
		t.Errorf("kept = %d, want 6", len(plan.Kept))
	}
}

func TestRunGCBytesRemovedTracksSize(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "toolchain"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "toolchain", "main.osty"), []byte("// stub\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	now := time.Now()
	sizes := []int{100, 200, 300}
	for i, size := range sizes {
		name := strings.Repeat(string('a'+rune(i)), 64) + "-" + HostTriple()
		dir := filepath.Join(root, CacheDirName, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir entry: %v", err)
		}
		bin := filepath.Join(dir, BinaryName())
		body := make([]byte, size)
		if err := os.WriteFile(bin, body, 0o755); err != nil {
			t.Fatalf("write: %v", err)
		}
		mt := now.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(bin, mt, mt); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	result, err := RunGC(root, GCOptions{Keep: 1, DryRun: true})
	if err != nil {
		t.Fatalf("RunGC: %v", err)
	}
	// Smallest two entries removed → 100 + 200 = 300 bytes.
	if result.BytesRemoved != 300 {
		t.Errorf("BytesRemoved = %d, want 300", result.BytesRemoved)
	}
}

// TestPlanOrderIsStable confirms repeated Plan calls return entries
// in the same order so a `--dry-run` followed by a real run prunes
// the same set even when mtimes tie.
func TestPlanOrderIsStable(t *testing.T) {
	root, _ := gcFixture(t, 5, time.Now().Add(-5*time.Minute), time.Minute)

	first, err := Plan(root, GCOptions{Keep: 2})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	second, err := Plan(root, GCOptions{Keep: 2})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	keys := func(entries []Entry) []string {
		out := make([]string, len(entries))
		for i, e := range entries {
			out[i] = e.Key
		}
		sort.Strings(out)
		return out
	}
	if got, want := keys(first.Removed), keys(second.Removed); !equalSlice(got, want) {
		t.Errorf("removed order drifted:\n  first  %v\n  second %v", got, want)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
