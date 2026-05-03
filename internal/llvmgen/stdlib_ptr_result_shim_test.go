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

func TestSharedPtrBackedResultHelperIsStdlibNeutral(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "internal", "llvmgen", "stdlib_ptr_result_shim.go"))
	if err != nil {
		t.Fatalf("read stdlib_ptr_result_shim.go: %v", err)
	}
	text := string(src)
	if strings.Contains(text, "emitStdFsPtrResultFromRuntimeCall(") {
		t.Fatalf("shared ptr-backed Result helper should not depend on the std.fs-specific helper")
	}
	for _, want := range []string{
		"builtinResultTypeFromAST(sourceType",
		"declareRuntimeSymbol(valueSymbol",
		"declareRuntimeSymbol(errorSymbol",
		"llvmNextLabel(emitter, prefix+\".err\")",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("shared ptr-backed Result helper lost expected implementation marker %q", want)
		}
	}
}

func TestStdFsPtrBackedResultHelperStaysFsLocal(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	dir := filepath.Join(root, "internal", "llvmgen")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read internal/llvmgen: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		text := string(src)
		count := strings.Count(text, "emitStdFsPtrResultFromRuntimeCall(")
		if entry.Name() == "stdlib_fs_shim.go" {
			continue
		}
		if count > 0 {
			t.Fatalf("%s calls the std.fs-specific ptr-backed Result helper; use emitPtrBackedResultFromRuntimeCall instead", entry.Name())
		}
	}
}
