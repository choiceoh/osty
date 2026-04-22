package llvmgen

import (
	"strings"
	"testing"
)

// TestInlineAlwaysEmitsAttr checks that `#[inline(always)]` puts the
// `alwaysinline` keyword on the `define` line, between the closing
// paren of the param list and the `{` that opens the body. Order of
// surrounding attributes (e.g. `ccc`) is not asserted — the test only
// checks for the presence of `alwaysinline` so future attribute
// additions don't cause brittle ordering churn.
func TestInlineAlwaysEmitsAttr(t *testing.T) {
	file := parseLLVMGenFile(t, `#[inline(always)]
fn tiny(n: Int) -> Int { n + 1 }

fn main() {
    let _ = tiny(42)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/inline_always.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, "define i64 @tiny(i64 %n) alwaysinline {") &&
		!strings.Contains(got, "alwaysinline {") {
		t.Fatalf("expected `alwaysinline` attr on tiny's define line; got:\n%s", got)
	}
}

// TestInlineNeverEmitsAttr mirrors the above for the hard "no" case.
func TestInlineNeverEmitsAttr(t *testing.T) {
	file := parseLLVMGenFile(t, `#[inline(never)]
fn big(n: Int) -> Int { n + 1 }

fn main() {
    let _ = big(42)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/inline_never.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, "noinline {") {
		t.Fatalf("expected `noinline` attr on big's define line; got:\n%s", got)
	}
}

// TestInlineSoftEmitsHint checks bare `#[inline]` uses the soft
// LLVM keyword `inlinehint` rather than the hard `alwaysinline`.
func TestInlineSoftEmitsHint(t *testing.T) {
	file := parseLLVMGenFile(t, `#[inline]
fn small(n: Int) -> Int { n + 1 }

fn main() {
    let _ = small(42)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/inline_hint.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, "inlinehint {") {
		t.Fatalf("bare #[inline] should emit `inlinehint` attr; got:\n%s", got)
	}
	if strings.Contains(got, "alwaysinline") {
		t.Fatalf("bare #[inline] must not emit the hard `alwaysinline` attr; got:\n%s", got)
	}
}

// TestHotEmitsAttr and TestColdEmitsAttr check each frequency hint
// independently. Two tests rather than a combined matrix because
// `#[hot] #[cold]` on the same fn is E0609 (duplicate annotation) —
// not a codegen concern.
func TestHotEmitsAttr(t *testing.T) {
	file := parseLLVMGenFile(t, `#[hot]
fn fast(n: Int) -> Int { n * 2 }

fn main() {
    let _ = fast(42)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/hot_attr.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, " hot {") && !strings.Contains(got, "hot \"") {
		t.Fatalf("#[hot] should emit the `hot` LLVM fn attr; got:\n%s", got)
	}
}

func TestColdEmitsAttr(t *testing.T) {
	file := parseLLVMGenFile(t, `#[cold]
fn errPath() -> Int { -1 }

fn main() {
    let _ = errPath()
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/cold_attr.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, " cold {") && !strings.Contains(got, "cold \"") {
		t.Fatalf("#[cold] should emit the `cold` LLVM fn attr; got:\n%s", got)
	}
}

// TestTargetFeatureEmitsStringAttr checks the multi-feature form
// renders as a single comma-separated `target-features` string attr.
// Each feature gets a `+` prefix per LLVM convention.
func TestTargetFeatureEmitsStringAttr(t *testing.T) {
	file := parseLLVMGenFile(t, `#[target_feature(avx512f, avx512bw)]
fn wide(n: Int) -> Int {
    let mut acc = 0
    for i in 0..n { acc = acc ^ i }
    acc
}

fn main() {
    let _ = wide(1024)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/target_feature.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, `"target-features"="+avx512f,+avx512bw"`) {
		t.Fatalf("#[target_feature(...)] should emit the combined `target-features` string attr with `+` prefixes; got:\n%s", got)
	}
}

// TestCombinedFnAttrsCompose confirms A8+A9+A10 can stack on the
// same function. The exact output is one `define` line with every
// applicable attribute keyword present.
func TestCombinedFnAttrsCompose(t *testing.T) {
	file := parseLLVMGenFile(t, `#[inline(always)]
#[hot]
#[target_feature(avx2)]
fn superHot(n: Int) -> Int { n * 3 + 1 }

fn main() {
    let _ = superHot(42)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/combined_fn_attrs.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)
	// Locate the define line for superHot.
	defineIdx := strings.Index(got, "define i64 @superHot")
	if defineIdx < 0 {
		t.Fatalf("define line for superHot not found in IR:\n%s", got)
	}
	lineEnd := strings.IndexByte(got[defineIdx:], '\n')
	defineLine := got[defineIdx : defineIdx+lineEnd]
	for _, want := range []string{"alwaysinline", "hot", `"target-features"="+avx2"`} {
		if !strings.Contains(defineLine, want) {
			t.Fatalf("superHot define line missing %q:\n  %s", want, defineLine)
		}
	}
}

// TestFnAttrsScopedToAnnotatedFunction — attributes are function-
// local. A sibling unannotated fn must not gain them just because
// another fn in the module is annotated.
func TestFnAttrsScopedToAnnotatedFunction(t *testing.T) {
	file := parseLLVMGenFile(t, `#[inline(always)]
fn tiny(n: Int) -> Int { n + 1 }

fn normal(n: Int) -> Int { n + 2 }

fn main() {
    let _ = tiny(1)
    let _ = normal(1)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/fn_attrs_scoped.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)
	// Find the `define i64 @normal(` line and verify it has no
	// attribute keywords between the closing paren and the brace.
	idx := strings.Index(got, "define i64 @normal(")
	if idx < 0 {
		t.Fatalf("normal function not found in IR:\n%s", got)
	}
	lineEnd := strings.IndexByte(got[idx:], '\n')
	line := got[idx : idx+lineEnd]
	if !strings.HasSuffix(line, ") {") {
		t.Fatalf("normal's define line should end `) {` (no attrs), got:\n  %s", line)
	}
}
