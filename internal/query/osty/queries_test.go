package osty

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/cst"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/lint"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/spanid"
	"github.com/osty/osty/internal/token"
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
	if pr.Run == nil {
		t.Fatal("parse returned nil FrontendRun")
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
	if pr2.Run == nil {
		t.Fatal("second parse returned nil FrontendRun")
	}
	if after.Misses != 0 || after.Reruns != 0 {
		t.Fatalf("cached call should not miss/rerun, got %+v", after)
	}
	if after.Hits == 0 {
		t.Fatalf("cached call should hit, got %+v", after)
	}
}

func TestBuildPackagePreservesFrontendRuns(t *testing.T) {
	eng := NewEngine()
	defer eng.Close()

	dir := NormalizePath("/tmp/pkg_run")
	_ = seedFile(eng, dir, "main.osty", "pub fn main() { }\n")

	pkg := eng.Queries.BuildPackage.Get(eng.DB, dir)
	if pkg == nil || len(pkg.Files) != 1 {
		t.Fatalf("BuildPackage returned %#v, want one file", pkg)
	}
	if pkg.Files[0].Run == nil {
		t.Fatal("BuildPackage dropped FrontendRun; native consumers would be forced back through public AST")
	}
	if pkg.Files[0].File == nil {
		t.Fatal("BuildPackage should still expose public AST compatibility output")
	}
}

func TestLintFileDependsOnlyOnSourceText(t *testing.T) {
	eng := NewEngine()
	defer eng.Close()

	path := seedFile(eng, "/tmp/pkg_lint_source", "main.osty", "pub fn main() { }\n")

	before := eng.DB.Metrics()
	_ = eng.Queries.LintFile.Get(eng.DB, path)
	after := eng.DB.Metrics().Sub(before)
	if after.Misses != 1 {
		t.Fatalf("LintFile should compute without fetching Parse/Resolve/Check, got %+v", after)
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

func TestParseCSTQueryReturnsNestedLosslessTree(t *testing.T) {
	eng := NewEngine()
	defer eng.Close()

	path := seedFile(eng, "/tmp/pkg_cst", "main.osty", "fn main() {\n    let x = add(1, 2)\n}\n")

	before := eng.DB.Metrics()
	res := eng.Queries.ParseCST.Get(eng.DB, path)
	after := eng.DB.Metrics().Sub(before)
	if after.Misses == 0 {
		t.Fatal("expected a miss on first ParseCST")
	}
	if res.Tree == nil {
		t.Fatal("ParseCST returned nil tree")
	}
	if string(res.Tree.Source) != string(res.Source) {
		t.Fatalf("tree source and query source differ: tree=%q query=%q", res.Tree.Source, res.Source)
	}

	var sawLet, sawCall bool
	res.Tree.Root().Walk(func(r cst.Red) bool {
		if r.Kind() == cst.GkLetStmt {
			sawLet = true
		}
		if r.Kind() == cst.GkCall {
			sawCall = true
		}
		return true
	})
	if !sawLet || !sawCall {
		t.Fatalf("ParseCST tree missing nested nodes: sawLet=%v sawCall=%v", sawLet, sawCall)
	}

	before = eng.DB.Metrics()
	again := eng.Queries.ParseCST.Get(eng.DB, path)
	after = eng.DB.Metrics().Sub(before)
	if again.Tree == nil {
		t.Fatal("cached ParseCST returned nil tree")
	}
	if after.Misses != 0 || after.Reruns != 0 || after.Hits == 0 {
		t.Fatalf("cached ParseCST should hit without miss/rerun, got %+v", after)
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

func TestParseDiagnosticsCarrySourceFileIdentity(t *testing.T) {
	eng := NewEngine()
	defer eng.Close()

	path := seedFile(eng, "/tmp/pkg_span_parse", "main.osty", "pub fn {")
	pr := eng.Queries.Parse.Get(eng.DB, path)
	if len(pr.Diags) == 0 {
		t.Fatal("expected parse diagnostics")
	}
	if pr.SourceFileID == "" {
		t.Fatal("parse result SourceFileID is empty")
	}
	got := pr.Diags[0]
	if got.File != path {
		t.Fatalf("diagnostic file = %q, want %q", got.File, path)
	}
	span, ok := got.PrimarySpan()
	if !ok {
		t.Fatal("diagnostic missing primary span")
	}
	if span.SourceFileID != pr.SourceFileID || span.ID == "" {
		t.Fatalf("diagnostic span identity = (%q, %q), want file %q and non-empty span", span.SourceFileID, span.ID, pr.SourceFileID)
	}
}

func TestCollectFileDiagnosticsFiltersByFileIdentity(t *testing.T) {
	pathA := NormalizePath("/tmp/pkg_span_filter/a.osty")
	pathB := NormalizePath("/tmp/pkg_span_filter/b.osty")
	pos := token.Pos{Offset: 4, Line: 1, Column: 5}
	spanA := diag.StampSpanSourceFileID(
		diag.Span{Start: pos, End: token.Pos{Offset: 5, Line: 1, Column: 6}},
		spanid.SourceFileIDFor(pathA),
	)
	spanB := diag.StampSpanSourceFileID(
		diag.Span{Start: pos, End: token.Pos{Offset: 5, Line: 1, Column: 6}},
		spanid.SourceFileIDFor(pathB),
	)
	resolveDiags := []*diag.Diagnostic{
		diag.New(diag.Error, "belongs to a by span id").Primary(spanA, "").Build(),
		diag.New(diag.Error, "belongs to b by file").File(pathB).Primary(spanB, "").Build(),
	}
	chk := &check.Result{}
	lr := &lint.Result{}

	got := collectFileDiagnostics(pathA, ParseResult{}, resolveDiags, chk, lr)
	if len(got) != 1 {
		t.Fatalf("diagnostic count = %d, want 1: %#v", len(got), got)
	}
	if got[0].Message != "belongs to a by span id" {
		t.Fatalf("diagnostic = %q, want span-id-routed diagnostic", got[0].Message)
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
	src := "pub fn foo() -> Int { 1 }\nfn main() { foo() }\n"
	path := seedFile(eng, "/tmp/pkg_ii", "main.osty", src)

	selfhost.ResetAstbridgeLowerCount()
	idx := eng.Queries.IdentIndex.Get(eng.DB, path)
	if idx == nil {
		t.Fatal("IdentIndex returned nil")
	}
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("IdentIndex touched astbridge = %d, want 0", got)
	}
	callOff := strings.LastIndex(src, "foo")
	if callOff < 0 {
		t.Fatal("test source missing foo call")
	}
	if got := idx[callOff]; got != "function" {
		t.Fatalf("IdentIndex[%d] = %q, want function (idx=%#v)", callOff, got, idx)
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

func TestResolveDiagnosticsUseNativeStructuredResult(t *testing.T) {
	eng := NewEngine()
	defer eng.Close()

	path := seedFile(eng, "/tmp/pkg_resolve_diags", "main.osty", "fn main() { missing() }\n")

	selfhost.ResetAstbridgeLowerCount()
	diags := eng.Queries.ResolveDiagnostics.Get(eng.DB, path)
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("ResolveDiagnostics touched astbridge = %d, want 0", got)
	}
	var sawUndefined bool
	for _, d := range diags {
		if d != nil && d.Code == diag.CodeUndefinedName {
			sawUndefined = true
			if d.File != path {
				t.Fatalf("diagnostic file = %q, want %q", d.File, path)
			}
		}
	}
	if !sawUndefined {
		t.Fatalf("ResolveDiagnostics = %#v, want undefined-name diagnostic", diags)
	}
}

func TestSemanticDBAccessorsExposeResolveAndCheckFacts(t *testing.T) {
	eng := NewEngine()
	defer eng.Close()

	dir := NormalizePath("/tmp/pkg_semanticdb")
	path := seedFile(eng, dir, "main.osty", "fn id<T>(x: T) -> T { x }\nfn main() { let value = id::<Int>(1) }\n")

	rp := eng.Queries.ResolvePackage.Get(eng.DB, dir)
	if rp == nil || rp.SemanticDB() == nil {
		t.Fatalf("ResolvePackage SemanticDB = %#v, want non-nil", rp)
	}
	if rp.SemanticDB().FileForPath(path) == nil {
		t.Fatalf("SemanticDB missing file %q", path)
	}

	chk := eng.Queries.CheckFile.Get(eng.DB, path)
	if chk == nil || chk.Semantic() == nil {
		t.Fatalf("CheckFile SemanticDB = %#v, want non-nil", chk)
	}
	if chk.Semantic().Check == nil {
		t.Fatal("combined SemanticDB missing checker facts")
	}
	if len(chk.Semantic().CheckIndex().InstantiationsByStableID) == 0 {
		t.Fatalf("combined SemanticDB instantiations = %#v, want generic call facts", chk.Semantic().Check.Instantiations)
	}
}

func TestLowerIRAndMIRPackageQueries(t *testing.T) {
	eng := NewEngine()
	defer eng.Close()

	dir := NormalizePath(t.TempDir())
	path := seedFile(eng, dir, "main.osty", "pub fn main() { }\n")
	key := LowerKey{Dir: dir, PackageName: "main", EntryPath: path}.normalized()

	irRes := eng.Queries.LowerIRPackage.Get(eng.DB, key)
	if irRes.Err != nil {
		t.Fatalf("LowerIRPackage returned error: %v", irRes.Err)
	}
	if irRes.Entry.IR == nil {
		t.Fatal("LowerIRPackage returned nil IR")
	}
	if irRes.Entry.MIR != nil {
		t.Fatal("LowerIRPackage should stop before MIR lowering")
	}

	mirRes := eng.Queries.LowerMIRPackage.Get(eng.DB, key)
	if mirRes.Err != nil {
		t.Fatalf("LowerMIRPackage returned error: %v", mirRes.Err)
	}
	if mirRes.Entry.IR == nil {
		t.Fatal("LowerMIRPackage dropped IR")
	}
	if mirRes.Entry.MIR == nil {
		t.Fatal("LowerMIRPackage returned nil MIR")
	}
}

func TestLowerMIRPackageReusesCacheAfterSemanticCutoff(t *testing.T) {
	eng := NewEngine()
	defer eng.Close()

	dir := NormalizePath(t.TempDir())
	path := seedFile(eng, dir, "main.osty", "pub fn main() { }\n")
	key := LowerKey{Dir: dir, PackageName: "main", EntryPath: path}.normalized()

	primed := eng.Queries.LowerMIRPackage.Get(eng.DB, key)
	if primed.Err != nil {
		t.Fatalf("LowerMIRPackage priming returned error: %v", primed.Err)
	}

	eng.Inputs.SourceText.Set(eng.DB, path, []byte("pub fn main() { }\n\n"))
	before := eng.DB.Metrics()
	again := eng.Queries.LowerMIRPackage.Get(eng.DB, key)
	after := eng.DB.Metrics().Sub(before)
	if again.Err != nil {
		t.Fatalf("LowerMIRPackage after edit returned error: %v", again.Err)
	}
	if after.Cutoffs == 0 {
		t.Fatalf("expected at least one semantic cutoff after whitespace edit, got %+v", after)
	}
	if again.Entry.IR != primed.Entry.IR {
		t.Fatal("LowerMIRPackage should reuse cached IR when semantic deps cut off")
	}
	if again.Entry.MIR != primed.Entry.MIR {
		t.Fatal("LowerMIRPackage should reuse cached MIR when semantic deps cut off")
	}
}

func TestEmitQueryWritesLLVMIRArtifact(t *testing.T) {
	eng := NewEngine()
	defer eng.Close()

	root := t.TempDir()
	dir := NormalizePath(root)
	path := seedFile(eng, dir, "main.osty", "pub fn main() { }\n")
	target := NewEmitTarget(
		LowerKey{Dir: dir, PackageName: "main", EntryPath: path},
		backend.NameLLVM,
		backend.EmitLLVMIR,
		backend.Layout{Root: root, Profile: "debug"},
		"",
		nil,
	)

	emitted := eng.Queries.Emit.Get(eng.DB, target)
	if emitted.Err != nil {
		t.Fatalf("Emit returned error: %v", emitted.Err)
	}
	if emitted.Result == nil {
		t.Fatal("Emit returned nil result")
	}
	if emitted.Result.Artifacts.LLVMIR == "" {
		t.Fatal("Emit did not report an LLVM IR artifact")
	}
	data, err := os.ReadFile(emitted.Result.Artifacts.LLVMIR)
	if err != nil {
		t.Fatalf("reading LLVM IR artifact: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("LLVM IR artifact is empty")
	}

	before := eng.DB.Metrics()
	again := eng.Queries.Emit.Get(eng.DB, target)
	after := eng.DB.Metrics().Sub(before)
	if again.Err != nil {
		t.Fatalf("cached Emit returned error: %v", again.Err)
	}
	if after.Misses != 0 || after.Reruns != 0 {
		t.Fatalf("cached Emit should not miss/rerun, got %+v", after)
	}
	if again.Result == nil || again.Result.Artifacts.LLVMIR != emitted.Result.Artifacts.LLVMIR {
		t.Fatalf("cached Emit changed artifact path: first=%#v again=%#v", emitted.Result, again.Result)
	}
}

func TestSeedLoadedWorkspacePreservesDotPathForLowering(t *testing.T) {
	root := t.TempDir()
	depDir := filepath.Join(root, "dep")
	if err := os.MkdirAll(depDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(root, "main.osty")
	if err := os.WriteFile(mainPath, []byte(`use dep

fn main() {
    dep.value()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(depDir, "lib.osty"), []byte(`pub fn value() -> Int { 1 }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ws, err := resolve.NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	if _, err := ws.LoadPackageNative(""); err != nil {
		t.Fatalf("LoadPackageNative: %v", err)
	}

	eng := NewEngine()
	defer eng.Close()
	seeded, err := eng.SeedLoadedWorkspace(ws)
	if err != nil {
		t.Fatalf("SeedLoadedWorkspace: %v", err)
	}
	rw := eng.Queries.ResolveWorkspace.Get(eng.DB, seeded.Root)
	if rw == nil {
		t.Fatal("ResolveWorkspace returned nil")
	}
	rootDir := NormalizePath(root)
	depNorm := NormalizePath(depDir)
	if got := rw.DotPathByDir(depNorm); got != "dep" {
		t.Fatalf("DotPathByDir(dep) = %q, want dep", got)
	}
	if rw.PackageByDir(rootDir) == nil || rw.PackageByDir(depNorm) == nil {
		t.Fatal("workspace query dropped root or dep package")
	}
	lowered := eng.Queries.LowerMIRPackage.Get(eng.DB, LowerKey{
		WorkspaceRoot: seeded.Root,
		Dir:           rootDir,
		PackageName:   "main",
		EntryPath:     NormalizePath(mainPath),
	})
	if lowered.Err != nil {
		t.Fatalf("LowerMIRPackage(workspace root) returned error: %v", lowered.Err)
	}
	if lowered.Entry.IR == nil || lowered.Entry.MIR == nil {
		t.Fatalf("workspace lowering missing IR/MIR: IR=%v MIR=%v", lowered.Entry.IR, lowered.Entry.MIR)
	}
}

func TestResolveWorkspaceSwitchesFromMemberFallbackToSeededPackages(t *testing.T) {
	eng := NewEngine()
	defer eng.Close()

	root := NormalizePath(t.TempDir())
	depDir := NormalizePath(t.TempDir())
	_ = seedFile(eng, root, "main.osty", "fn main() { }\n")
	_ = seedFile(eng, depDir, "lib.osty", "pub fn value() -> Int { 1 }\n")

	eng.Inputs.WorkspaceMembers.Set(eng.DB, struct{}{}, []string{root})
	rw := eng.Queries.ResolveWorkspace.Get(eng.DB, root)
	if rw == nil || rw.PackageByDir(depDir) != nil {
		t.Fatalf("fallback workspace should contain only root package, got %#v", rw)
	}

	eng.Inputs.WorkspacePackages.Set(eng.DB, root, []WorkspacePackage{
		{Dir: root, DotPath: "", Name: "main"},
		{Dir: depDir, DotPath: "dep", Name: "dep"},
	})
	rw = eng.Queries.ResolveWorkspace.Get(eng.DB, root)
	if rw == nil {
		t.Fatal("ResolveWorkspace returned nil after seeding WorkspacePackages")
	}
	if got := rw.DotPathByDir(depDir); got != "dep" {
		t.Fatalf("DotPathByDir(depDir) = %q, want dep", got)
	}
	if rw.PackageByDir(depDir) == nil {
		t.Fatal("ResolveWorkspace did not invalidate after WorkspacePackages was seeded")
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
