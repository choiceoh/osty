package llvmgen

import "testing"

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

	t.Run("ignores comments mentioning bootstrap syntax", func(t *testing.T) {
		src := []byte("// `use go \"...\" { ... }` stays in comments only.\n// `use runtime.golegacy.foo` is also documentation here.\npub fn keep() -> Int { 1 }\n")
		if isBootstrapOnlyOstyFile(src) {
			t.Fatalf("expected comment-only mentions to stay in the native merged set")
		}
	})
}
