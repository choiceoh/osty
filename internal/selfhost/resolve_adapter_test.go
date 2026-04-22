package selfhost_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/osty/osty/internal/selfhost"
)

func TestResolveSourceStructuredRecordsSymbolAndRefCoverage(t *testing.T) {
	resolved := selfhost.ResolveSourceStructured([]byte(`fn helper(x: Int) -> Int {
    x
}

fn main() {
    let value = helper(1)
}
`))
	if resolved.Summary.Diagnostics != 0 {
		t.Fatalf("diagnostics = %d, want 0 (summary=%#v diagnostics=%#v)", resolved.Summary.Diagnostics, resolved.Summary, resolved.Diagnostics)
	}
	helper := findResolvedSymbol(resolved, "helper", "fn")
	if helper == nil {
		t.Fatalf("missing helper symbol in %#v", resolved.Symbols)
	}
	ref := findResolvedRef(resolved, "helper")
	if ref == nil {
		t.Fatalf("missing helper ref in %#v", resolved.Refs)
	}
	if ref.TargetNode != helper.Node {
		t.Fatalf("helper ref target node = %d, want %d", ref.TargetNode, helper.Node)
	}
	if resolved.Summary.SymbolsByKind["fn"] == 0 {
		t.Fatalf("fn symbol histogram missing: %#v", resolved.Summary.SymbolsByKind)
	}
}

func TestResolvePackageStructuredHandlesCrossFileRefsASTNative(t *testing.T) {
	dir := t.TempDir()
	helperPath := filepath.Join(dir, "helper.osty")
	mainPath := filepath.Join(dir, "main.osty")

	helper := canonicalSelfhostInput(t, []byte(`pub fn helper() -> Int {
    41
}
`), 0)
	helper.Name = "helper.osty"
	helper.Path = helperPath

	main := canonicalSelfhostInput(t, []byte(`fn main() {
    let value = helper()
}
`), len(helper.Source)+1)
	main.Name = "main.osty"
	main.Path = mainPath

	resolved, err := selfhost.ResolvePackageStructured(selfhost.PackageResolveInput{
		Files: []selfhost.PackageResolveFile{helper, main},
	})
	if err != nil {
		t.Fatalf("ResolvePackageStructured: %v", err)
	}
	if resolved.Summary.Diagnostics != 0 {
		t.Fatalf("diagnostics = %d, want 0 (summary=%#v diagnostics=%#v)", resolved.Summary.Diagnostics, resolved.Summary, resolved.Diagnostics)
	}
	helperSym := findResolvedSymbol(resolved, "helper", "fn")
	if helperSym == nil {
		t.Fatalf("missing helper symbol in %#v", resolved.Symbols)
	}
	ref := findResolvedRef(resolved, "helper")
	if ref == nil {
		t.Fatalf("missing helper ref in %#v", resolved.Refs)
	}
	if ref.File != mainPath {
		t.Fatalf("ref file = %q, want %q", ref.File, mainPath)
	}
	if ref.TargetFile != helperPath {
		t.Fatalf("target file = %q, want %q", ref.TargetFile, helperPath)
	}
	if ref.TargetNode != helperSym.Node {
		t.Fatalf("target node = %d, want %d", ref.TargetNode, helperSym.Node)
	}
}

func TestResolvePackageStructuredUseBodyMissingMemberASTNative(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "main.osty")
	input := canonicalSelfhostInput(t, []byte(`use go "dep" as dep {
    fn make() -> Int
}

fn main() {
    dep.missing()
}
`), 0)
	input.Name = "main.osty"
	input.Path = srcPath

	resolved, err := selfhost.ResolvePackageStructured(selfhost.PackageResolveInput{
		Files: []selfhost.PackageResolveFile{input},
	})
	if err != nil {
		t.Fatalf("ResolvePackageStructured: %v", err)
	}
	got := findResolveDiagnostic(resolved, "E0508")
	if got == nil {
		t.Fatalf("expected E0508, got %#v", resolved.Diagnostics)
	}
	if got.File != srcPath {
		t.Fatalf("diagnostic file = %q, want %q", got.File, srcPath)
	}
	if got.Name != "missing" {
		t.Fatalf("diagnostic name = %q, want %q", got.Name, "missing")
	}
	if resolved.Summary.DiagnosticsByCode["E0508"] != 1 {
		t.Fatalf("diagnostic histogram = %#v, want E0508=1", resolved.Summary.DiagnosticsByCode)
	}
}

func TestResolvePackageStructuredDuplicateUsesOwningFilePath(t *testing.T) {
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "first.osty")
	secondPath := filepath.Join(dir, "second.osty")

	first := canonicalSelfhostInput(t, []byte(`pub fn helper() -> Int {
    0
}
`), 0)
	first.Name = "first.osty"
	first.Path = firstPath

	second := canonicalSelfhostInput(t, []byte(`pub fn helper() -> Int {
    1
}
`), len(first.Source)+1)
	second.Name = "second.osty"
	second.Path = secondPath

	resolved, err := selfhost.ResolvePackageStructured(selfhost.PackageResolveInput{
		Files: []selfhost.PackageResolveFile{first, second},
	})
	if err != nil {
		t.Fatalf("ResolvePackageStructured: %v", err)
	}
	got := findResolveDiagnostic(resolved, "E0501")
	if got == nil {
		t.Fatalf("expected E0501, got %#v", resolved.Diagnostics)
	}
	if got.File != secondPath {
		t.Fatalf("duplicate file = %q, want %q", got.File, secondPath)
	}
	if resolved.Summary.Duplicates != 1 {
		t.Fatalf("duplicates = %d, want 1 (summary=%#v)", resolved.Summary.Duplicates, resolved.Summary)
	}
}

// TestResolveStructuredFromRunIsAstbridgeFree pins the core wedge
// promise: calling Run + Diagnostics + ResolveStructuredFromRun on a
// clean single-file source must not trigger astLowerPublicFile, i.e.
// AstbridgeLowerCount stays at zero. The same test then calls
// run.File() and verifies the counter bumps exactly once, confirming
// that astbridge is still reachable for fallbacks (for example the
// --show-scopes path in `osty resolve`) and that the counter is wired
// to the right site. This is the regression net for future wedges:
// any new native path that accidentally re-introduces an *ast.File
// detour will bump the counter and fail this test.
func TestResolveStructuredFromRunIsAstbridgeFree(t *testing.T) {
	src := []byte(`fn helper(x: Int) -> Int {
    x
}

fn main() {
    let value = helper(1)
}
`)
	selfhost.ResetAstbridgeLowerCount()

	run := selfhost.Run(src)
	_ = run.Diagnostics()
	resolved := selfhost.ResolveStructuredFromRun(run)

	if resolved.Summary.Diagnostics != 0 {
		t.Fatalf("clean source produced diagnostics: %#v", resolved.Diagnostics)
	}
	if got := selfhost.AstbridgeLowerCount(); got != 0 {
		t.Fatalf("AstbridgeLowerCount after Run + Diagnostics + ResolveStructuredFromRun = %d, want 0 (the arena path must not touch astbridge)", got)
	}

	if file := run.File(); file == nil {
		t.Fatalf("run.File() returned nil")
	}
	if got := selfhost.AstbridgeLowerCount(); got != 1 {
		t.Fatalf("AstbridgeLowerCount after run.File() = %d, want 1 (counter should be wired to the sole astbridge entry point)", got)
	}

	_ = run.File()
	if got := selfhost.AstbridgeLowerCount(); got != 1 {
		t.Fatalf("AstbridgeLowerCount after cached run.File() = %d, want 1 (re-calling File() should not re-lower)", got)
	}
}

// TestResolveStructuredFromRunMatchesResolveSourceStructured pins the
// invariant that feeds the astbridge removal wedge: running the native
// resolver on a FrontendRun's parser arena directly must produce the
// same ResolveResult as the legacy path that re-lexes/re-parses the
// source. When this holds, downstream CLI call sites can switch from
// ResolveSourceStructured / ResolvePackageStructured (both of which
// still participate in the *ast.File round-trip for multi-file inputs)
// to ResolveStructuredFromRun without any observable change.
func TestResolveStructuredFromRunMatchesResolveSourceStructured(t *testing.T) {
	cases := []struct {
		name string
		src  []byte
	}{
		{
			name: "helper+main with ref",
			src: []byte(`fn helper(x: Int) -> Int {
    x
}

fn main() {
    let value = helper(1)
}
`),
		},
		{
			name: "use body with struct decl",
			src: []byte(`use std.fs as fs

pub struct User {
    pub name: String,
    pub age: Int,
}

fn main() {
    let u = User { name: "a", age: 1 }
}
`),
		},
		{
			name: "unresolved ref produces diagnostic",
			src: []byte(`fn main() {
    let x = missing()
}
`),
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			legacy := selfhost.ResolveSourceStructured(tc.src)
			fresh := selfhost.ResolveStructuredFromRun(selfhost.Run(tc.src))
			if !reflect.DeepEqual(legacy, fresh) {
				t.Fatalf("ResolveStructuredFromRun diverges from ResolveSourceStructured\nlegacy=%#v\nfresh=%#v", legacy, fresh)
			}
		})
	}
}

func TestResolveSourceStructuredUndefinedLoopLabel(t *testing.T) {
	resolved := selfhost.ResolveSourceStructured([]byte(`fn main() {
    for {
        break 'missing
    }
}
`))
	if got := findResolveDiagnostic(resolved, "E0763"); got == nil {
		t.Fatalf("expected E0763, got %#v", resolved.Diagnostics)
	}
}

func TestResolveSourceStructuredLoopLabelShadow(t *testing.T) {
	resolved := selfhost.ResolveSourceStructured([]byte(`fn main() {
    'outer: for {
        'outer: for {
            continue 'outer
        }
    }
}
`))
	if got := findResolveDiagnostic(resolved, "E0764"); got == nil {
		t.Fatalf("expected E0764, got %#v", resolved.Diagnostics)
	}
}

func TestResolveSourceStructuredResolvesBreakValue(t *testing.T) {
	resolved := selfhost.ResolveSourceStructured([]byte(`fn main() {
    let value = 1
    let result = loop {
        break value
    }
}
`))
	if resolved.Summary.Diagnostics != 0 {
		t.Fatalf("unexpected diagnostics: %#v", resolved.Diagnostics)
	}
	if ref := findResolvedRef(resolved, "value"); ref == nil {
		t.Fatalf("missing break-value ref in %#v", resolved.Refs)
	}
}

func findResolvedSymbol(result selfhost.ResolveResult, name string, kind string) *selfhost.ResolvedSymbol {
	for i := range result.Symbols {
		if result.Symbols[i].Name == name && result.Symbols[i].Kind == kind {
			return &result.Symbols[i]
		}
	}
	return nil
}

func findResolvedRef(result selfhost.ResolveResult, name string) *selfhost.ResolvedRef {
	for i := range result.Refs {
		if result.Refs[i].Name == name {
			return &result.Refs[i]
		}
	}
	return nil
}

func findResolveDiagnostic(result selfhost.ResolveResult, code string) *selfhost.ResolveDiagnosticRecord {
	for i := range result.Diagnostics {
		if result.Diagnostics[i].Code == code {
			return &result.Diagnostics[i]
		}
	}
	return nil
}
