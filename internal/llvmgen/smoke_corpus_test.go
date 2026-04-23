package llvmgen

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestSmokeExecutableCorpusCoversAllFixtures(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "backend", "llvm_smoke")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read smoke fixture dir: %v", err)
	}

	want := map[string]struct{}{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".osty" {
			continue
		}
		want[entry.Name()] = struct{}{}
	}

	got := map[string]struct{}{}
	for _, tc := range SmokeExecutableCorpus() {
		if _, exists := got[tc.Fixture]; exists {
			t.Fatalf("duplicate smoke corpus fixture %q", tc.Fixture)
		}
		got[tc.Fixture] = struct{}{}
	}

	var missing []string
	for name := range want {
		if _, ok := got[name]; !ok {
			missing = append(missing, name)
		}
	}

	var extra []string
	for name := range got {
		if _, ok := want[name]; !ok {
			extra = append(extra, name)
		}
	}

	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) != 0 || len(extra) != 0 {
		t.Fatalf("smoke corpus mismatch: missing=%v extra=%v", missing, extra)
	}
}
