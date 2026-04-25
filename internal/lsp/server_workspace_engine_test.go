package lsp

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	ostyquery "github.com/osty/osty/internal/query/osty"
)

// TestWorkspaceEngineCachesRepeatedEdits verifies that re-analyzing the
// same source in a workspace hits every cached slot — zero misses, zero
// reruns, all hits.
func TestWorkspaceEngineCachesRepeatedEdits(t *testing.T) {
	_, appDir, _ := setupWorkspace(t)
	mainPath := filepath.Join(appDir, "main.osty")
	mainSrc := []byte("fn main() { 1 }\n")

	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	// First analysis — cold start.
	a := s.analyzePackageContaining(mainPath, mainSrc)
	if a == nil {
		t.Fatal("first analyze returned nil")
	}

	// Second analysis with identical source — pure cache hits.
	before := s.engine.DB.Metrics()
	a2 := s.analyzePackageContaining(mainPath, mainSrc)
	if a2 == nil {
		t.Fatal("second analyze returned nil")
	}
	after := s.engine.DB.Metrics().Sub(before)

	if after.Misses != 0 {
		t.Errorf("repeated same-source workspace analyze should not miss: %+v", after)
	}
	if after.Reruns != 0 {
		t.Errorf("repeated same-source workspace analyze should not rerun: %+v", after)
	}
	if after.Hits == 0 {
		t.Errorf("repeated same-source workspace analyze should produce hits: %+v", after)
	}
}

// TestWorkspaceEngineCutoffOnWhitespaceEdit verifies that a whitespace-
// only edit to one file in a workspace triggers a Parse rerun for that
// file but lets downstream queries (ResolveWorkspace, CheckWorkspace)
// cut off early when the semantic output is unchanged.
func TestWorkspaceEngineCutoffOnWhitespaceEdit(t *testing.T) {
	_, appDir, _ := setupWorkspace(t)
	mainPath := filepath.Join(appDir, "main.osty")
	mainSrc := []byte("fn main() { 1 }\n")

	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	// Warm up the engine.
	_ = s.analyzePackageContaining(mainPath, mainSrc)

	// Add a trailing newline — byte-different but semantically identical.
	mainPlusNL := append([]byte(nil), mainSrc...)
	mainPlusNL = append(mainPlusNL, '\n')

	before := s.engine.DB.Metrics()
	_ = s.analyzePackageContaining(mainPath, mainPlusNL)
	after := s.engine.DB.Metrics().Sub(before)

	if after.Reruns == 0 {
		t.Errorf("expected at least Parse to rerun on byte diff: %+v", after)
	}
	if after.Cutoffs == 0 {
		t.Errorf("expected at least one cutoff on whitespace-only edit: %+v", after)
	}
	t.Logf("workspace whitespace-edit metrics: %+v", after)
}

// TestWorkspaceEngineRerunsOnSemanticEdit verifies the flip side: adding
// a new top-level declaration in one package forces re-runs through
// ResolveWorkspace because the resolver's output has genuinely changed.
func TestWorkspaceEngineRerunsOnSemanticEdit(t *testing.T) {
	_, appDir, _ := setupWorkspace(t)
	mainPath := filepath.Join(appDir, "main.osty")
	mainSrc := []byte("fn main() { 1 }\n")

	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	// Warm up.
	_ = s.analyzePackageContaining(mainPath, mainSrc)

	// Add a new function — genuinely different semantics.
	editedMain := []byte("fn main() { 1 }\nfn extra() -> Int { 99 }\n")

	before := s.engine.DB.Metrics()
	_ = s.analyzePackageContaining(mainPath, editedMain)
	after := s.engine.DB.Metrics().Sub(before)

	if after.Reruns == 0 {
		t.Errorf("expected reruns after adding a new decl: %+v", after)
	}
	t.Logf("workspace semantic-edit metrics: %+v", after)
}

// TestWorkspaceEngineOtherPackageCachedOnEdit verifies that editing
// app/main.osty does not re-parse lib/util.osty. The other package's
// Parse slot should remain verified because its SourceText was not
// updated.
func TestWorkspaceEngineOtherPackageCachedOnEdit(t *testing.T) {
	_, appDir, libDir := setupWorkspace(t)
	mainPath := filepath.Join(appDir, "main.osty")
	utilPath := filepath.Join(libDir, "util.osty")
	mainSrc := []byte("fn main() { 1 }\n")

	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
	utilKey := ostyquery.NormalizePath(utilPath)

	// Warm up the engine so both packages are parsed.
	_ = s.analyzePackageContaining(mainPath, mainSrc)

	// Confirm util has been parsed.
	pr := s.engine.Queries.Parse.Get(s.engine.DB, utilKey)
	if pr.File == nil {
		t.Fatal("util Parse returned nil after warm-up")
	}

	// Edit main — this should not invalidate util's Parse slot.
	editedMain := []byte("fn main() { 2 }\n")
	_ = s.analyzePackageContaining(mainPath, editedMain)

	// Re-fetch util's Parse result — should be a pure hit.
	before := s.engine.DB.Metrics()
	pr2 := s.engine.Queries.Parse.Get(s.engine.DB, utilKey)
	after := s.engine.DB.Metrics().Sub(before)

	if pr2.File == nil {
		t.Fatal("util Parse returned nil after main edit")
	}
	if after.Misses != 0 || after.Reruns != 0 {
		t.Errorf("util Parse should have been a pure hit: %+v", after)
	}
	if after.Hits == 0 {
		t.Errorf("expected at least one hit for cached util: %+v", after)
	}
}

// TestWorkspaceEngineReturnsAllPackages verifies that the engine-based
// workspace path populates the packages field with every loaded package
// (both app and lib), not just the one containing the edited file.
func TestWorkspaceEngineReturnsAllPackages(t *testing.T) {
	_, appDir, _ := setupWorkspace(t)
	mainPath := filepath.Join(appDir, "main.osty")
	mainSrc := []byte("fn main() { 1 }\n")

	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	a := s.analyzePackageContaining(mainPath, mainSrc)
	if a == nil {
		t.Fatal("analyze returned nil")
	}
	if len(a.packages) < 2 {
		t.Fatalf("packages = %d, want >= 2 (app + lib)", len(a.packages))
	}

	// Verify each package has files.
	for i, pkg := range a.packages {
		if pkg == nil {
			t.Fatalf("packages[%d] is nil", i)
		}
		if len(pkg.Files) == 0 {
			t.Fatalf("packages[%d] (%s) has no files", i, pkg.Dir)
		}
	}
}

// TestWorkspaceEngineSeedsDiskContent verifies that the engine path
// reads file content from disk for files that are not open in the
// editor (i.e. the lib package).
func TestWorkspaceEngineSeedsDiskContent(t *testing.T) {
	_, appDir, libDir := setupWorkspace(t)
	mainPath := filepath.Join(appDir, "main.osty")
	utilPath := filepath.Join(libDir, "util.osty")

	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	// Analyze with edited main — util is not open in the editor
	// so its content should come from disk.
	editedMain := []byte("fn main() { 2 }\n")
	a := s.analyzePackageContaining(mainPath, editedMain)
	if a == nil {
		t.Fatal("analyze returned nil")
	}

	// Verify util's SourceText was seeded from disk.
	utilKey := ostyquery.NormalizePath(utilPath)
	pr := s.engine.Queries.Parse.Get(s.engine.DB, utilKey)
	if pr.File == nil {
		t.Fatal("util Parse returned nil — disk content may not have been seeded")
	}
}

// TestWorkspaceEngineWorkspaceMembersSeeded verifies that the engine's
// WorkspaceMembers input is populated after a workspace analysis, so
// subsequent edits within the same workspace reuse the same member set
// without re-scanning the filesystem.
func TestWorkspaceEngineWorkspaceMembersSeeded(t *testing.T) {
	_, appDir, _ := setupWorkspace(t)
	mainPath := filepath.Join(appDir, "main.osty")
	mainSrc := []byte("fn main() { 1 }\n")

	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	// First analysis should seed WorkspaceMembers.
	_ = s.analyzePackageContaining(mainPath, mainSrc)

	if !s.engine.Inputs.WorkspaceMembers.Has(s.engine.DB, struct{}{}) {
		t.Fatal("WorkspaceMembers should be seeded after workspace analysis")
	}
}

// TestWorkspaceEngineCrossPackageCutoff verifies that a non-semantic
// edit to a file in one package doesn't force re-resolution of packages
// that don't import it. When app/main.osty gets a whitespace edit, the
// lib package's ResolveWorkspace contribution should remain cached.
func TestWorkspaceEngineCrossPackageCutoff(t *testing.T) {
	_, appDir, _ := setupWorkspace(t)
	mainPath := filepath.Join(appDir, "main.osty")
	mainSrc := []byte("fn main() { 1 }\n")

	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	// Warm up.
	_ = s.analyzePackageContaining(mainPath, mainSrc)

	// Whitespace-only edit to main.
	mainPlusNL := append([]byte(nil), mainSrc...)
	mainPlusNL = append(mainPlusNL, '\n')

	before := s.engine.DB.Metrics()
	_ = s.analyzePackageContaining(mainPath, mainPlusNL)
	after := s.engine.DB.Metrics().Sub(before)

	// The ResolveWorkspace and CheckWorkspace queries should cut off
	// because the semantic output is unchanged.
	if after.Cutoffs == 0 {
		t.Errorf("expected cutoffs on whitespace-only workspace edit: %+v", after)
	}
	t.Logf("cross-package cutoff metrics: %+v", after)
}

// ---- helpers ----

// setupWorkspace creates a minimal two-package workspace for testing:
//
//	$tmpdir/
//	  app/
//	    main.osty   — "fn main() { 1 }"
//	  lib/
//	    util.osty   — "fn helper() -> Int { 42 }"
//
// Returns (root, appDir, libDir).
func setupWorkspace(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	appDir := filepath.Join(root, "app")
	libDir := filepath.Join(root, "lib")

	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("mkdir app: %v", err)
	}
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatalf("mkdir lib: %v", err)
	}

	mainPath := filepath.Join(appDir, "main.osty")
	utilPath := filepath.Join(libDir, "util.osty")
	if err := os.WriteFile(mainPath, []byte("fn main() { 1 }\n"), 0o644); err != nil {
		t.Fatalf("write app/main.osty: %v", err)
	}
	if err := os.WriteFile(utilPath, []byte("fn helper() -> Int { 42 }\n"), 0o644); err != nil {
		t.Fatalf("write lib/util.osty: %v", err)
	}

	return root, appDir, libDir
}
