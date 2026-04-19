package doctest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/parser"
)

// TestToolchainDoctestCoverageSentinel pins the doctest-based parity
// signal from the self-hosted toolchain. The extractor is generic —
// this test asserts that the listed modules actually *use* it, so a
// future change that silently rips ` ```osty ``` ` blocks out of
// public `///` comments trips CI instead of quietly regressing
// coverage to zero.
//
// Floor-only: each module must carry ≥1 block. Add a module by
// appending to `requiredModules`.
func TestToolchainDoctestCoverageSentinel(t *testing.T) {
	requiredModules := []string{
		"semver.osty",
		"semver_parse.osty",
		"diagnostic.osty",
		"diag_policy.osty",
	}

	root := repoRoot(t)
	toolchainDir := filepath.Join(root, "toolchain")

	var allDocs []Doctest
	for _, name := range requiredModules {
		path := filepath.Join(toolchainDir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		file, diags := parser.ParseDiagnostics(src)
		if file == nil {
			t.Fatalf("parse %s: %v", path, diags)
		}
		docs := Extract(file)
		if len(docs) == 0 {
			t.Errorf("toolchain/%s lost its doctest coverage (expected ≥1, got 0)", name)
		}
		allDocs = append(allDocs, docs...)
	}

	// Parse-regression guard: a block that extracts but whose body
	// breaks the parser turns `osty test --doc toolchain` into a hard
	// failure. Catch that here via the shared runner builder.
	_, diags := parser.ParseDiagnostics(BuildRunnerSource(allDocs))
	for _, d := range diags {
		if d != nil && d.Severity.String() == "error" {
			t.Errorf("synthesised doctest runner has parse error: %s", d.Message)
		}
	}
}

// repoRoot walks up from the package directory to the repo root (the
// directory containing `go.mod`).
func repoRoot(t *testing.T) string {
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
			t.Fatalf("go.mod not found walking up from package dir")
		}
		dir = parent
	}
}
