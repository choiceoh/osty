package selfhost_test

import (
	"path/filepath"
	"reflect"
	"strings"
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

func TestResolveFromSourceMatchesRunDiagnosticsAndStructuredResult(t *testing.T) {
	cases := []struct {
		name string
		path string
		src  []byte
	}{
		{
			name: "clean source",
			path: "main.osty",
			src: []byte(`fn helper(x: Int) -> Int {
    x
}

fn main() {
    let value = helper(1)
}
`),
		},
		{
			name: "missing ref",
			path: "broken.osty",
			src: []byte(`fn main() {
    let x = missing()
}
`),
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			run := selfhost.Run(tc.src)
			wantDiags := run.Diagnostics()
			wantResult := selfhost.ResolveStructuredFromRunForPath(run, tc.path)

			selfhost.ResetAstbridgeLowerCount()
			gotDiags, gotResult := selfhost.ResolveFromSource(tc.src, tc.path)
			if !reflect.DeepEqual(wantDiags, gotDiags) {
				t.Fatalf("ResolveFromSource parse diagnostics diverge\nwant=%#v\ngot=%#v", wantDiags, gotDiags)
			}
			if !reflect.DeepEqual(wantResult, gotResult) {
				t.Fatalf("ResolveFromSource result diverges\nwant=%#v\ngot=%#v", wantResult, gotResult)
			}
			if got := selfhost.AstbridgeLowerCount(); got != 0 {
				t.Fatalf("ResolveFromSource: AstbridgeLowerCount = %d, want 0", got)
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

func TestResolveSourceStructuredWithCfgDropsNonMatchingOS(t *testing.T) {
	src := []byte(`#[cfg(os = "linux")]
fn linuxOnly() -> Int { 42 }

#[cfg(os = "darwin")]
fn darwinOnly() -> Int { 99 }

fn unconditional() -> Int { 1 }
`)
	env := &selfhost.CfgEnv{OS: "linux", Arch: "amd64", Target: "linux"}
	resolved := selfhost.ResolveSourceStructuredWithCfg(src, env)
	if resolved.Summary.Diagnostics != 0 {
		t.Fatalf("unexpected diagnostics: %#v", resolved.Diagnostics)
	}
	if findResolvedSymbol(resolved, "linuxOnly", "fn") == nil {
		t.Errorf("linuxOnly should survive under os=linux")
	}
	if findResolvedSymbol(resolved, "darwinOnly", "fn") != nil {
		t.Errorf("darwinOnly should drop under os=linux")
	}
	if findResolvedSymbol(resolved, "unconditional", "fn") == nil {
		t.Errorf("unannotated fn should always survive")
	}
}

func TestResolveSourceStructuredWithCfgFeatureFlag(t *testing.T) {
	src := []byte(`#[cfg(feature = "ssl")]
fn withSsl() -> Int { 1 }

#[cfg(feature = "tls")]
fn withTls() -> Int { 2 }
`)
	env := &selfhost.CfgEnv{OS: "linux", Arch: "amd64", Target: "linux", Features: []string{"ssl"}}
	resolved := selfhost.ResolveSourceStructuredWithCfg(src, env)
	if findResolvedSymbol(resolved, "withSsl", "fn") == nil {
		t.Errorf("withSsl should survive with feature=ssl")
	}
	if findResolvedSymbol(resolved, "withTls", "fn") != nil {
		t.Errorf("withTls should drop without feature=tls")
	}
}

func TestResolveSourceStructuredWithCfgUnknownKeyEmitsE0405(t *testing.T) {
	src := []byte(`#[cfg(bogus = "value")]
fn bad() -> Int { 1 }
`)
	env := &selfhost.CfgEnv{OS: "linux", Arch: "amd64", Target: "linux"}
	resolved := selfhost.ResolveSourceStructuredWithCfg(src, env)
	if got := findResolveDiagnostic(resolved, "E0405"); got == nil {
		t.Fatalf("expected E0405 for unknown cfg key, got %#v", resolved.Diagnostics)
	}
	if findResolvedSymbol(resolved, "bad", "fn") != nil {
		t.Errorf("decl with unknown cfg key should drop")
	}
}

func TestResolveSourceStructuredPartialStructMergesMethods(t *testing.T) {
	// Two partial declarations of the same struct — fields in one,
	// methods in the other. R19 allows this; no duplicate diag should
	// fire, and both methods flow through annotation validation.
	src := []byte(`pub struct Point {
    pub x: Int,
    pub y: Int,
}

pub struct Point {
    pub fn origin() -> Self {
        Point { x: 0, y: 0 }
    }
}
`)
	resolved := selfhost.ResolveSourceStructured(src)
	if got := findResolveDiagnostic(resolved, "E0501"); got != nil {
		t.Fatalf("unexpected E0501 on legal partial merge: %#v", resolved.Diagnostics)
	}
	if resolved.Summary.Duplicates != 0 {
		t.Errorf("duplicates = %d, want 0", resolved.Summary.Duplicates)
	}
	// Exactly one Point symbol (from the first partial); the second
	// partial reuses the canonical symbol.
	count := 0
	for _, s := range resolved.Symbols {
		if s.Name == "Point" && s.Kind == "type" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Point symbol count = %d, want 1 (partial merge)", count)
	}
}

func TestResolveSourceStructuredPartialFieldsInMultipleEmitsE0501(t *testing.T) {
	src := []byte(`pub struct Point {
    pub x: Int,
}

pub struct Point {
    pub y: Int,
}
`)
	resolved := selfhost.ResolveSourceStructured(src)
	if got := findResolveDiagnostic(resolved, "E0501"); got == nil {
		t.Fatalf("expected E0501 for fields in multiple partials, got %#v", resolved.Diagnostics)
	}
	// The diag message should mention fields-in-one-partial so callers
	// can distinguish this from a plain duplicate.
	found := false
	for _, d := range resolved.Diagnostics {
		if d.Code == "E0501" && strings.Contains(d.Message, "fields") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected fields-specific E0501 message, got %#v", resolved.Diagnostics)
	}
}

func TestResolveSourceStructuredPartialPubMismatchEmitsE0501(t *testing.T) {
	src := []byte(`pub struct Box {
    pub value: Int,
}

struct Box {
    fn display(self) -> Int { self.value }
}
`)
	resolved := selfhost.ResolveSourceStructured(src)
	found := false
	for _, d := range resolved.Diagnostics {
		if d.Code == "E0501" && strings.Contains(d.Message, "visibility") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected visibility-specific E0501, got %#v", resolved.Diagnostics)
	}
}

func TestResolveSourceStructuredPartialGenericMismatchEmitsE0501(t *testing.T) {
	src := []byte(`pub struct Cell<T> {
    pub value: T,
}

pub struct Cell<U> {
    pub fn display(self) -> U { self.value }
}
`)
	resolved := selfhost.ResolveSourceStructured(src)
	found := false
	for _, d := range resolved.Diagnostics {
		if d.Code == "E0501" && strings.Contains(d.Message, "type parameters") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected type-parameter-specific E0501, got %#v", resolved.Diagnostics)
	}
}

func TestResolveSourceStructuredPartialDuplicateMethodEmitsE0501(t *testing.T) {
	src := []byte(`pub struct Counter {
    pub n: Int,

    pub fn reset(mut self) {
        self.n = 0
    }
}

pub struct Counter {
    pub fn reset(mut self) {
        self.n = -1
    }
}
`)
	resolved := selfhost.ResolveSourceStructured(src)
	found := false
	for _, d := range resolved.Diagnostics {
		if d.Code == "E0501" && strings.Contains(d.Message, "method `reset`") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected duplicate-method E0501, got %#v", resolved.Diagnostics)
	}
}

func TestResolveSourceStructuredStructEnumKindMismatchEmitsE0501(t *testing.T) {
	// A struct and an enum sharing a name are NOT a legal partial —
	// it's a normal duplicate, so a plain E0501 fires and the
	// duplicates counter bumps.
	src := []byte(`pub struct Color {
    pub hex: Int,
}

pub enum Color {
    Red,
    Green,
}
`)
	resolved := selfhost.ResolveSourceStructured(src)
	if got := findResolveDiagnostic(resolved, "E0501"); got == nil {
		t.Fatalf("expected E0501 for struct-vs-enum kind mismatch, got %#v", resolved.Diagnostics)
	}
	if resolved.Summary.Duplicates == 0 {
		t.Errorf("duplicates counter should bump on kind mismatch, got %d", resolved.Summary.Duplicates)
	}
}

func TestResolveSourceStructuredNilCfgPassesEveryDecl(t *testing.T) {
	// Nil env preserves the pre-port behaviour: no filtering, every
	// cfg-annotated decl survives. The shape validator still runs.
	src := []byte(`#[cfg(os = "darwin")]
fn darwinOnly() -> Int { 42 }

#[cfg(bogus = "value")]
fn bad() -> Int { 1 }
`)
	resolved := selfhost.ResolveSourceStructuredWithCfg(src, nil)
	if findResolvedSymbol(resolved, "darwinOnly", "fn") == nil {
		t.Errorf("nil cfg should leave darwinOnly alive")
	}
	if findResolvedSymbol(resolved, "bad", "fn") == nil {
		t.Errorf("nil cfg should leave malformed-cfg decl alive")
	}
	if got := findResolveDiagnostic(resolved, "E0405"); got == nil {
		t.Errorf("E0405 should still fire under nil cfg (validation is always on)")
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

// ---- v0.6 annotation recognition + arg validation --------------------

// Every v0.6 performance annotation must be accepted by the Osty
// resolver. Regression net: before the port, these fired E0400 and any
// user code hit spurious resolver errors under the native checker.
func TestV06AnnotationsRecognized(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"vectorize-bare", "#[vectorize]\nfn f() -> Int { 0 }\n"},
		{"vectorize-tuned", `#[vectorize(scalable, predicate, width = 8)]` + "\nfn f() -> Int { 0 }\n"},
		{"no_vectorize", "#[no_vectorize]\nfn f() -> Int { 0 }\n"},
		{"parallel", "#[parallel]\nfn f() -> Int { 0 }\n"},
		{"unroll-bare", "#[unroll]\nfn f() -> Int { 0 }\n"},
		{"unroll-count", "#[unroll(count = 4)]\nfn f() -> Int { 0 }\n"},
		{"inline-bare", "#[inline]\nfn f() -> Int { 0 }\n"},
		{"inline-always", "#[inline(always)]\nfn f() -> Int { 0 }\n"},
		{"inline-never", "#[inline(never)]\nfn f() -> Int { 0 }\n"},
		{"hot", "#[hot]\nfn f() -> Int { 0 }\n"},
		{"cold", "#[cold]\nfn f() -> Int { 0 }\n"},
		{"pure", "#[pure]\nfn f() -> Int { 0 }\n"},
		{"target_feature", "#[target_feature(avx512f, avx512bw)]\nfn f() -> Int { 0 }\n"},
		{"noalias-bare", "#[noalias]\nfn f() -> Int { 0 }\n"},
		{"noalias-params", "#[noalias(src, dst)]\nfn f() -> Int { 0 }\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := selfhost.ResolveSourceStructured([]byte(c.src))
			for _, d := range r.Diagnostics {
				if d.Code == "E0400" || d.Code == "E0739" {
					t.Errorf("unexpected %s on %s: %q", d.Code, c.name, d.Message)
				}
			}
		})
	}
}

func TestV06BareFlagRejectsArgs(t *testing.T) {
	cases := []string{
		"#[hot(foo)]\nfn f() -> Int { 0 }\n",
		"#[cold(bar)]\nfn f() -> Int { 0 }\n",
		"#[pure(baz)]\nfn f() -> Int { 0 }\n",
		"#[no_vectorize(x)]\nfn f() -> Int { 0 }\n",
		"#[parallel(y)]\nfn f() -> Int { 0 }\n",
	}
	for _, src := range cases {
		r := selfhost.ResolveSourceStructured([]byte(src))
		found := false
		for _, d := range r.Diagnostics {
			if d.Code == "E0739" && strings.Contains(d.Message, "does not take arguments") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected E0739 for args on bare flag, got %#v (src=%q)", r.Diagnostics, src)
		}
	}
}

func TestV06VectorizeBadWidthRejected(t *testing.T) {
	// width must be a positive integer in 1..1024.
	src := []byte(`#[vectorize(width = 9999)]
fn f() -> Int { 0 }
`)
	r := selfhost.ResolveSourceStructured(src)
	found := false
	for _, d := range r.Diagnostics {
		if d.Code == "E0739" && strings.Contains(d.Message, "1..1024") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected out-of-range width diag, got %#v", r.Diagnostics)
	}
}

func TestV06VectorizeUnknownKeyRejected(t *testing.T) {
	src := []byte(`#[vectorize(bogus)]
fn f() -> Int { 0 }
`)
	r := selfhost.ResolveSourceStructured(src)
	found := false
	for _, d := range r.Diagnostics {
		if d.Code == "E0739" && strings.Contains(d.Message, "unknown") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected unknown-key diag, got %#v", r.Diagnostics)
	}
}

func TestV06VectorizeDuplicateFlagRejected(t *testing.T) {
	src := []byte(`#[vectorize(scalable, scalable)]
fn f() -> Int { 0 }
`)
	r := selfhost.ResolveSourceStructured(src)
	found := false
	for _, d := range r.Diagnostics {
		if d.Code == "E0739" && strings.Contains(d.Message, "duplicate") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected duplicate-scalable diag, got %#v", r.Diagnostics)
	}
}

func TestV06UnrollBadCountRejected(t *testing.T) {
	src := []byte(`#[unroll(count = 5000)]
fn f() -> Int { 0 }
`)
	r := selfhost.ResolveSourceStructured(src)
	found := false
	for _, d := range r.Diagnostics {
		if d.Code == "E0739" && strings.Contains(d.Message, "1..1024") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected out-of-range count diag, got %#v", r.Diagnostics)
	}
}

func TestV06InlineExtraArgRejected(t *testing.T) {
	src := []byte(`#[inline(always, foo)]
fn f() -> Int { 0 }
`)
	r := selfhost.ResolveSourceStructured(src)
	found := false
	for _, d := range r.Diagnostics {
		if d.Code == "E0739" && strings.Contains(d.Message, "at most one argument") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected `at most one argument` diag, got %#v", r.Diagnostics)
	}
}

func TestV06InlineUnknownFlagRejected(t *testing.T) {
	src := []byte(`#[inline(sometimes)]
fn f() -> Int { 0 }
`)
	r := selfhost.ResolveSourceStructured(src)
	found := false
	for _, d := range r.Diagnostics {
		if d.Code == "E0739" && strings.Contains(d.Message, "unknown") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected unknown-inline-flag diag, got %#v", r.Diagnostics)
	}
}

func TestV06TargetFeatureEmptyRejected(t *testing.T) {
	src := []byte(`#[target_feature()]
fn f() -> Int { 0 }
`)
	r := selfhost.ResolveSourceStructured(src)
	found := false
	for _, d := range r.Diagnostics {
		if d.Code == "E0739" && strings.Contains(d.Message, "at least one feature") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected empty-feature-list diag, got %#v", r.Diagnostics)
	}
}

func TestV06TargetFeatureDuplicateRejected(t *testing.T) {
	src := []byte(`#[target_feature(avx512f, avx512f)]
fn f() -> Int { 0 }
`)
	r := selfhost.ResolveSourceStructured(src)
	found := false
	for _, d := range r.Diagnostics {
		if d.Code == "E0739" && strings.Contains(d.Message, "duplicate feature") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected duplicate-feature diag, got %#v", r.Diagnostics)
	}
}

func TestV06NoaliasDuplicateRejected(t *testing.T) {
	src := []byte(`#[noalias(p, p)]
fn f() -> Int { 0 }
`)
	r := selfhost.ResolveSourceStructured(src)
	found := false
	for _, d := range r.Diagnostics {
		if d.Code == "E0739" && strings.Contains(d.Message, "duplicate parameter") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected duplicate-param diag, got %#v", r.Diagnostics)
	}
}

// ---- Detect import cycles ---------------------------------------------

func TestDetectImportCyclesAcyclicGraphEmitsNoDiag(t *testing.T) {
	// a → b → c; no cycle.
	input := selfhost.WorkspaceUses{Packages: []selfhost.PackageUses{
		{Path: "a", Uses: []selfhost.UseEdge{{Target: "b", Pos: 10}}},
		{Path: "b", Uses: []selfhost.UseEdge{{Target: "c", Pos: 20}}},
		{Path: "c", Uses: nil},
	}}
	diags := selfhost.DetectImportCycles(input)
	if len(diags) != 0 {
		t.Fatalf("expected 0 cycle diags on DAG, got %d: %#v", len(diags), diags)
	}
}

func TestDetectImportCyclesTwoCycleEmitsOneDiag(t *testing.T) {
	// a ↔ b; one back-edge closes the cycle.
	input := selfhost.WorkspaceUses{Packages: []selfhost.PackageUses{
		{Path: "a", Uses: []selfhost.UseEdge{{Target: "b", Pos: 10, EndPos: 11, File: "a.osty"}}},
		{Path: "b", Uses: []selfhost.UseEdge{{Target: "a", Pos: 20, EndPos: 21, File: "b.osty"}}},
	}}
	diags := selfhost.DetectImportCycles(input)
	if len(diags) != 1 {
		t.Fatalf("expected 1 cycle diag, got %d: %#v", len(diags), diags)
	}
	d := diags[0]
	if d.Importer != "b" || d.Target != "a" {
		t.Errorf("expected b→a back-edge, got %s→%s", d.Importer, d.Target)
	}
	if d.Pos != 20 {
		t.Errorf("expected pos=20, got %d", d.Pos)
	}
	if !strings.Contains(d.Message, "cyclic import") {
		t.Errorf("expected cyclic-import message, got %q", d.Message)
	}
}

func TestDetectImportCyclesThreeNodeCycle(t *testing.T) {
	// a → b → c → a; DFS from `a` finds the c→a back-edge.
	input := selfhost.WorkspaceUses{Packages: []selfhost.PackageUses{
		{Path: "a", Uses: []selfhost.UseEdge{{Target: "b", Pos: 1}}},
		{Path: "b", Uses: []selfhost.UseEdge{{Target: "c", Pos: 2}}},
		{Path: "c", Uses: []selfhost.UseEdge{{Target: "a", Pos: 3}}},
	}}
	diags := selfhost.DetectImportCycles(input)
	if len(diags) != 1 {
		t.Fatalf("expected 1 cycle diag for 3-node cycle, got %d", len(diags))
	}
	if diags[0].Importer != "c" || diags[0].Target != "a" {
		t.Errorf("expected c→a back-edge, got %s→%s", diags[0].Importer, diags[0].Target)
	}
}

func TestDetectImportCyclesSelfLoop(t *testing.T) {
	input := selfhost.WorkspaceUses{Packages: []selfhost.PackageUses{
		{Path: "a", Uses: []selfhost.UseEdge{{Target: "a", Pos: 5}}},
	}}
	diags := selfhost.DetectImportCycles(input)
	if len(diags) != 1 {
		t.Fatalf("expected 1 cycle diag for self-loop, got %d", len(diags))
	}
}

func TestDetectImportCyclesUnknownTargetIgnored(t *testing.T) {
	// Edge pointing to a package not in the workspace (e.g. std / external dep).
	input := selfhost.WorkspaceUses{Packages: []selfhost.PackageUses{
		{Path: "a", Uses: []selfhost.UseEdge{{Target: "std.fs", Pos: 10}}},
	}}
	diags := selfhost.DetectImportCycles(input)
	if len(diags) != 0 {
		t.Fatalf("expected unknown target to be ignored, got %d diags", len(diags))
	}
}

func TestDetectImportCyclesMultipleCyclesEmitsMultipleDiags(t *testing.T) {
	// Two independent 2-cycles: a↔b and c↔d.
	input := selfhost.WorkspaceUses{Packages: []selfhost.PackageUses{
		{Path: "a", Uses: []selfhost.UseEdge{{Target: "b", Pos: 1}}},
		{Path: "b", Uses: []selfhost.UseEdge{{Target: "a", Pos: 2}}},
		{Path: "c", Uses: []selfhost.UseEdge{{Target: "d", Pos: 3}}},
		{Path: "d", Uses: []selfhost.UseEdge{{Target: "c", Pos: 4}}},
	}}
	diags := selfhost.DetectImportCycles(input)
	if len(diags) != 2 {
		t.Fatalf("expected 2 cycle diags, got %d: %#v", len(diags), diags)
	}
}

// ---- Lookup package member (E0507 / E0508) ----------------------------

func TestLookupPackageMemberPublicSymbolEmitsNothing(t *testing.T) {
	res := selfhost.LookupPackageMember("fs", "readFile", false, true, true)
	if res.Status != selfhost.MemberLookupOK {
		t.Fatalf("expected MemberLookupOK, got status=%d (code=%q)", res.Status, res.Code)
	}
	if res.Code != "" || res.Message != "" {
		t.Errorf("OK status should have empty strings, got %#v", res)
	}
}

func TestLookupPackageMemberPrivateSymbolEmitsE0507(t *testing.T) {
	res := selfhost.LookupPackageMember("fs", "internal", false, true, false)
	if res.Status != selfhost.MemberLookupPrivate {
		t.Fatalf("expected MemberLookupPrivate, got status=%d", res.Status)
	}
	if res.Code != "E0507" {
		t.Errorf("expected E0507 for private symbol, got %q", res.Code)
	}
	wantMsg := "`fs.internal` is not exported from package `fs`"
	if res.Message != wantMsg {
		t.Errorf("message mismatch\n want: %q\n got:  %q", wantMsg, res.Message)
	}
	if res.Primary != "private across packages" {
		t.Errorf("unexpected primary label: %q", res.Primary)
	}
	if res.Note != "declared without `pub` in package `fs`" {
		t.Errorf("unexpected note: %q", res.Note)
	}
	wantHint := "add `pub` to the declaration of `internal` or access it only from within its package"
	if res.Hint != wantHint {
		t.Errorf("hint mismatch\n want: %q\n got:  %q", wantHint, res.Hint)
	}
}

func TestLookupPackageMemberMissingSymbolEmitsE0508(t *testing.T) {
	res := selfhost.LookupPackageMember("fs", "bogus", false, false, false)
	if res.Status != selfhost.MemberLookupMissing {
		t.Fatalf("expected MemberLookupMissing, got status=%d", res.Status)
	}
	if res.Code != "E0508" {
		t.Errorf("expected E0508 for missing symbol, got %q", res.Code)
	}
	wantMsg := "package `fs` has no exported name `bogus`"
	if res.Message != wantMsg {
		t.Errorf("message mismatch\n want: %q\n got:  %q", wantMsg, res.Message)
	}
	if res.Primary != "unknown member" {
		t.Errorf("unexpected primary label: %q", res.Primary)
	}
	if res.Note != "" || res.Hint != "" {
		t.Errorf("missing-symbol should have no note/hint, got note=%q hint=%q", res.Note, res.Hint)
	}
}

func TestLookupPackageMemberMissingInTypePosSwitchesWording(t *testing.T) {
	// typePos=true tightens "name" → "type" in the E0508 message.
	res := selfhost.LookupPackageMember("fs", "Handle", true, false, false)
	if res.Code != "E0508" {
		t.Fatalf("expected E0508, got %q", res.Code)
	}
	want := "package `fs` has no exported type `Handle`"
	if res.Message != want {
		t.Errorf("message mismatch\n want: %q\n got:  %q", want, res.Message)
	}
}

func TestLookupPackageMemberPrivateIgnoresTypePos(t *testing.T) {
	// E0507 wording does not branch on typePos — the surface name is the
	// same whether the private symbol is referenced in a type or value
	// position. Matches the Go resolver's pre-port behaviour.
	valueRes := selfhost.LookupPackageMember("pkg", "x", false, true, false)
	typeRes := selfhost.LookupPackageMember("pkg", "x", true, true, false)
	if valueRes.Message != typeRes.Message {
		t.Errorf("E0507 message must not vary with typePos\n  value: %q\n  type:  %q",
			valueRes.Message, typeRes.Message)
	}
}
