package llvmgen

import (
	"os/exec"
	"strings"
	"testing"
)

// TestVectorizeWidthEmitsMetadata checks that `#[vectorize(width = 8)]`
// produces a `llvm.loop.vectorize.width, i32 8` property inside the
// loop metadata node, not just a bare `vectorize.enable` flag. Both
// emitter paths (HIR via generateFromAST) must emit the same shape.
func TestVectorizeWidthEmitsMetadata(t *testing.T) {
	file := parseLLVMGenFile(t, `#[vectorize(width = 8)]
fn hot(n: Int) -> Int {
    let mut a = 0
    for i in 0..n {
        a = a + i
    }
    a
}

fn main() {
    let _ = hot(1024)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/vectorize_width.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, `!"llvm.loop.vectorize.width", i32 8`) {
		t.Fatalf("expected vectorize.width property with i32 8, got:\n%s", got)
	}
}

// TestVectorizeScalableEmitsMetadata locks in the scalable-hint
// shape: `#[vectorize(scalable)]` → `vectorize.scalable.enable=true`.
func TestVectorizeScalableEmitsMetadata(t *testing.T) {
	file := parseLLVMGenFile(t, `#[vectorize(scalable)]
fn hot(n: Int) -> Int {
    let mut a = 0
    for i in 0..n {
        a = a + i
    }
    a
}

fn main() {
    let _ = hot(8)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/vectorize_scalable.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, `!"llvm.loop.vectorize.scalable.enable", i1 true`) {
		t.Fatalf("expected scalable.enable property, got:\n%s", got)
	}
}

// TestVectorizePredicateEmitsMetadata confirms the tail-folding hint
// shape: `#[vectorize(predicate)]` →
// `vectorize.predicate.enable=true`.
func TestVectorizePredicateEmitsMetadata(t *testing.T) {
	file := parseLLVMGenFile(t, `#[vectorize(predicate)]
fn hot(n: Int) -> Int {
    let mut a = 0
    for i in 0..n {
        a = a + i
    }
    a
}

fn main() {
    let _ = hot(8)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/vectorize_predicate.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, `!"llvm.loop.vectorize.predicate.enable", i1 true`) {
		t.Fatalf("expected predicate.enable property, got:\n%s", got)
	}
}

// TestParallelAccessGroupTagsLoadsAndStores locks in the v0.6 A6
// invariant: a per-function `!llvm.access.group` node is allocated,
// every load/store in the body carries an `!llvm.access.group !N`
// attachment, and the loop metadata references the same group via
// `llvm.loop.parallel_accesses`.
func TestParallelAccessGroupTagsLoadsAndStores(t *testing.T) {
	file := parseLLVMGenFile(t, `#[parallel]
fn hot(n: Int) -> Int {
    let mut a = 0
    for i in 0..n {
        a = a + i
    }
    a
}

fn main() {
    let _ = hot(8)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/parallel_access_group.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)

	if !strings.Contains(got, "distinct !{}") {
		t.Fatalf("expected empty-tuple access group node `distinct !{}`, got:\n%s", got)
	}
	if !strings.Contains(got, `!"llvm.loop.parallel_accesses"`) {
		t.Fatalf("expected loop metadata to reference parallel_accesses, got:\n%s", got)
	}
	// Every load/store in the annotated function body should carry
	// the attachment. Spot-check by counting tag occurrences — if the
	// post-processor leaked or missed sites we'd see 0 or only the
	// loop-metadata reference.
	tagCount := strings.Count(got, "!llvm.access.group !")
	if tagCount < 3 {
		t.Fatalf("expected several `!llvm.access.group !N` attachments "+
			"(load/store + loop ref), got %d:\n%s", tagCount, got)
	}
}

// TestParallelDoesNotLeakToSiblingFunction — the access-group
// attachment is per-function. A sibling unannotated fn must not gain
// `!llvm.access.group` tags even when it shares the module with a
// `#[parallel]` fn.
func TestParallelDoesNotLeakToSiblingFunction(t *testing.T) {
	file := parseLLVMGenFile(t, `#[parallel]
fn hot(n: Int) -> Int {
    let mut a = 0
    for i in 0..n {
        a = a + i
    }
    a
}

fn cold(n: Int) -> Int {
    let mut a = 0
    for i in 0..n {
        a = a + i
    }
    a
}

fn main() {
    let _ = hot(1)
    let _ = cold(1)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/parallel_scoped.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)

	cold, ok := extractFunctionBody(got, "cold")
	if !ok {
		t.Fatalf("cold function not found in IR:\n%s", got)
	}
	if strings.Contains(cold, "!llvm.access.group") {
		t.Fatalf("unannotated `cold` body picked up an access-group tag:\n%s", cold)
	}
}

// TestUnrollBareEmitsEnable checks the `#[unroll]` bare-flag shape
// emits `llvm.loop.unroll.enable=true`, not a count-based property.
func TestUnrollBareEmitsEnable(t *testing.T) {
	file := parseLLVMGenFile(t, `#[unroll]
fn hot(n: Int) -> Int {
    let mut a = 0
    for i in 0..n {
        a = a + i
    }
    a
}

fn main() {
    let _ = hot(8)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/unroll_bare.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, `!"llvm.loop.unroll.enable", i1 true`) {
		t.Fatalf("expected unroll.enable=true property, got:\n%s", got)
	}
	if strings.Contains(got, "llvm.loop.unroll.count") {
		t.Fatalf("bare `#[unroll]` must not emit unroll.count property:\n%s", got)
	}
}

// TestUnrollCountEmitsCount checks that `#[unroll(count = 4)]`
// produces `llvm.loop.unroll.count, i32 4` and suppresses the bare
// enable flag (one or the other, not both).
func TestUnrollCountEmitsCount(t *testing.T) {
	file := parseLLVMGenFile(t, `#[unroll(count = 4)]
fn hot(n: Int) -> Int {
    let mut a = 0
    for i in 0..n {
        a = a + i
    }
    a
}

fn main() {
    let _ = hot(8)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/unroll_count.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, `!"llvm.loop.unroll.count", i32 4`) {
		t.Fatalf("expected unroll.count with i32 4 property, got:\n%s", got)
	}
	if strings.Contains(got, "llvm.loop.unroll.enable") {
		t.Fatalf("count form must not also emit bare unroll.enable:\n%s", got)
	}
}

// TestCombinedHintsProduceOneLoopMD is the integration of all three:
// `#[vectorize(...)] #[parallel] #[unroll(...)]` should yield a
// single self-referential loop metadata node whose property list
// contains every hint's child. If the emitter split them into
// separate nodes, LLVM would only honor the last one attached.
func TestCombinedHintsProduceOneLoopMD(t *testing.T) {
	file := parseLLVMGenFile(t, `#[vectorize(scalable, predicate, width = 8)]
#[parallel]
#[unroll(count = 4)]
fn hot(n: Int) -> Int {
    let mut a = 0
    for i in 0..n {
        a = a + i
    }
    a
}

fn main() {
    let _ = hot(8)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/combined_hints.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)

	wanted := []string{
		`!"llvm.loop.vectorize.enable", i1 true`,
		`!"llvm.loop.vectorize.width", i32 8`,
		`!"llvm.loop.vectorize.scalable.enable", i1 true`,
		`!"llvm.loop.vectorize.predicate.enable", i1 true`,
		`!"llvm.loop.parallel_accesses",`,
		`!"llvm.loop.unroll.count", i32 4`,
	}
	for _, w := range wanted {
		if !strings.Contains(got, w) {
			t.Fatalf("combined-hints IR missing property %q:\n%s", w, got)
		}
	}
	// Exactly one loop attachment on the back-edge.
	if c := strings.Count(got, ", !llvm.loop !"); c != 1 {
		t.Fatalf("expected exactly 1 !llvm.loop attachment, got %d:\n%s", c, got)
	}
}

// TestClangActuallyVectorizesWithParallel is the stronger clang
// oracle: with `#[parallel]` on a list-style access pattern (where
// alias analysis would otherwise bail), we should see clang report
// `vectorized loop` rather than fail the pass.
//
// We reuse the XOR reduction shape from the existing oracle — it
// already vectorizes without `#[parallel]` — but run it with all
// three hints stacked to make sure the combined metadata does not
// confuse the vectorizer.
func TestClangActuallyVectorizesWithCombinedHints(t *testing.T) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not on PATH — skipping integration oracle")
	}
	file := parseLLVMGenFile(t, `#[vectorize(predicate)]
#[parallel]
#[unroll(count = 2)]
fn xorTo(n: Int) -> Int {
    let mut acc = 0
    for i in 0..n {
        acc = acc ^ i
    }
    acc
}

fn main() {
    let _ = xorTo(1024)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/combined_clang_oracle.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	tmp := t.TempDir()
	irPath := tmp + "/module.ll"
	if err := writeBytes(irPath, ir); err != nil {
		t.Fatalf("write ir: %v", err)
	}
	cmd := exec.Command(clangPath,
		"-x", "ir", "-O3", "-S",
		"-o", tmp+"/module.s",
		"-Rpass=loop-vectorize",
		irPath,
	)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("clang -O3 failed on combined-hints IR: %v\n%s", runErr, out)
	}
	if !strings.Contains(string(out), "vectorized loop") {
		t.Fatalf("expected `vectorized loop` remark with combined hints, got:\n%s", out)
	}
}
