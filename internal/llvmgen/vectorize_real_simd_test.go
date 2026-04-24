package llvmgen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// lowerThroughMIR runs the same full pipeline as `osty build --backend
// llvm`: parse → resolve → check → ir.Lower → Monomorphize → mir.Lower
// → GenerateFromMIR. Tests that want to assert on the IR the native
// dispatcher actually selects must go through this path; `generateFromAST`
// is the legacy HIR emitter and doesn't reach the MIR-only fast-path
// machinery.
func lowerThroughMIR(t *testing.T, src string) string {
	t.Helper()
	file := parseLLVMGenFile(t, src)
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{
		
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
	})
	mod, issues := ir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower: %v", issues)
	}
	monoMod, _ := ir.Monomorphize(mod)
	mirMod := mir.Lower(monoMod)
	out, err := GenerateFromMIR(mirMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/vectorize_mir_probe.osty",
	})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	return string(out)
}

// TestVectorizeFunctionBodyIsCallFree pins the GC-contract side of the
// v0.6 A5 delivery: inside a `#[vectorize]` function the lowered loop
// body must not contain `call void @osty.gc.safepoint_v1(...)`. LLVM's
// loop vectorizer treats any call inside the latch as a potential
// side-effect and bails; the whole point of this test is that the
// contract documented in LANG_SPEC §3.8.3 actually holds at the IR
// level so the vectorizer has a chance to fire on -O2/-O3.
//
// A sibling unannotated function in the same file keeps its per-
// iteration poll (negative control). This prevents the safepoint
// opt-out from regressing into a module-wide "no loop polls" bug.
func TestVectorizeFunctionBodyIsCallFree(t *testing.T) {
	// v0.6 A5.2 flip: default is vectorize-ON (no annotation). The
	// opt-out is `#[no_vectorize]`, which restores per-iteration
	// safepoint polls. The test keeps one fn on each side of the
	// default so a regression where one leaks into the other would be
	// caught.
	file := parseLLVMGenFile(t, `fn hot(n: Int) -> Int {
    let mut acc = 0
    for i in 0..n {
        acc = acc + i
    }
    acc
}

#[no_vectorize]
fn cold(n: Int) -> Int {
    let mut acc = 0
    for i in 0..n {
        acc = acc + i
    }
    acc
}

fn main() {
    let _ = hot(1)
    let _ = cold(1)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/vectorize_callfree.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)

	hot, ok := extractFunctionBody(got, "hot")
	if !ok {
		t.Fatalf("hot function not found in IR:\n%s", got)
	}
	if strings.Contains(hot, "osty.gc.safepoint_v1") {
		t.Fatalf("default-on vectorize should keep hot body call-free "+
			"(LLVM vectorizer would otherwise bail):\n%s", hot)
	}

	cold, ok := extractFunctionBody(got, "cold")
	if !ok {
		t.Fatalf("cold function not found in IR:\n%s", got)
	}
	if !strings.Contains(cold, "osty.gc.safepoint_v1") {
		t.Fatalf("`#[no_vectorize]` should restore per-iteration safepoints "+
			"and the opt-out must not leak to sibling functions:\n%s", cold)
	}
}

// TestVectorizeAnnotationLetsClangVectorize is the integration oracle:
// pipe an annotated XOR-reduction IR into clang at -O3, parse its
// `-Rpass=loop-vectorize` remarks, and assert we see at least one
// "vectorized loop" message. XOR was chosen on purpose — a plain
// arithmetic-series sum (`acc = acc + i`) can be closed-formed by
// InstCombine and the entire loop disappears, which is a great outcome
// but indistinguishable from "vectorizer didn't fire." XOR reduction
// survives every algebraic folder while still being a textbook
// reducible operation, so the remarks are meaningful.
//
// Skipped when `clang` isn't on PATH (cross-platform CI friendliness).
func TestVectorizeAnnotationLetsClangVectorize(t *testing.T) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not on PATH — skipping integration oracle")
	}

	file := parseLLVMGenFile(t, `#[vectorize]
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
		SourcePath:  "/tmp/vectorize_clang_oracle.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	irPath := filepath.Join(t.TempDir(), "module.ll")
	if err := writeBytes(irPath, ir); err != nil {
		t.Fatalf("write ir: %v", err)
	}

	cmd := exec.Command(clangPath,
		"-x", "ir",
		"-O3",
		"-S",
		"-o", filepath.Join(t.TempDir(), "module.s"),
		"-Rpass=loop-vectorize",
		irPath,
	)
	out, runErr := cmd.CombinedOutput()
	remarks := string(out)

	// clang prints its remarks to stderr; CombinedOutput merges them
	// with stdout, so a non-zero exit is genuinely a failure (not just
	// noisy remarks on a successful build).
	if runErr != nil {
		t.Fatalf("clang -O3 on vectorize-annotated IR failed: %v\n%s",
			runErr, remarks)
	}
	if !strings.Contains(remarks, "vectorized loop") {
		t.Fatalf("clang -O3 on annotated IR did not vectorize the loop; "+
			"expected a `remark: vectorized loop` line. Full output:\n%s",
			remarks)
	}
}

// TestVectorizeAppliesToListLocalReadLoop pins the "plain everyday code
// should SIMD" promise of the default-on vectorize flip: build a local
// `List<Int>`, then run a read-only XOR reduction over it. The MIR
// emitter has long had a fast-path for *parameter* lists, but before
// the lazy snapshot was added the caller had no way to cover locals —
// so the reduction loop kept calling `@osty_rt_list_get_i64` per
// iteration and LLVM's vectorizer bailed ("call instruction cannot be
// vectorized"). This test is the regression oracle for that gap:
// without the MIR-side snapshot + `memory(read)` attributes on the
// list runtime surface, it fails at the clang remark check.
func TestVectorizeAppliesToListLocalReadLoop(t *testing.T) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not on PATH — skipping integration oracle")
	}

	src := `fn computeXor(n: Int) -> Int {
    let mut xs: List<Int> = []
    let mut k = 0
    while k < n {
        xs.push(k)
        k = k + 1
    }
    let mut acc = 0
    for j in 0..xs.len() {
        acc = acc ^ xs[j]
    }
    acc
}

fn main() {
    let _ = computeXor(64)
}
`
	ir := lowerThroughMIR(t, src)

	irPath := filepath.Join(t.TempDir(), "module.ll")
	if err := writeBytes(irPath, []byte(ir)); err != nil {
		t.Fatalf("write ir: %v", err)
	}
	cmd := exec.Command(clangPath,
		"-x", "ir",
		"-O3",
		"-S",
		"-o", filepath.Join(t.TempDir(), "module.s"),
		"-Rpass=loop-vectorize",
		irPath,
	)
	out, runErr := cmd.CombinedOutput()
	remarks := string(out)
	if runErr != nil {
		t.Fatalf("clang -O3 on list-local IR failed: %v\n%s", runErr, remarks)
	}
	if !strings.Contains(remarks, "vectorized loop") {
		fnBody, _ := extractFunctionBody(ir, "computeXor")
		t.Fatalf("clang -O3 did not vectorize a read-only loop over a local "+
			"List<Int>; default-on vectorize is effectively a no-op on "+
			"everyday code. Remarks:\n%s\n\ncomputeXor body:\n%s",
			remarks, fnBody)
	}
}

// TestVectorizeLazySnapshotRespectsInLoopMutation is the soundness
// oracle for the lazy-snapshot fast path: when a subscript read shares
// a loop body with a mutation (push/set/insert) on the same local,
// caching `data`/`len` across iterations would hand out stale pointers
// after a reallocation. The CFG reachability gate must detect this and
// fall back to the typed slow-path runtime call — we pin that by
// asserting the emitted IR does NOT grow a `list.fast.*` branch inside
// the mixed loop, and the slow-path `@osty_rt_list_get_i64` call stays
// in the loop body.
func TestVectorizeLazySnapshotRespectsInLoopMutation(t *testing.T) {
	src := `fn mixed(n: Int) -> Int {
    let mut xs: List<Int> = []
    let mut acc = 0
    for i in 0..n {
        xs.push(i)
        acc = acc ^ xs[0]
    }
    acc
}

fn main() {
    let _ = mixed(8)
}
`
	ir := lowerThroughMIR(t, src)
	body, ok := extractFunctionBody(ir, "mixed")
	if !ok {
		t.Fatalf("mixed function not found:\n%s", ir)
	}
	if strings.Contains(body, "list.fast.") {
		t.Fatalf("lazy fast path fired for a local that is mutated in "+
			"the same loop as the read — would cache a stale data ptr "+
			"across a realloc. Body:\n%s", body)
	}
	if !strings.Contains(body, "@osty_rt_list_get_i64") {
		t.Fatalf("expected the typed slow-path call to still be present "+
			"in the mixed loop (soundness fallback). Body:\n%s", body)
	}
}

// extractFunctionBody returns the text between `define ... @name(...) {`
// and the matching closing `}` on a line by itself. It's a tiny helper
// for tests that need to scope substring assertions to one function
// without dragging in a real LLVM IR parser.
func extractFunctionBody(module, name string) (string, bool) {
	needle := "@" + name + "("
	start := strings.Index(module, needle)
	if start < 0 {
		return "", false
	}
	// Walk to the `{` that opens this function's body.
	brace := strings.IndexByte(module[start:], '{')
	if brace < 0 {
		return "", false
	}
	start += brace + 1
	// Matching `}` on its own line is unique in our emitter — bodies
	// never contain a bare `}` at column zero.
	end := strings.Index(module[start:], "\n}\n")
	if end < 0 {
		return "", false
	}
	return module[start : start+end], true
}

func writeBytes(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}
