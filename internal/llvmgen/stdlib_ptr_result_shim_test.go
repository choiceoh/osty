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

func TestStdEnvCurrentDirUsesSharedPtrBackedResultHelper(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "internal", "llvmgen", "stdlib_env_shim.go"))
	if err != nil {
		t.Fatalf("read stdlib_env_shim.go: %v", err)
	}
	text := string(src)
	if !strings.Contains(text, "emitPtrBackedResultFromRuntimeCall(\n\t\t\"env.current_dir\"") {
		t.Fatalf("env.currentDir no longer routes through the shared ptr-backed Result helper")
	}
	for _, forbidden := range []string{
		"env.current_dir.err",
		"env.current_dir.ok",
		"env.current_dir.cont",
		"env.currentDir currently needs ptr-backed Result",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("env.currentDir still carries manual Result marker %q", forbidden)
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
		"stdlib_env_shim.go": []string{
			"env.require currently needs ptr-backed Result<String, Error>",
		},
		"stdlib_regex_shim.go": []string{
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
		count += strings.Count(text, "currently needs ptr-backed Result")
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

func TestMirFsPtrBackedResultHelperReuseStaysInventoried(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "internal", "llvmgen", "stdlib_mir_runtime_shim.go"))
	if err != nil {
		t.Fatalf("read stdlib_mir_runtime_shim.go: %v", err)
	}
	text := string(src)
	markers := []string{
		"g.emitStdFsPtrResultMIRArgs(c, \"uuid.parse\"",
		"g.emitStdFsPtrResultMIRArgs(c, \"regex.compile\"",
	}
	count := strings.Count(text, "emitStdFsPtrResultMIRArgs(")
	if count != len(markers) {
		t.Fatalf("stdlib_mir_runtime_shim.go has %d std.fs ptr-backed Result MIR helper calls, want %d inventoried calls", count, len(markers))
	}
	for _, marker := range markers {
		if !strings.Contains(text, marker) {
			t.Fatalf("stdlib_mir_runtime_shim.go lost inventoried std.fs ptr-backed Result MIR helper call %q", marker)
		}
	}
}
