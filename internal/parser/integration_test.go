package parser

import (
	"os"
	"path/filepath"
	"testing"
)

// TestParseTestdataFiles parses every .osty file under ../../testdata and
// reports any parse errors. This guards against regressions when adding new
// syntax to the sample files.
func TestParseTestdataFiles(t *testing.T) {
	// Walk up to the project root and find testdata/.
	root := findRepoRoot(t)
	pattern := filepath.Join(root, "testdata", "*.osty")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("no testdata files matched %s", pattern)
	}
	for _, path := range matches {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			_, errs := Parse(src)
			if len(errs) > 0 {
				for _, e := range errs {
					t.Errorf("%s: %s", path, e.Error())
				}
			}
		})
	}
}

// findRepoRoot finds the directory containing go.mod, starting from the
// test's working directory. Tests run in the package's source dir by
// default.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod")
		}
		dir = parent
	}
}
