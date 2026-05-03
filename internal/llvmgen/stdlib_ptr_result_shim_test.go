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
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
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

func TestManualPtrBackedResultBlocksStayInventoried(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	dir := filepath.Join(root, "internal", "llvmgen")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read internal/llvmgen: %v", err)
	}

	allowed := map[string][]string{
		"stdlib_regex_shim.go": {
			"regex.compile Result must be ptr-backed",
		},
	}
	seen := map[string]bool{}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		if entry.Name() == "stdlib_ptr_result_shim.go" || entry.Name() == "stdlib_fs_shim.go" {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		text := string(src)
		count := strings.Count(text, "Result must be ptr-backed")
		if count == 0 {
			continue
		}

		markers := allowed[entry.Name()]
		if len(markers) == 0 {
			t.Fatalf("%s has an uninventoried manual ptr-backed Result block", entry.Name())
		}
		if count != len(markers) {
			t.Fatalf("%s has %d manual ptr-backed Result markers, want %d inventoried markers", entry.Name(), count, len(markers))
		}
		for _, marker := range markers {
			if !strings.Contains(text, marker) {
				t.Fatalf("%s lost inventoried manual ptr-backed Result marker %q", entry.Name(), marker)
			}
			seen[entry.Name()+"\x00"+marker] = true
		}
	}

	for filename, markers := range allowed {
		for _, marker := range markers {
			if !seen[filename+"\x00"+marker] {
				t.Fatalf("inventoried manual ptr-backed Result marker %q in %s was not found", marker, filename)
			}
		}
	}
}
