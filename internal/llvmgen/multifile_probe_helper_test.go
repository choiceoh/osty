package llvmgen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsBootstrapOnlyOstyFile(t *testing.T) {
	t.Run("detects real use go ffi", func(t *testing.T) {
		src := []byte("use go \"strings\" as strings {\n    pub fn trimSpace(s: String) -> String\n}\n")
		if !isBootstrapOnlyOstyFile(src) {
			t.Fatalf("expected real use-go file to be classified as bootstrap-only")
		}
	})

	t.Run("detects real runtime golegacy ffi", func(t *testing.T) {
		src := []byte("use runtime.golegacy.astbridge as astbridge {\n    pub fn pos() -> Int\n}\n")
		if !isBootstrapOnlyOstyFile(src) {
			t.Fatalf("expected runtime.golegacy file to be classified as bootstrap-only")
		}
	})

	t.Run("detects real runtime cihost ffi", func(t *testing.T) {
		src := []byte("use runtime.cihost as host {\n    pub fn LoadRunnerState(root: String) -> Bool\n}\n")
		if !isBootstrapOnlyOstyFile(src) {
			t.Fatalf("expected runtime.cihost file to be classified as bootstrap-only")
		}
	})

	t.Run("ignores comments mentioning bootstrap syntax", func(t *testing.T) {
		src := []byte("// `use go \"...\" { ... }` stays in comments only.\n// `use runtime.golegacy.foo` is also documentation here.\n// `use runtime.cihost as host` too.\npub fn keep() -> Int { 1 }\n")
		if isBootstrapOnlyOstyFile(src) {
			t.Fatalf("expected comment-only mentions to stay in the native merged set")
		}
	})
}

func TestCollectToolchainProbeFilesSkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".osty"), 0o755); err != nil {
		t.Fatalf("mkdir .osty: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "alpha.osty"), []byte("pub fn alpha() -> Int { 1 }\n"), 0o644); err != nil {
		t.Fatalf("write alpha.osty: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "beta_test.osty"), []byte("pub fn beta() -> Int { 2 }\n"), 0o644); err != nil {
		t.Fatalf("write beta_test.osty: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore\n"), 0o644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}

	files, skipped, err := collectToolchainProbeFiles(dir, false)
	if err != nil {
		t.Fatalf("collectToolchainProbeFiles: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipBootstrapOnly=false unexpectedly skipped files: %v", skipped)
	}
	if got, want := len(files), 1; got != want {
		t.Fatalf("len(files) = %d, want %d (%v)", got, want, files)
	}
	if got, want := files[0], "alpha.osty"; got != want {
		t.Fatalf("files[0] = %q, want %q", got, want)
	}
}
