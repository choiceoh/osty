package osty

import (
	"strings"
	"testing"
)

// Helper: seed one "package" with one file for tests.
func seedFile(eng *Engine, dir, name, src string) string {
	path := NormalizePath(dir + "/" + name)
	eng.Inputs.SourceText.Set(eng.DB, path, []byte(src))
	eng.Inputs.PackageFiles.Set(eng.DB, NormalizePath(dir), []string{path})
	return path
}

func TestParseMissThenHit(t *testing.T) {
	eng := NewEngine()
	defer eng.Close()

	path := seedFile(eng, "/tmp/pkg", "main.osty", "pub fn main() { }")

	before := eng.DB.Metrics()
	pr := eng.Queries.Parse.Get(eng.DB, path)
	if pr.File == nil {
		t.Fatal("parse returned nil file")
	}
	after := eng.DB.Metrics().Sub(before)
	if after.Misses == 0 {
		t.Fatal("expected at least one miss on first parse")
	}

	// Second Get with same source: pure cache hit, no misses, no reruns.
	before = eng.DB.Metrics()
	pr2 := eng.Queries.Parse.Get(eng.DB, path)
	after = eng.DB.Metrics().Sub(before)
	if pr2.File == nil {
		t.Fatal("second parse returned nil file")
	}
	if after.Misses != 0 || after.Reruns != 0 {
		t.Fatalf("cached call should not miss/rerun, got %+v", after)
	}
	if after.Hits == 0 {
		t.Fatalf("cached call should hit, got %+v", after)
	}
}

func TestParseRerunsOnSourceChange(t *testing.T) {
	eng := NewEngine()
	defer eng.Close()

	path := seedFile(eng, "/tmp/pkg2", "main.osty", "pub fn a() { }")

	_ = eng.Queries.Parse.Get(eng.DB, path)

	// Change source.
	eng.Inputs.SourceText.Set(eng.DB, path, []byte("pub fn b() { }"))

	before := eng.DB.Metrics()
	_ = eng.Queries.Parse.Get(eng.DB, path)
	after := eng.DB.Metrics().Sub(before)
	if after.Reruns != 1 {
		t.Fatalf("expected 1 rerun after source change, got %+v", after)
	}
}

func TestResolveFileIndependentPaths(t *testing.T) {
	eng := NewEngine()
	defer eng.Close()

	// Two independent packages. Editing one must not invalidate the
	// other's resolve result.
	pathA := seedFile(eng, "/tmp/pkg_a", "a.osty", "pub fn a() { }")
	pathB := seedFile(eng, "/tmp/pkg_b", "b.osty", "pub fn b() { }")

	_ = eng.Queries.ResolveFile.Get(eng.DB, pathA)
	_ = eng.Queries.ResolveFile.Get(eng.DB, pathB)

	// Change only A's source.
	eng.Inputs.SourceText.Set(eng.DB, pathA, []byte("pub fn a2() { }"))

	before := eng.DB.Metrics()
	_ = eng.Queries.ResolveFile.Get(eng.DB, pathB)
	after := eng.DB.Metrics().Sub(before)
	if after.Reruns > 0 {
		t.Fatalf("B should not rerun when A changes: %+v", after)
	}
	if after.Misses > 0 {
		t.Fatalf("B should be fully cached: %+v", after)
	}
}

func TestDiagnosticsChangeOnError(t *testing.T) {
	eng := NewEngine()
	defer eng.Close()

	// Start with valid source.
	path := seedFile(eng, "/tmp/pkg_err", "main.osty", "pub fn main() { }")
	diags := eng.Queries.FileDiagnostics.Get(eng.DB, path)
	validDiagCount := len(diags)

	// Inject a parse error.
	eng.Inputs.SourceText.Set(eng.DB, path, []byte("pub fn {"))
	diags = eng.Queries.FileDiagnostics.Get(eng.DB, path)
	if len(diags) <= validDiagCount {
		t.Fatalf("expected new diagnostics after parse error; before=%d after=%d",
			validDiagCount, len(diags))
	}
}

// Whitespace-only edit: the AST shape may differ, the source bytes
// differ, but the resolver's output should be structurally identical
// (no symbol name / position changes). Verifies the early-cutoff
// cascade path.
func TestCutoffOnTrivialWhitespaceEdit(t *testing.T) {
	eng := NewEngine()
	defer eng.Close()

	// An unambiguous, single-symbol program. Adding a trailing
	// newline is a byte-level diff but the top-level decl list and
	// its offsets are identical.
	path := seedFile(eng, "/tmp/pkg_ws", "main.osty", "pub fn a() { }\n")

	// Prime the cache all the way to CheckFile.
	_ = eng.Queries.CheckFile.Get(eng.DB, path)

	// Add another trailing newline — bytes differ, but semantic
	// content of the decl is unchanged.
	eng.Inputs.SourceText.Set(eng.DB, path, []byte("pub fn a() { }\n\n"))

	before := eng.DB.Metrics()
	_ = eng.Queries.CheckFile.Get(eng.DB, path)
	after := eng.DB.Metrics().Sub(before)

	// Parse MUST re-run (bytes differ). ResolvePackage's semantic
	// output is identical → its hash should match → downstream
	// CheckPackage/CheckFile/LintFile stay cached via early cutoff.
	if after.Reruns == 0 {
		t.Fatal("expected at least Parse to re-run after whitespace change")
	}
	if after.Cutoffs == 0 {
		t.Fatalf("expected early-cutoff to fire for whitespace-only edit, got %+v", after)
	}
	t.Logf("whitespace-edit metrics delta: %+v", after)
}

func TestIdentIndexUpdatesWithSymbols(t *testing.T) {
	eng := NewEngine()
	defer eng.Close()
	path := seedFile(eng, "/tmp/pkg_ii", "main.osty", "pub fn foo() { }\npub fn bar() { }\n")

	idx := eng.Queries.IdentIndex.Get(eng.DB, path)
	if idx == nil {
		t.Fatal("IdentIndex returned nil")
	}

	// Cached repeat.
	before := eng.DB.Metrics()
	idx2 := eng.Queries.IdentIndex.Get(eng.DB, path)
	after := eng.DB.Metrics().Sub(before)
	if after.Misses != 0 || after.Reruns != 0 {
		t.Fatalf("second call should be cached: %+v", after)
	}
	if len(idx) != len(idx2) {
		t.Fatalf("index differs across cached calls: %d vs %d", len(idx), len(idx2))
	}
}

func TestNormalizePathIdempotent(t *testing.T) {
	samples := []string{
		"/abs/path/main.osty",
		"relative/path.osty",
		"./nested/../file.osty",
	}
	for _, s := range samples {
		once := NormalizePath(s)
		twice := NormalizePath(once)
		if once != twice {
			t.Errorf("NormalizePath not idempotent for %q: %q -> %q", s, once, twice)
		}
	}
}

func TestFromURI(t *testing.T) {
	tests := []struct {
		uri      string
		wantOK   bool
		wantPart string
	}{
		{"file:///c:/some/path/main.osty", true, "main.osty"},
		{"file:///home/user/pkg/main.osty", true, "main.osty"},
		{"untitled:Untitled-1", false, "untitled:"},
	}
	for _, tc := range tests {
		got, ok := FromURI(tc.uri)
		if ok != tc.wantOK {
			t.Errorf("FromURI(%q).ok = %v, want %v", tc.uri, ok, tc.wantOK)
		}
		if !strings.Contains(got, tc.wantPart) {
			t.Errorf("FromURI(%q) = %q, expected substring %q", tc.uri, got, tc.wantPart)
		}
	}
}
