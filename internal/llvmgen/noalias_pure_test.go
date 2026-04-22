package llvmgen

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func osReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

// TestNoaliasBareAppliesToAllPointerParams pins the rule that bare
// `#[noalias]` stamps every `ptr`-typed parameter with the LLVM
// `noalias` attribute while leaving non-pointer params untouched.
func TestNoaliasBareAppliesToAllPointerParams(t *testing.T) {
	file := parseLLVMGenFile(t, `#[noalias]
fn sumDot(xs: List<Int>, ys: List<Int>, n: Int) -> Int {
    let mut acc = 0
    for i in 0..n { acc = acc + xs[i] * ys[i] }
    acc
}

fn main() {
    let xs = [1, 2]
    let _ = sumDot(xs, xs, 2)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/noalias_bare.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)
	// Every ptr param on sumDot should carry `noalias`; i64 params
	// must not. The exact define shape is:
	//   define i64 @sumDot(ptr noalias %xs, ptr noalias %ys, i64 %n) {
	if !strings.Contains(got, "ptr noalias") {
		t.Fatalf("expected `ptr noalias` on pointer params; got:\n%s", got)
	}
	// Find the sumDot define line and count its `ptr noalias`
	// occurrences — should be exactly 2 (one per pointer param).
	idx := strings.Index(got, "define i64 @sumDot(")
	if idx < 0 {
		t.Fatalf("sumDot define line not found in IR:\n%s", got)
	}
	lineEnd := strings.IndexByte(got[idx:], '\n')
	defineLine := got[idx : idx+lineEnd]
	if c := strings.Count(defineLine, "ptr noalias"); c != 2 {
		t.Fatalf("expected 2 `ptr noalias` on sumDot signature, got %d:\n  %s",
			c, defineLine)
	}
	// i64 %n must not gain an i64 noalias annotation — LLVM rejects
	// noalias on non-pointer types.
	if strings.Contains(defineLine, "i64 noalias") {
		t.Fatalf("noalias must not attach to non-pointer params:\n  %s", defineLine)
	}
}

// TestNoaliasParamListIsSurgical checks `#[noalias(src)]` stamps only
// the named parameter, leaving the unnamed pointer parameter
// without the attribute.
func TestNoaliasParamListIsSurgical(t *testing.T) {
	file := parseLLVMGenFile(t, `#[noalias(src)]
fn copy(src: List<Int>, dst: List<Int>, n: Int) -> Int {
    let mut acc = 0
    for i in 0..n { acc = acc + src[i] }
    acc
}

fn main() {
    let xs = [1, 2]
    let _ = copy(xs, xs, 2)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/noalias_param_list.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)
	idx := strings.Index(got, "define i64 @copy(")
	if idx < 0 {
		t.Fatalf("copy define line not found in IR:\n%s", got)
	}
	lineEnd := strings.IndexByte(got[idx:], '\n')
	defineLine := got[idx : idx+lineEnd]
	// Exactly one `ptr noalias` — the src param.
	if c := strings.Count(defineLine, "ptr noalias"); c != 1 {
		t.Fatalf("expected 1 `ptr noalias` (src only), got %d:\n  %s", c, defineLine)
	}
	// And the dst param should still be `ptr %dst` without noalias.
	if !strings.Contains(defineLine, "ptr %dst") {
		t.Fatalf("expected plain `ptr %%dst` for unlisted param; got:\n  %s",
			defineLine)
	}
}

// TestPureEmitsReadnone checks the `#[pure]` → `readnone` mapping.
func TestPureEmitsReadnone(t *testing.T) {
	file := parseLLVMGenFile(t, `#[pure]
fn hash(a: Int, b: Int) -> Int { a * 31 + b }

fn main() {
    let _ = hash(1, 2)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/pure_attr.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, "readnone {") && !strings.Contains(got, "readnone \"") {
		t.Fatalf("expected `readnone` fn attr; got:\n%s", got)
	}
}

// TestPureDoesNotLeakToSiblings — the `readnone` attr is function-
// local. An unannotated sibling must not pick it up.
func TestPureDoesNotLeakToSiblings(t *testing.T) {
	file := parseLLVMGenFile(t, `#[pure]
fn hashA(a: Int, b: Int) -> Int { a * 31 + b }

fn hashB(a: Int, b: Int) -> Int { a + b * 17 }

fn main() {
    let _ = hashA(1, 2) + hashB(3, 4)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/pure_scoped.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)
	hashB, ok := extractFunctionBody(got, "hashB")
	if !ok {
		t.Fatalf("hashB not found in IR:\n%s", got)
	}
	// Look at the define line for hashB specifically — the body
	// itself never contains "readnone" as a keyword, but the line
	// preceding the body would if we leaked.
	idx := strings.Index(got, "define i64 @hashB(")
	if idx < 0 {
		t.Fatalf("hashB define line not found:\n%s", got)
	}
	lineEnd := strings.IndexByte(got[idx:], '\n')
	defineLine := got[idx : idx+lineEnd]
	if strings.Contains(defineLine, "readnone") {
		t.Fatalf("unannotated hashB leaked a readnone attr:\n  %s", defineLine)
	}
	_ = hashB
}

// TestNoaliasCombineWithPureAndHot — the three v0.6 attribute tracks
// compose on one function. The resulting define line must carry
// every applicable keyword (noalias on ptr params, readnone + hot as
// fn attrs).
func TestNoaliasCombineWithPureAndHot(t *testing.T) {
	file := parseLLVMGenFile(t, `#[noalias]
#[pure]
#[hot]
fn superHot(xs: List<Int>, n: Int) -> Int {
    let mut acc = 0
    for i in 0..n { acc = acc + xs[i] }
    acc
}

fn main() {
    let xs = [1, 2]
    let _ = superHot(xs, 2)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/combined_tier2.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	got := string(ir)
	idx := strings.Index(got, "define i64 @superHot(")
	if idx < 0 {
		t.Fatalf("superHot define line not found:\n%s", got)
	}
	lineEnd := strings.IndexByte(got[idx:], '\n')
	defineLine := got[idx : idx+lineEnd]
	for _, want := range []string{"ptr noalias", "hot", "readnone"} {
		if !strings.Contains(defineLine, want) {
			t.Fatalf("combined superHot missing %q:\n  %s", want, defineLine)
		}
	}
}

// TestPureEnablesCSEInClang is the clang oracle: passing an annotated
// `readnone` function through -O3 should let the optimizer CSE
// repeated calls with identical arguments. We verify by counting the
// remaining calls to the pure function in the caller's assembly —
// if CSE succeeded, the three source-level calls fold into one or
// fewer. Skipped when `clang` is not on PATH.
func TestPureEnablesCSEInClang(t *testing.T) {
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not on PATH — skipping CSE oracle")
	}

	file := parseLLVMGenFile(t, `#[pure]
fn computeKey(x: Int, y: Int) -> Int {
    x * 31 + y
}

fn caller(a: Int, b: Int) -> Int {
    let k1 = computeKey(a, b)
    let k2 = computeKey(a, b)
    let k3 = computeKey(a, b)
    k1 + k2 + k3
}

fn main() {
    let _ = caller(7, 11)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/pure_cse_oracle.osty",
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	tmp := t.TempDir()
	irPath := tmp + "/module.ll"
	if err := writeBytes(irPath, ir); err != nil {
		t.Fatalf("write ir: %v", err)
	}
	asmPath := tmp + "/module.s"
	cmd := exec.Command(clangPath, "-x", "ir", "-O3", "-S", "-o", asmPath, irPath)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("clang -O3 failed: %v\n%s", runErr, out)
	}
	asmBytes, err := readFileBytes(asmPath)
	if err != nil {
		t.Fatalf("read asm: %v", err)
	}
	asm := string(asmBytes)
	// Count `computeKey` call sites in the caller's assembly. With
	// `readnone`, LLVM should CSE + inline them down to 0-1
	// occurrences. Without the attr it would keep 3 separate calls.
	// We assert "fewer than 3" rather than "exactly 0" because the
	// exact folding depends on the target architecture's ABI.
	callCount := strings.Count(asm, "bl\tcomputeKey") +
		strings.Count(asm, "bl\t_computeKey") +
		strings.Count(asm, "call\tcomputeKey") +
		strings.Count(asm, "callq\tcomputeKey") +
		strings.Count(asm, "callq\t_computeKey")
	if callCount >= 3 {
		t.Fatalf("expected #[pure] to enable CSE and fold 3 calls to < 3; "+
			"got %d call sites in asm. Full asm:\n%s", callCount, asm)
	}
}

func readFileBytes(path string) ([]byte, error) {
	return osReadFile(path)
}
