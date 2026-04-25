package lsp

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	ostyquery "github.com/osty/osty/internal/query/osty"
)

// TestPackageEngineCachesSiblingParses verifies that editing one file
// in a package does not re-parse unchanged siblings. The sibling's
// Parse query should hit its cached slot because SourceText was not
// updated for that file.
func TestPackageEngineCachesSiblingParses(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.osty")
	helperPath := filepath.Join(dir, "helper.osty")

	mainSrc := []byte("fn main() { helper() }\n")
	helperSrc := []byte("fn helper() -> Int { 42 }\n")

	if err := os.WriteFile(mainPath, mainSrc, 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	if err := os.WriteFile(helperPath, helperSrc, 0o644); err != nil {
		t.Fatalf("write helper: %v", err)
	}

	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	// First analysis — cold start, everything misses.
	a := s.analyzePackageContaining(mainPath, mainSrc)
	if a == nil {
		t.Fatal("first analyze returned nil")
	}

	// Second analysis with identical source — engine should hit
	// cached slots for every query, producing zero misses.
	before := s.engine.DB.Metrics()
	a2 := s.analyzePackageContaining(mainPath, mainSrc)
	if a2 == nil {
		t.Fatal("second analyze returned nil")
	}
	after := s.engine.DB.Metrics().Sub(before)

	if after.Misses != 0 {
		t.Errorf("repeated same-source package analyze should not miss: %+v", after)
	}
	if after.Hits == 0 {
		t.Errorf("repeated same-source package analyze should produce hits: %+v", after)
	}
}

// TestPackageEngineCutoffOnWhitespaceEdit verifies that a whitespace-
// only edit to one file in a package triggers a Parse rerun for that
// file but lets downstream queries (ResolvePackage, CheckPackage,
// LintFile) cut off early when the semantic output is unchanged.
func TestPackageEngineCutoffOnWhitespaceEdit(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.osty")
	helperPath := filepath.Join(dir, "helper.osty")

	mainSrc := []byte("fn main() { helper() }\n")
	helperSrc := []byte("fn helper() -> Int { 42 }\n")

	if err := os.WriteFile(mainPath, mainSrc, 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	if err := os.WriteFile(helperPath, helperSrc, 0o644); err != nil {
		t.Fatalf("write helper: %v", err)
	}

	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	// Warm up the engine.
	_ = s.analyzePackageContaining(mainPath, mainSrc)

	// Add a trailing newline to main.osty — byte-different but
	// semantically identical to the resolver.
	mainPlusNewline := append([]byte(nil), mainSrc...)
	mainPlusNewline = append(mainPlusNewline, '\n')

	before := s.engine.DB.Metrics()
	_ = s.analyzePackageContaining(mainPath, mainPlusNewline)
	after := s.engine.DB.Metrics().Sub(before)

	// Parse should rerun because SourceText bytes changed.
	if after.Reruns == 0 {
		t.Errorf("expected at least Parse to rerun on byte diff: %+v", after)
	}
	// ResolvePackage / CheckPackage / LintFile should cut off because
	// the semantic output hash matches the previous run.
	if after.Cutoffs == 0 {
		t.Errorf("expected at least one cutoff on whitespace-only edit: %+v", after)
	}
	t.Logf("whitespace-edit metrics: %+v", after)
}

// TestPackageEngineRerunsOnSemanticEdit verifies the flip side: adding
// a new top-level declaration forces re-runs all the way through
// CheckFile because the resolver's output has genuinely changed.
func TestPackageEngineRerunsOnSemanticEdit(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.osty")
	helperPath := filepath.Join(dir, "helper.osty")

	mainSrc := []byte("fn main() { helper() }\n")
	helperSrc := []byte("fn helper() -> Int { 42 }\n")

	if err := os.WriteFile(mainPath, mainSrc, 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	if err := os.WriteFile(helperPath, helperSrc, 0o644); err != nil {
		t.Fatalf("write helper: %v", err)
	}

	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	// Warm up.
	_ = s.analyzePackageContaining(mainPath, mainSrc)

	// Add a new function — genuinely different semantics.
	editedMain := []byte("fn main() { helper() }\nfn extra() -> Int { 99 }\n")

	before := s.engine.DB.Metrics()
	_ = s.analyzePackageContaining(mainPath, editedMain)
	after := s.engine.DB.Metrics().Sub(before)

	if after.Reruns == 0 {
		t.Errorf("expected reruns after adding a new decl: %+v", after)
	}
	t.Logf("semantic-edit metrics: %+v", after)
}

// TestPackageEngineHelperFileCachedOnMainEdit verifies that editing
// main.osty does not re-parse helper.osty. The helper's Parse slot
// should remain verified because its SourceText was not updated.
func TestPackageEngineHelperFileCachedOnMainEdit(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.osty")
	helperPath := filepath.Join(dir, "helper.osty")

	mainSrc := []byte("fn main() { helper() }\n")
	helperSrc := []byte("fn helper() -> Int { 42 }\n")

	if err := os.WriteFile(mainPath, mainSrc, 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	if err := os.WriteFile(helperPath, helperSrc, 0o644); err != nil {
		t.Fatalf("write helper: %v", err)
	}

	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
	helperKey := ostyquery.NormalizePath(helperPath)

	// Warm up the engine so both files are parsed.
	_ = s.analyzePackageContaining(mainPath, mainSrc)

	// Confirm helper has been parsed.
	pr := s.engine.Queries.Parse.Get(s.engine.DB, helperKey)
	if pr.File == nil {
		t.Fatal("helper Parse returned nil after warm-up")
	}

	// Edit main — this should not invalidate helper's Parse slot.
	editedMain := []byte("fn main() { helper() + 1 }\n")
	_ = s.analyzePackageContaining(mainPath, editedMain)

	// Re-fetch helper's Parse result. The engine should hit the cache
	// because helper's SourceText was never updated.
	before := s.engine.DB.Metrics()
	pr2 := s.engine.Queries.Parse.Get(s.engine.DB, helperKey)
	after := s.engine.DB.Metrics().Sub(before)

	if pr2.File == nil {
		t.Fatal("helper Parse returned nil after main edit")
	}
	if after.Misses != 0 || after.Reruns != 0 {
		t.Errorf("helper Parse should have been a pure hit: %+v", after)
	}
	if after.Hits == 0 {
		t.Errorf("expected at least one hit for cached helper: %+v", after)
	}
}

// TestPackageEngineReturnsPackages verifies that the engine-based
// package path populates the packages field for cross-file handlers
// (references, rename, workspaceSymbol).
func TestPackageEngineReturnsPackages(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.osty")
	helperPath := filepath.Join(dir, "helper.osty")

	mainSrc := []byte("fn main() { helper() }\n")
	helperSrc := []byte("fn helper() -> Int { 42 }\n")

	if err := os.WriteFile(mainPath, mainSrc, 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	if err := os.WriteFile(helperPath, helperSrc, 0o644); err != nil {
		t.Fatalf("write helper: %v", err)
	}

	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	a := s.analyzePackageContaining(mainPath, mainSrc)
	if a == nil {
		t.Fatal("analyze returned nil")
	}
	if len(a.packages) != 1 {
		t.Fatalf("packages = %d, want 1", len(a.packages))
	}
	pkg := a.packages[0]
	if pkg == nil {
		t.Fatal("packages[0] is nil")
	}
	// The package should contain both files.
	if len(pkg.Files) != 2 {
		t.Fatalf("package has %d files, want 2", len(pkg.Files))
	}
}

// TestPackageEngineFallbackOnEmptyDir verifies that the engine path
// returns nil when the directory has no .osty files, allowing the
// caller to fall through to single-file mode.
func TestPackageEngineFallbackOnEmptyDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")

	// Write a file but do NOT write a sibling — dirHasOstySiblings
	// returns false so analyzePackageContaining should return nil.
	if err := os.WriteFile(path, []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}

	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	a := s.analyzePackageContaining(path, []byte("fn main() {}\n"))
	if a != nil {
		t.Fatal("expected nil when no siblings exist (should fall back to single-file mode)")
	}
}

// TestPackageEngineSeedsDiskContent verifies that the engine path
// reads sibling file content from disk when the sibling is not open
// in the editor.
func TestPackageEngineSeedsDiskContent(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.osty")
	helperPath := filepath.Join(dir, "helper.osty")

	mainSrc := []byte("fn main() { helper() }\n")
	helperSrc := []byte("fn helper() -> Int { 42 }\n")

	if err := os.WriteFile(mainPath, mainSrc, 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	if err := os.WriteFile(helperPath, helperSrc, 0o644); err != nil {
		t.Fatalf("write helper: %v", err)
	}

	s := NewServer(bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})

	// Analyze with an edited main — helper is not open in the editor
	// so its content should come from disk.
	editedMain := []byte("fn main() { helper() + 1 }\n")
	a := s.analyzePackageContaining(mainPath, editedMain)
	if a == nil {
		t.Fatal("analyze returned nil")
	}

	// Verify helper's SourceText was seeded from disk by checking the
	// Parse result for the helper file.
	helperKey := ostyquery.NormalizePath(helperPath)
	pr := s.engine.Queries.Parse.Get(s.engine.DB, helperKey)
	if pr.File == nil {
		t.Fatal("helper Parse returned nil — disk content may not have been seeded")
	}
}
