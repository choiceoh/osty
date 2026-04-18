package check

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
)

func runPodShape(t *testing.T, src string) []*diag.Diagnostic {
	t.Helper()
	file, diags := parser.ParseDiagnostics([]byte(src))
	if len(diags) != 0 {
		t.Fatalf("parse diagnostics: %v", diags)
	}
	return runPodShapeChecks(file)
}

func countPodShapeDiags(ds []*diag.Diagnostic) int {
	n := 0
	for _, d := range ds {
		if d.Code == diag.CodePodShapeViolation {
			n++
		}
	}
	return n
}

func assertPodCount(t *testing.T, src string, want int) []*diag.Diagnostic {
	t.Helper()
	out := runPodShape(t, src)
	got := countPodShapeDiags(out)
	if got != want {
		t.Fatalf("expected %d E0771 diagnostics, got %d:\n%s",
			want, got, formatDiags(out))
	}
	return out
}

// --- positive: well-formed Pod structs are accepted ---

func TestPodAcceptsAllPrimitiveFields(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Header {
    pub mark: Int8,
    pub kind: Int8,
    pub size: Int32,
    pub padding: Int32,
}
`
	assertPodCount(t, src, 0)
}

func TestPodAcceptsRawPtrField(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Linked {
    pub next: RawPtr,
    pub size: Int32,
}
`
	assertPodCount(t, src, 0)
}

func TestPodAcceptsTupleOfPod(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Pair {
    pub coords: (Int, Int),
}
`
	assertPodCount(t, src, 0)
}

func TestPodAcceptsOptionOfPod(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Slot {
    pub addr: Option<RawPtr>,
}
`
	assertPodCount(t, src, 0)
}

func TestPodAcceptsOptionalSugarOfPod(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Slot {
    pub addr: RawPtr?,
}
`
	assertPodCount(t, src, 0)
}

func TestPodAcceptsLocalPodStructAsField(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Inner {
    pub x: Int,
}

#[pod]
#[repr(c)]
struct Outer {
    pub inner: Inner,
    pub flag: Bool,
}
`
	assertPodCount(t, src, 0)
}

func TestPodAcceptsGenericWithPodBound(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Cell<T: Pod> {
    pub value: T,
}
`
	assertPodCount(t, src, 0)
}

func TestPodAcceptsAllFloatVariants(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Vec3 {
    pub x: Float32,
    pub y: Float64,
    pub z: Float,
}
`
	assertPodCount(t, src, 0)
}

// --- negative: reject non-Pod fields ---

func TestPodRejectsStringField(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Bad {
    pub name: String,
}
`
	assertPodCount(t, src, 1)
}

func TestPodRejectsListField(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Bad {
    pub xs: List<Int>,
}
`
	assertPodCount(t, src, 1)
}

func TestPodRejectsMapField(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Bad {
    pub kv: Map<String, Int>,
}
`
	assertPodCount(t, src, 1)
}

func TestPodRejectsBytesField(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Bad {
    pub buf: Bytes,
}
`
	assertPodCount(t, src, 1)
}

func TestPodRejectsResultField(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Bad {
    pub r: Result<Int, Error>,
}
`
	assertPodCount(t, src, 1)
}

func TestPodRejectsFnTypeField(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Bad {
    pub callback: fn(Int) -> Int,
}
`
	assertPodCount(t, src, 1)
}

func TestPodRejectsTupleWithNonPod(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Bad {
    pub mixed: (Int, String),
}
`
	assertPodCount(t, src, 1)
}

func TestPodRejectsOptionOfNonPod(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Bad {
    pub maybe: Option<String>,
}
`
	assertPodCount(t, src, 1)
}

func TestPodRejectsReferenceToNonLocalNonPodStruct(t *testing.T) {
	src := `
struct Other {
    pub x: Int,
}

#[pod]
#[repr(c)]
struct Bad {
    pub o: Other,
}
`
	assertPodCount(t, src, 1)
}

// --- negative: missing #[repr(c)] ---

func TestPodRejectsMissingRepr(t *testing.T) {
	src := `
#[pod]
struct NoRepr {
    pub x: Int,
}
`
	out := assertPodCount(t, src, 1)
	for _, d := range out {
		if d.Code == diag.CodePodShapeViolation {
			if !strings.Contains(d.Message, "#[repr(c)]") {
				t.Fatalf("expected diagnostic to mention #[repr(c)], got: %s", d.Message)
			}
			return
		}
	}
}

// --- negative: unbounded generic parameter ---

func TestPodRejectsUnboundedGeneric(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Cell<T> {
    pub value: T,
}
`
	// Two diagnostics: one for the unbounded generic parameter, plus
	// one for the field whose type is the unbounded T (which is
	// non-Pod from the field-type checker's view since T isn't in
	// our podPrimitives / podStructs sets and has no Pod bound).
	out := runPodShape(t, src)
	got := countPodShapeDiags(out)
	if got < 1 {
		t.Fatalf("expected at least 1 E0771, got %d:\n%s", got, formatDiags(out))
	}
	// At least one diagnostic must mention the parameter name.
	mentioned := false
	for _, d := range out {
		if d.Code != diag.CodePodShapeViolation {
			continue
		}
		if strings.Contains(d.Message, "`T`") {
			mentioned = true
			break
		}
	}
	if !mentioned {
		t.Fatalf("expected diagnostic to name unbounded parameter `T`, got:\n%s", formatDiags(out))
	}
}

func TestPodAcceptsMultipleGenericsWithPodBound(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Pair<A: Pod, B: Pod> {
    pub a: A,
    pub b: B,
}
`
	assertPodCount(t, src, 0)
}

func TestPodRejectsOneOfMultipleGenericsWithoutBound(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Pair<A: Pod, B> {
    pub a: A,
    pub b: B,
}
`
	out := runPodShape(t, src)
	got := countPodShapeDiags(out)
	if got < 1 {
		t.Fatalf("expected at least 1 E0771, got %d:\n%s", got, formatDiags(out))
	}
	// Diagnostic should name `B`, not `A`.
	for _, d := range out {
		if d.Code == diag.CodePodShapeViolation && strings.Contains(d.Message, "`B`") {
			return
		}
	}
	t.Fatalf("expected diagnostic to name unbounded parameter `B`:\n%s", formatDiags(out))
}

// --- ordinary structs are untouched ---

func TestPodLeavesOrdinaryStructsAlone(t *testing.T) {
	src := `
struct User {
    pub name: String,
    pub age: Int,
}
`
	assertPodCount(t, src, 0)
}

func TestPodLeavesReprWithoutPodAlone(t *testing.T) {
	// `#[repr(c)]` without `#[pod]` is currently a no-op in the
	// shape checker — repr by itself is just a layout request and
	// not a Pod assertion. The annotation may be flagged elsewhere
	// (privilege gate); it is not the Pod checker's concern.
	src := `
#[repr(c)]
struct ReprOnly {
    pub name: String,
    pub age: Int,
}
`
	assertPodCount(t, src, 0)
}

// --- diagnostic shape ---

func TestPodDiagnosticNamesOffendingField(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Bad {
    pub good: Int,
    pub naughty: String,
}
`
	out := runPodShape(t, src)
	if len(out) == 0 {
		t.Fatalf("expected at least one diagnostic")
	}
	for _, d := range out {
		if d.Code != diag.CodePodShapeViolation {
			continue
		}
		if strings.Contains(d.Message, "naughty") && strings.Contains(d.Message, "String") {
			return
		}
	}
	t.Fatalf("expected diagnostic to name field `naughty` of type `String`:\n%s", formatDiags(out))
}

func TestPodReportsAllOffendingFields(t *testing.T) {
	src := `
#[pod]
#[repr(c)]
struct Bad {
    pub a: String,
    pub b: List<Int>,
    pub c: Bytes,
}
`
	assertPodCount(t, src, 3)
}
