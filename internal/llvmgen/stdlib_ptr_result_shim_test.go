package llvmgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStdUuidParseUsesSharedPtrBackedResultHelper(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "internal", "llvmgen", "stdlib_uuid_shim.go"))
	if err != nil {
		t.Fatalf("read stdlib_uuid_shim.go: %v", err)
	}
	text := string(src)
	if !strings.Contains(text, "emitPtrBackedResultFromRuntimeCall(") {
		t.Fatalf("uuid.parse no longer routes through the shared ptr-backed Result helper")
	}
	for _, forbidden := range []string{
		"uuid.parse.err",
		"uuid.parse.ok",
		"uuid.parse.cont",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("uuid.parse still carries manual Result block label %q", forbidden)
		}
	}
}
