package check

import (
	"testing"

	"github.com/osty/osty/internal/diag"
)

// Pod-bound end-to-end tests. These exercise the full parser →
// resolver → privilege + pod + noalloc pipeline for `<T: Pod>`
// bound clauses. Before this spike, `Pod` was unknown to the
// resolver and `E0500` fired in privileged packages too; the
// prelude registration in this PR lets the resolver bind `Pod`
// to a SymBuiltin so generic bound clauses typecheck cleanly
// inside privileged packages.

func TestPodBoundPrivilegedCellResolves(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
pub struct Cell<T: Pod> {
    pub v: T,
}
`
	diags := runEndToEnd(t, src, true)
	errs := errorsOnly(diags)
	if len(errs) > 0 {
		t.Fatalf("privileged `Cell<T: Pod>` must resolve cleanly, got:\n%s",
			renderDiagList(errs))
	}
}

func TestPodBoundPrivilegedMultipleParamsResolve(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
pub struct Pair<A: Pod, B: Pod> {
    pub a: A,
    pub b: B,
}
`
	diags := runEndToEnd(t, src, true)
	errs := errorsOnly(diags)
	if len(errs) > 0 {
		t.Fatalf("multiple Pod-bounded generics must resolve, got:\n%s",
			renderDiagList(errs))
	}
}

func TestPodBoundPrivilegedFnGenericResolves(t *testing.T) {
	// Plain `<T: Pod>` on a fn with no runtime-intrinsic calls in the
	// body. Verifies the bound binds cleanly now that `Pod` is in the
	// prelude. Intrinsic-call machinery (`raw.*`) is a later spike.
	src := `
#[no_alloc]
pub fn identityPod<T: Pod>(v: T) -> T {
    v
}
`
	diags := runEndToEnd(t, src, true)
	errs := errorsOnly(diags)
	if len(errs) > 0 {
		t.Fatalf("privileged `<T: Pod>` on a fn must resolve cleanly, got:\n%s",
			renderDiagList(errs))
	}
}

func TestPodBoundUnprivilegedFnRejected(t *testing.T) {
	src := `
pub fn takesPod<T: Pod>(value: T) -> T {
    value
}
`
	diags := runEndToEnd(t, src, false)
	if countByCode(diags, diag.CodeRuntimePrivilegeViolation) == 0 {
		t.Fatalf("user code with `<T: Pod>` must produce E0770, got:\n%s",
			renderDiagList(diags))
	}
}

func TestPodBoundUnprivilegedStructRejected(t *testing.T) {
	src := `
pub struct Holder<T: Pod> {
    pub v: T,
}
`
	diags := runEndToEnd(t, src, false)
	if countByCode(diags, diag.CodeRuntimePrivilegeViolation) == 0 {
		t.Fatalf("user struct with `<T: Pod>` must produce E0770, got:\n%s",
			renderDiagList(diags))
	}
}

func TestPodBoundUnprivilegedEnumRejected(t *testing.T) {
	src := `
pub enum Wrap<T: Pod> {
    Empty,
    Full(T),
}
`
	diags := runEndToEnd(t, src, false)
	if countByCode(diags, diag.CodeRuntimePrivilegeViolation) == 0 {
		t.Fatalf("user enum with `<T: Pod>` must produce E0770, got:\n%s",
			renderDiagList(diags))
	}
}

func TestPodBoundUnprivilegedTypeAliasRejected(t *testing.T) {
	src := `
pub type PodList<T: Pod> = List<T>
`
	diags := runEndToEnd(t, src, false)
	if countByCode(diags, diag.CodeRuntimePrivilegeViolation) == 0 {
		t.Fatalf("user type alias with `<T: Pod>` must produce E0770, got:\n%s",
			renderDiagList(diags))
	}
}

func TestPodBoundUnprivilegedInterfaceRejected(t *testing.T) {
	src := `
pub interface Storage<T: Pod> {
    fn put(self, v: T)
}
`
	diags := runEndToEnd(t, src, false)
	if countByCode(diags, diag.CodeRuntimePrivilegeViolation) == 0 {
		t.Fatalf("user interface with `<T: Pod>` must produce E0770, got:\n%s",
			renderDiagList(diags))
	}
}

func TestPodBoundOrdinaryBoundsUnaffected(t *testing.T) {
	// `<T: Ordered>` is NOT runtime-gated; user code must continue
	// to use it freely. The privilege gate widening must not cause
	// false positives on ordinary prelude bounds.
	src := `
pub fn sortable<T: Ordered>(xs: List<T>) -> List<T> {
    xs
}

pub fn hashed<T: Hashable + Equal>(x: T) -> Int {
    x.hash()
}
`
	diags := runEndToEnd(t, src, false)
	if countByCode(diags, diag.CodeRuntimePrivilegeViolation) > 0 {
		t.Fatalf("ordinary Ordered/Hashable bounds must not be gated, got:\n%s",
			renderDiagList(diags))
	}
}
