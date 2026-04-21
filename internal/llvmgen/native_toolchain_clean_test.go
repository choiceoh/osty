package llvmgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/parser"
)

// TestNativeToolchainMergedIsClean locks the milestone reached when
// the envEntryKey Option<Int> refactor landed: every non-bootstrap
// toolchain/*.osty file, concatenated into a single buffer, lowers
// to LLVM IR without a backend wall. This is the Tier A self-host
// milestone — if a future change re-introduces a wall, the test fails
// with the offending error so the regression surfaces immediately
// rather than hiding inside the info-only TestProbeNativeToolchainMerged.
//
// Paired with TestProbeNativeToolchainMerged: the probe is info-only
// and always passes (logs the first wall); this test is authoritative
// and fails on any wall.
func TestNativeToolchainMergedIsClean(t *testing.T) {
	if testing.Short() {
		t.Skip("slow (~10s); skipped in -short")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	dir := filepath.Join(root, "toolchain")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".osty") || strings.HasSuffix(name, "_test.osty") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if isBootstrapOnlyOstyFile(src) {
			continue
		}
		files = append(files, name)
	}
	merged := mergeToolchainSources(t, root, files)
	file, _ := parser.ParseDiagnostics(merged)
	if file == nil {
		t.Fatalf("merged parse returned nil (%d files, %d bytes)", len(files), len(merged))
	}
	_, err = generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/toolchain_native_merged.osty"})
	if err != nil {
		t.Fatalf("expected clean merged lowering, got wall: %v", err)
	}
}
