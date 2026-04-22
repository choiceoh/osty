package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	ir "github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// TestVectorizeAnnotationEmitsLoopMetadata checks that a range-based
// for-loop inside a `#[vectorize]` function has `!llvm.loop !N` metadata
// attached to its backedge branch and that the corresponding self-
// referential metadata node + `llvm.loop.vectorize.enable` property
// appear at module tail.
func TestVectorizeAnnotationEmitsLoopMetadata(t *testing.T) {
	file := parseLLVMGenFile(t, `#[vectorize]
fn hot(n: Int) -> Int {
    let mut acc = 0
    for i in 0..n {
        acc = acc + i
    }
    acc
}

fn main() {
    let _ = hot(1024)
}
`)

	irBytes, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/vectorize_range.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(irBytes)

	if !strings.Contains(got, ", !llvm.loop !") {
		t.Fatalf("expected backedge branch with !llvm.loop metadata, got:\n%s", got)
	}
	if !strings.Contains(got, `!"llvm.loop.vectorize.enable", i1 true`) {
		t.Fatalf("expected vectorize-enable metadata node, got:\n%s", got)
	}
	if !strings.Contains(got, "distinct !{") {
		t.Fatalf("expected distinct self-referential loop md node, got:\n%s", got)
	}
}

// TestVectorizeAnnotationAbsentEmitsNoLoopMetadata is the negative
// control: without the annotation, no loop metadata should be emitted
// and the IR should be byte-identical to today's baseline shape.
func TestVectorizeIsDefaultOn(t *testing.T) {
	file := parseLLVMGenFile(t, `fn hot(n: Int) -> Int {
    let mut acc = 0
    for i in 0..n {
        acc = acc + i
    }
    acc
}

fn main() {
    let _ = hot(1024)
}
`)

	irBytes, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/vectorize_default_on.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(irBytes)

	// v0.6 A5.2 flip: every function's loops get the vectorize hint
	// unless the function carries `#[no_vectorize]`. Typing no
	// annotation is the fast path now.
	if !strings.Contains(got, "!llvm.loop") {
		t.Fatalf("v0.6 default should emit !llvm.loop metadata; got:\n%s", got)
	}
	if !strings.Contains(got, `!"llvm.loop.vectorize.enable", i1 true`) {
		t.Fatalf("v0.6 default should emit vectorize.enable; got:\n%s", got)
	}
}

// TestNoVectorizeAnnotationOptsOut covers the opt-out: `#[no_vectorize]`
// suppresses both the loop metadata and the safepoint skip, giving
// back the pre-v0.6 GC-cooperative shape for functions that need
// mid-loop yielding.
func TestNoVectorizeAnnotationOptsOut(t *testing.T) {
	file := parseLLVMGenFile(t, `#[no_vectorize]
fn cold(n: Int) -> Int {
    let mut acc = 0
    for i in 0..n {
        acc = acc + i
    }
    acc
}

fn main() {
    let _ = cold(1024)
}
`)

	irBytes, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/vectorize_opt_out.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(irBytes)

	cold, ok := extractFunctionBody(got, "cold")
	if !ok {
		t.Fatalf("cold function not found in IR:\n%s", got)
	}
	if strings.Contains(cold, "!llvm.loop") {
		t.Fatalf("`#[no_vectorize]` should suppress !llvm.loop; got:\n%s", cold)
	}
	if !strings.Contains(cold, "osty.gc.safepoint_v1") {
		t.Fatalf("`#[no_vectorize]` should restore per-iteration safepoint polls; got:\n%s", cold)
	}
}

// TestNoVectorizeScopedToAnnotatedFunction asserts that `#[no_vectorize]`
// is function-local: a loop in an unannotated sibling fn still gets
// the default vectorize hint, while the opt-out fn keeps its
// scalar-with-safepoint shape.
func TestNoVectorizeScopedToAnnotatedFunction(t *testing.T) {
	file := parseLLVMGenFile(t, `fn hot(n: Int) -> Int {
    let mut a = 0
    for i in 0..n {
        a = a + i
    }
    a
}

#[no_vectorize]
fn cold(n: Int) -> Int {
    let mut b = 0
    for j in 0..n {
        b = b + j
    }
    b
}

fn main() {
    let _ = hot(1)
    let _ = cold(1)
}
`)

	irBytes, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/no_vectorize_scoped.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(irBytes)

	// Default-on + one opt-out => exactly one `!llvm.loop` attachment
	// (on `hot`). `cold` carries safepoint, no metadata.
	attachments := strings.Count(got, ", !llvm.loop !")
	if attachments != 1 {
		t.Fatalf("expected exactly 1 loop metadata attachment, got %d:\n%s", attachments, got)
	}
}

// TestVectorizeAnnotationOnMultipleLoopsAllocatesDistinctMD checks that
// two loops in the same `#[vectorize]` function each receive their own
// self-referential metadata node (distinct semantics).
func TestVectorizeAnnotationOnMultipleLoopsAllocatesDistinctMD(t *testing.T) {
	file := parseLLVMGenFile(t, `#[vectorize]
fn twoLoops(n: Int) -> Int {
    let mut a = 0
    for i in 0..n {
        a = a + i
    }
    for j in 0..n {
        a = a + j
    }
    a
}

fn main() {
    let _ = twoLoops(8)
}
`)

	irBytes, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/vectorize_multi.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(irBytes)

	attachments := strings.Count(got, ", !llvm.loop !")
	if attachments != 2 {
		t.Fatalf("expected 2 loop metadata attachments, got %d:\n%s", attachments, got)
	}
	distincts := strings.Count(got, "distinct !{")
	if distincts != 2 {
		t.Fatalf("expected 2 distinct loop md nodes, got %d:\n%s", distincts, got)
	}
}

// TestVectorizeAnnotationFlowsThroughMIRPipeline is the end-to-end
// analogue of the HIR tests above: it runs the full ast → resolve →
// check → ir.Lower → ir.Monomorphize → mir.Lower → GenerateFromMIR
// pipeline and asserts that `#[vectorize]` survives each stage and
// produces the loop-vectorize metadata in the default MIR-first code
// path. The HIR-only tests above cover the legacy emitter; this test
// pins the MIR emitter that the backend dispatcher actually selects
// for raw `llvm-ir` emission today (see internal/backend/llvm.go's
// useMIRBackend).
func TestVectorizeAnnotationFlowsThroughMIRPipeline(t *testing.T) {
	src := `
#[vectorize]
pub fn sumTo(n: Int) -> Int {
    let mut acc = 0
    for i in 0..n {
        acc = acc + i
    }
    acc
}

fn main() {
    let _ = sumTo(1024)
}
`
	file := parseLLVMGenFile(t, src)
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.File(file, res, check.Opts{
		UseGolegacy:   true,
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
	})
	mod, issues := ir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower issues: %v", issues)
	}

	var foundIR *ir.FnDecl
	for _, decl := range mod.Decls {
		if fd, ok := decl.(*ir.FnDecl); ok && fd.Name == "sumTo" {
			foundIR = fd
			break
		}
	}
	if foundIR == nil {
		t.Fatal("sumTo not found in IR module")
	}
	if !foundIR.Vectorize {
		t.Fatal("ir.FnDecl Vectorize = false, expected true (#[vectorize] not propagated from AST)")
	}

	monoMod, _ := ir.Monomorphize(mod)
	mirMod := mir.Lower(monoMod)
	var foundMIR *mir.Function
	for _, f := range mirMod.Functions {
		if f != nil && f.Name == "sumTo" {
			foundMIR = f
			break
		}
	}
	if foundMIR == nil {
		t.Fatal("sumTo not found in MIR module")
	}
	if !foundMIR.Vectorize {
		t.Fatal("mir.Function Vectorize = false, expected true (not propagated from IR to MIR)")
	}

	out, err := GenerateFromMIR(mirMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/vectorize_mir_e2e.osty",
	})
	if err != nil {
		t.Fatalf("GenerateFromMIR error: %v", err)
	}
	got := string(out)

	if !strings.Contains(got, ", !llvm.loop !") {
		t.Fatalf("MIR-emitted IR missing backedge metadata:\n%s", got)
	}
	if !strings.Contains(got, `!"llvm.loop.vectorize.enable", i1 true`) {
		t.Fatalf("MIR-emitted IR missing vectorize-enable property:\n%s", got)
	}
	if !strings.Contains(got, "distinct !{") {
		t.Fatalf("MIR-emitted IR missing distinct self-referential loop md:\n%s", got)
	}
}
