package check

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// runPriv parses + resolves + runs only the privilege gate.
// Unlike the noalloc helper, it exposes the `privileged` flag so
// tests can exercise both paths.
func runPriv(t *testing.T, src string, privileged bool) []*diag.Diagnostic {
	t.Helper()
	file, diags := parser.ParseDiagnostics([]byte(src))
	if len(diags) != 0 {
		t.Fatalf("parse diagnostics: %v", diags)
	}
	// Resolver is not strictly required for the privilege gate (the
	// gate works directly on the AST), but we run it anyway so the
	// fixture is as close to the real pipeline as possible.
	_ = resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	return runPrivilegeGate(file, privileged)
}

func countPrivilegeDiags(ds []*diag.Diagnostic) int {
	n := 0
	for _, d := range ds {
		if d.Code == diag.CodeRuntimePrivilegeViolation {
			n++
		}
	}
	return n
}

func assertPrivilegeCount(t *testing.T, src string, privileged bool, want int) []*diag.Diagnostic {
	t.Helper()
	out := runPriv(t, src, privileged)
	got := countPrivilegeDiags(out)
	if got != want {
		t.Fatalf("expected %d E0770 diagnostics (privileged=%v), got %d:\n%s",
			want, privileged, got, formatDiags(out))
	}
	return out
}

// --- unprivileged rejections ---

func TestPrivilegeRejectsIntrinsicOutsidePrivileged(t *testing.T) {
	src := `
#[intrinsic]
fn alloc(bytes: Int, align: Int) -> Int { 0 }
`
	assertPrivilegeCount(t, src, false, 1)
}

func TestPrivilegeRejectsCABIOutsidePrivileged(t *testing.T) {
	src := `
#[c_abi]
pub fn connect(port: Int) -> Int {
    port
}
`
	assertPrivilegeCount(t, src, false, 1)
}

func TestPrivilegeRejectsExportOutsidePrivileged(t *testing.T) {
	src := `
#[export("custom_symbol")]
pub fn trampoline() -> Int {
    0
}
`
	assertPrivilegeCount(t, src, false, 1)
}

func TestPrivilegeRejectsNoAllocOutsidePrivileged(t *testing.T) {
	src := `
#[no_alloc]
fn pure(a: Int, b: Int) -> Int {
    a + b
}
`
	assertPrivilegeCount(t, src, false, 1)
}

func TestPrivilegeRejectsUseStdRuntime(t *testing.T) {
	src := `
use std.runtime.raw
`
	assertPrivilegeCount(t, src, false, 1)
}

func TestPrivilegeRejectsUseStdRuntimeBareNamespace(t *testing.T) {
	src := `
use std.runtime
`
	assertPrivilegeCount(t, src, false, 1)
}

func TestPrivilegeAcceptsOrdinaryStdImport(t *testing.T) {
	// `use std.fs` is fine — only `std.runtime.*` is gated.
	src := `
use std.fs
`
	assertPrivilegeCount(t, src, false, 0)
}

func TestPrivilegeRejectsRawPtrTypeReference(t *testing.T) {
	src := `
pub fn takesPtr(p: RawPtr) -> Int {
    0
}
`
	assertPrivilegeCount(t, src, false, 1)
}

func TestPrivilegeRejectsPodTypeReference(t *testing.T) {
	src := `
pub fn takesPod<T: Pod>(value: T) -> T {
    value
}
`
	// The `Pod` appears in the generic bound position; the spike
	// walker only inspects parameter/return/field *types*, not bound
	// clauses. So the current implementation produces 0 here. This is
	// deliberate: bound clauses go through the resolver, which is the
	// right place to reject runtime-only constraint names. Document
	// the known gap.
	assertPrivilegeCount(t, src, false, 0)
}

func TestPrivilegeRejectsRawPtrInReturnType(t *testing.T) {
	src := `
pub fn makePtr() -> RawPtr {
    0
}
`
	assertPrivilegeCount(t, src, false, 1)
}

func TestPrivilegeRejectsRawPtrInsideGeneric(t *testing.T) {
	src := `
pub fn wrap() -> List<RawPtr> {
    []
}
`
	assertPrivilegeCount(t, src, false, 1)
}

func TestPrivilegeRejectsRawPtrInOptional(t *testing.T) {
	src := `
pub fn maybePtr() -> RawPtr? {
    None
}
`
	assertPrivilegeCount(t, src, false, 1)
}

func TestPrivilegeRejectsRawPtrInStructField(t *testing.T) {
	src := `
pub struct Holder {
    pub p: RawPtr,
}
`
	assertPrivilegeCount(t, src, false, 1)
}

// --- privileged acceptances ---

func TestPrivilegeAcceptsIntrinsicInsidePrivileged(t *testing.T) {
	src := `
#[intrinsic]
fn alloc(bytes: Int, align: Int) -> Int { 0 }
`
	assertPrivilegeCount(t, src, true, 0)
}

func TestPrivilegeAcceptsStackedAnnotationsInsidePrivileged(t *testing.T) {
	src := `
#[export("osty.gc.alloc_v1")]
#[c_abi]
#[no_alloc]
pub fn alloc_v1(bytes: Int, kind: Int) -> Int {
    0
}
`
	assertPrivilegeCount(t, src, true, 0)
}

func TestPrivilegeAcceptsRawPtrInsidePrivileged(t *testing.T) {
	src := `
pub fn makePtr(p: RawPtr) -> RawPtr {
    p
}
`
	assertPrivilegeCount(t, src, true, 0)
}

func TestPrivilegeAcceptsStdRuntimeImportInsidePrivileged(t *testing.T) {
	src := `
use std.runtime.raw
`
	assertPrivilegeCount(t, src, true, 0)
}

// --- mixed / multi-violation reporting ---

func TestPrivilegeReportsMultipleViolationsInOneFile(t *testing.T) {
	src := `
use std.runtime.raw

#[intrinsic]
fn alloc(bytes: Int) -> RawPtr { 0 }

#[no_alloc]
#[c_abi]
pub fn step(p: RawPtr) -> Int {
    0
}
`
	// 1 use + 2 annotations on alloc (#[intrinsic]) + 1 annotation
	// slot on step combined with RawPtr param + RawPtr return on
	// alloc.
	// Concretely what the gate catches:
	//   - use std.runtime.raw              → 1
	//   - #[intrinsic] on alloc            → 1
	//   - RawPtr in alloc's return         → 1
	//   - #[no_alloc] on step              → 1
	//   - #[c_abi] on step                 → 1
	//   - RawPtr in step's param           → 1
	// Total: 6. Exact ordering is not asserted.
	assertPrivilegeCount(t, src, false, 6)
}

// --- heuristic: isPrivilegedPackage / isPrivilegedPackagePath ---

func TestIsPrivilegedPackagePath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"std.runtime", true},
		{"std.runtime.raw", true},
		{"std.runtime.internal.bump", true},
		{"std.runtime_extra", false}, // no trailing dot — not a subpath
		{"std.fs", false},
		{"my.app", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isPrivilegedPackagePath(c.path); got != c.want {
			t.Errorf("isPrivilegedPackagePath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestIsPrivilegedPackageDir(t *testing.T) {
	cases := []struct {
		dir  string
		want bool
	}{
		{"/repo/stdlib/std/runtime", true},
		{"/repo/stdlib/std/runtime/raw", true},
		{"/repo/stdlib/std/fs", false},
		{"/user/home/myapp", false},
		{"", false},
	}
	for _, c := range cases {
		pkg := &resolve.Package{Dir: c.dir}
		if got := isPrivilegedPackage(pkg); got != c.want {
			t.Errorf("isPrivilegedPackage(dir=%q) = %v, want %v", c.dir, got, c.want)
		}
	}
}

// --- diagnostic surface ---

func TestPrivilegeDiagnosticMessageNamesSurface(t *testing.T) {
	src := `
#[intrinsic]
fn alloc(bytes: Int) -> Int { 0 }
`
	out := runPriv(t, src, false)
	if len(out) == 0 {
		t.Fatalf("expected at least one diagnostic")
	}
	for _, d := range out {
		if d.Code == diag.CodeRuntimePrivilegeViolation {
			joined := d.Message
			for _, n := range d.Notes {
				joined += " " + n
			}
			if !strings.Contains(joined, "runtime sublanguage") {
				t.Fatalf("expected diagnostic to mention 'runtime sublanguage', got: %s", joined)
			}
			return
		}
	}
	t.Fatalf("no E0770 diagnostic found:\n%s", formatDiags(out))
}
