package backend

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/osty/osty/internal/onb"
)

type fakeONBLinker struct {
	links []linkCall
}

func (f *fakeONBLinker) LinkBinary(_ context.Context, objectPaths []string, binaryPath, target string, linkLibraries []string) error {
	f.links = append(f.links, linkCall{
		objectPaths:   append([]string(nil), objectPaths...),
		binaryPath:    binaryPath,
		target:        target,
		linkLibraries: append([]string(nil), linkLibraries...),
	})
	return os.WriteFile(binaryPath, []byte("onb fake binary"), 0o755)
}

func TestONBBackendRegisteredAsDevBackend(t *testing.T) {
	t.Parallel()

	name, err := ParseName("onb")
	if err != nil {
		t.Fatalf("ParseName(onb) returned error: %v", err)
	}
	if name != NameONB {
		t.Fatalf("ParseName(onb) = %q, want %q", name, NameONB)
	}
	if !EmitBinary.ValidFor(NameONB) || !EmitObject.ValidFor(NameONB) {
		t.Fatal("ONB should accept object and binary emission")
	}
	if !EmitASM.ValidFor(NameONB) {
		t.Fatal("ONB should accept assembly emission")
	}
	if EmitLLVMIR.ValidFor(NameONB) {
		t.Fatal("ONB must not claim LLVM IR emission")
	}
}

func TestONBBackendEmitAssemblyWritesArtifact(t *testing.T) {
	t.Parallel()

	req := newBackendRequest(t, EmitASM, `fn main() {}`)
	req.Layout.Target = "aarch64-apple-darwin"
	result, err := ONBBackend{}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit(asm) returned error: %v", err)
	}
	if result == nil || result.Artifacts.Assembly == "" {
		t.Fatalf("ONBBackend.Emit(asm) result = %+v", result)
	}
	data, err := os.ReadFile(result.Artifacts.Assembly)
	if err != nil {
		t.Fatalf("ReadFile(%q) returned error: %v", result.Artifacts.Assembly, err)
	}
	text := string(data)
	for _, want := range []string{"_main:", "\tmov w0, #0", "\tret"} {
		if !strings.Contains(text, want) {
			t.Fatalf("assembly missing %q:\n%s", want, text)
		}
	}
}

func TestONBBackendEmitAssemblyForPrintlnLiteral(t *testing.T) {
	t.Parallel()

	req := newBackendRequest(t, EmitASM, `fn main() { println("hi") }`)
	req.Layout.Target = "aarch64-apple-darwin"
	result, err := ONBBackend{}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit(asm) returned error: %v", err)
	}
	data, err := os.ReadFile(result.Artifacts.Assembly)
	if err != nil {
		t.Fatalf("ReadFile(%q) returned error: %v", result.Artifacts.Assembly, err)
	}
	text := string(data)
	for _, want := range []string{"\tbl _puts", ".section __TEXT,__cstring,cstring_literals", "\t.asciz \"hi\""} {
		if !strings.Contains(text, want) {
			t.Fatalf("assembly missing %q:\n%s", want, text)
		}
	}
}

func TestONBBackendEmitAssemblyForPrintlnIntLiteral(t *testing.T) {
	t.Parallel()

	req := newBackendRequest(t, EmitASM, `fn main() { println(123) }`)
	req.Layout.Target = "aarch64-apple-darwin"
	result, err := ONBBackend{}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit(asm) returned error: %v", err)
	}
	data, err := os.ReadFile(result.Artifacts.Assembly)
	if err != nil {
		t.Fatalf("ReadFile(%q) returned error: %v", result.Artifacts.Assembly, err)
	}
	text := string(data)
	for _, want := range []string{"\tmovz x1, #123", "\tstr x1, [sp]", "\tbl _printf", "\t.asciz \"%lld\\n\""} {
		if !strings.Contains(text, want) {
			t.Fatalf("assembly missing %q:\n%s", want, text)
		}
	}
}

func TestONBBackendEmitObjectWritesMachOArtifact(t *testing.T) {
	t.Parallel()

	req := newBackendRequest(t, EmitObject, `fn main() {}`)
	req.Layout.Target = "aarch64-apple-darwin"
	result, err := ONBBackend{}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit(object) returned error: %v", err)
	}
	if result == nil {
		t.Fatal("ONBBackend.Emit() returned nil result")
	}
	if result.Backend != NameONB {
		t.Fatalf("result.Backend = %q, want %q", result.Backend, NameONB)
	}
	if result.Artifacts.Object == "" {
		t.Fatal("ONB object artifact path is empty")
	}
	if _, err := os.Stat(result.Artifacts.Object); err != nil {
		t.Fatalf("ONB object artifact was not written: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("ONB result should include the phase plan warning")
	}
}

func TestONBBackendEmitBinaryInvokesHostLinker(t *testing.T) {
	t.Parallel()

	req := newBackendRequest(t, EmitBinary, `fn main() {}`)
	req.Layout.Target = "aarch64-apple-darwin"
	linker := &fakeONBLinker{}
	result, err := (ONBBackend{linker: linker}).Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit(binary) returned error: %v", err)
	}
	if result == nil || result.Artifacts.Object == "" || result.Artifacts.Binary == "" {
		t.Fatalf("ONBBackend.Emit(binary) result = %+v", result)
	}
	if _, err := os.Stat(result.Artifacts.Object); err != nil {
		t.Fatalf("ONB binary path should leave object artifact for debugging: %v", err)
	}
	if _, err := os.Stat(result.Artifacts.Binary); err != nil {
		t.Fatalf("ONB binary artifact was not written: %v", err)
	}
	if len(linker.links) != 1 {
		t.Fatalf("link calls = %d, want 1", len(linker.links))
	}
	got := linker.links[0].objectPaths
	if len(got) != 2 || got[0] != result.Artifacts.Object {
		t.Fatalf("link object paths = %v, want [%s, <runtime>]", got, result.Artifacts.Object)
	}
	if !strings.HasSuffix(got[1], "osty_runtime.o") {
		t.Fatalf("second link object = %q, want runtime .o (osty_runtime.o suffix)", got[1])
	}
}

func TestONBBackendBinaryRunsUnitMainOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB phase 1.0 executable smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	req := newBackendRequest(t, EmitBinary, `fn main() {}`)
	req.Layout.Target = "aarch64-apple-darwin"
	result, err := ONBBackend{}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit(binary) returned error: %v", err)
	}
	if result == nil || result.Artifacts.Binary == "" {
		t.Fatalf("ONBBackend.Emit(binary) result = %+v", result)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ONB binary returned error: %v\n%s", err, out)
	}
}

func TestONBBackendBinaryRunsPrintlnLiteralOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB println executable smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	req := newBackendRequest(t, EmitBinary, `fn main() { println("hi") }`)
	req.Layout.Target = "aarch64-apple-darwin"
	result, err := ONBBackend{}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit(binary) returned error: %v", err)
	}
	if result == nil || result.Artifacts.Binary == "" {
		t.Fatalf("ONBBackend.Emit(binary) result = %+v", result)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ONB binary returned error: %v\n%s", err, out)
	}
	if string(out) != "hi\n" {
		t.Fatalf("ONB binary output = %q, want hi newline", out)
	}
}

func TestONBBackendBinaryRunsPrintlnIntLiteralOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB println executable smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	req := newBackendRequest(t, EmitBinary, `fn main() { println(123) }`)
	req.Layout.Target = "aarch64-apple-darwin"
	result, err := ONBBackend{}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit(binary) returned error: %v", err)
	}
	if result == nil || result.Artifacts.Binary == "" {
		t.Fatalf("ONBBackend.Emit(binary) result = %+v", result)
	}
	cmd := exec.Command(result.Artifacts.Binary)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ONB binary returned error: %v\n%s", err, out)
	}
	if string(out) != "123\n" {
		t.Fatalf("ONB binary output = %q, want 123 newline", out)
	}
}

// TestONBBackendBinaryRunsArithOnDarwinARM64 exercises the Phase A2 arithmetic
// + locals slice end-to-end. Each table case keeps `OSTY_ONB_STRICT=1` set so
// the test fails loudly if ONB's MIR coverage regresses and silently falls
// back to LLVM.
func TestONBBackendBinaryRunsArithOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB arith executable smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "two_locals_add",
			src: `fn main() {
    let x = 10
    let y = 32
    println(x + y)
}`,
			want: "42\n",
		},
		{
			name: "mut_add",
			src: `fn main() {
    let mut n = 1
    n = n + 5
    println(n)
}`,
			want: "6\n",
		},
		{
			name: "expr_chain",
			src: `fn main() {
    let a = 10
    let b = 3
    println(a - b * 2)
}`,
			want: "4\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(onb.EnvStrict, "1")
			req := newBackendRequest(t, EmitBinary, tc.src)
			req.Layout.Target = "aarch64-apple-darwin"
			result, err := ONBBackend{}.Emit(context.Background(), req)
			if err != nil {
				t.Fatalf("ONBBackend.Emit returned error: %v", err)
			}
			if result == nil || result.Artifacts.Binary == "" {
				t.Fatalf("missing binary artifact: %+v", result)
			}
			out, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
			if err != nil {
				t.Fatalf("binary returned error: %v\n%s", err, out)
			}
			if string(out) != tc.want {
				t.Fatalf("binary output = %q, want %q", out, tc.want)
			}
		})
	}
}

// TestONBBackendLldbFrameVariableShowsLocalsOnDarwinARM64 covers Phase
// B.3: the DWARF emitter publishes one DW_TAG_variable DIE per named
// Int local with a DW_OP_fbreg location, and lldb's `frame variable`
// recovers the value from the stack slot the lowerer wrote it to.
//
// The test stops at the first source line of a helper function so the
// param shuffle has run, then asks lldb for the parameter values. A
// regression that misaligns frame_base or skips the variable DIEs
// surfaces as wrong values rather than missing variables, so the assert
// checks both presence AND content.
func TestONBBackendLldbFrameVariableShowsLocalsOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("lldb DWARF integration smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}
	if _, err := exec.LookPath("lldb"); err != nil {
		t.Skip("lldb not found on PATH")
	}
	if _, err := exec.LookPath("dsymutil"); err != nil {
		t.Skip("dsymutil not found on PATH")
	}

	t.Setenv(onb.EnvStrict, "1")
	src := `fn add(a: Int, b: Int) -> Int { a + b }
fn main() {
    let x = 10
    let y = 32
    println(add(x, y))
}`
	req := newBackendRequest(t, EmitBinary, src)
	req.Layout.Target = "aarch64-apple-darwin"
	result, err := ONBBackend{}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit returned error: %v", err)
	}
	if out, err := exec.Command("dsymutil", result.Artifacts.Binary).CombinedOutput(); err != nil {
		t.Fatalf("dsymutil failed: %v\n%s", err, out)
	}
	out, err := exec.Command("lldb",
		"-o", "br set -f main.osty -l 1",
		"-o", "run",
		"-o", "frame variable",
		"-o", "exit",
		"--", result.Artifacts.Binary,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("lldb returned error: %v\n%s", err, out)
	}
	text := string(out)
	for _, want := range []string{"a = 10", "b = 32"} {
		if !strings.Contains(text, want) {
			t.Fatalf("lldb frame variable missing %q:\n%s", want, text)
		}
	}
}

// TestONBBackendLldbResolvesSourceLineOnDarwinARM64 verifies the full
// debugger integration: dsymutil bundles the .dSYM, lldb auto-loads it,
// and `breakpoint set --file <src> --line N` resolves to a concrete PC
// inside the function. This covers Phase B.0 (line program) + B.1
// (CU DIE / abbrev / str) + B.2 (DWARF relocs + subprogram DIEs).
func TestONBBackendLldbResolvesSourceLineOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("lldb DWARF integration smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}
	if _, err := exec.LookPath("lldb"); err != nil {
		t.Skip("lldb not found on PATH")
	}
	if _, err := exec.LookPath("dsymutil"); err != nil {
		t.Skip("dsymutil not found on PATH")
	}

	t.Setenv(onb.EnvStrict, "1")
	src := `fn main() {
    let mut i = 0
    while i < 3 {
        println(i)
        i = i + 1
    }
}`
	req := newBackendRequest(t, EmitBinary, src)
	req.Layout.Target = "aarch64-apple-darwin"
	result, err := ONBBackend{}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit returned error: %v", err)
	}
	if result == nil || result.Artifacts.Binary == "" {
		t.Fatalf("missing binary artifact: %+v", result)
	}
	if out, err := exec.Command("dsymutil", result.Artifacts.Binary).CombinedOutput(); err != nil {
		t.Fatalf("dsymutil failed: %v\n%s", err, out)
	}
	out, err := exec.Command("lldb",
		"-o", "br set -f main.osty -l 4",
		"-o", "exit",
		"--", result.Artifacts.Binary,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("lldb returned error: %v\n%s", err, out)
	}
	text := string(out)
	if strings.Contains(text, "Unable to resolve breakpoint") {
		t.Fatalf("lldb failed to resolve source breakpoint:\n%s", text)
	}
	if !strings.Contains(text, "main.osty:4") {
		t.Fatalf("lldb output missing 'main.osty:4' source location:\n%s", text)
	}
}

// TestONBBackendBinaryRunsStringAndListOnDarwinARM64 exercises Phase A2's
// runtime-call slice: String concatenation and `List<Int>` push/len. These
// shapes lower to `bl _osty_rt_strings_Concat`, `bl _osty_rt_list_new`,
// `bl _osty_rt_list_push_i64`, `bl _osty_rt_list_len`, all of which need
// the bundled runtime object on the linker command line.
func TestONBBackendBinaryRunsStringAndListOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB string/list executable smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "string_concat_with_local",
			src: `fn main() {
    let name = "world"
    println("hello, " + name)
}`,
			want: "hello, world\n",
		},
		{
			name: "string_concat_via_helper",
			src: `fn greet(name: String) -> String {
    "hello, " + name
}
fn main() {
    println(greet("alice"))
}`,
			want: "hello, alice\n",
		},
		{
			name: "list_push_len",
			src: `fn main() {
    let mut v: List<Int> = []
    v.push(10)
    v.push(20)
    v.push(30)
    println(v.len())
}`,
			want: "3\n",
		},
		{
			name: "list_built_in_loop",
			src: `fn main() {
    let mut v: List<Int> = []
    for i in 0..5 {
        v.push(i)
    }
    println(v.len())
}`,
			want: "5\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(onb.EnvStrict, "1")
			req := newBackendRequest(t, EmitBinary, tc.src)
			req.Layout.Target = "aarch64-apple-darwin"
			result, err := ONBBackend{}.Emit(context.Background(), req)
			if err != nil {
				t.Fatalf("ONBBackend.Emit returned error: %v", err)
			}
			if result == nil || result.Artifacts.Binary == "" {
				t.Fatalf("missing binary artifact: %+v", result)
			}
			out, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
			if err != nil {
				t.Fatalf("binary returned error: %v\n%s", err, out)
			}
			if string(out) != tc.want {
				t.Fatalf("binary output = %q, want %q", out, tc.want)
			}
		})
	}
}

// TestONBBackendBinaryRunsLoopsAndMatchOnDarwinARM64 exercises the Phase A2
// Week 4 control-flow expansion: `while`, `for ... in 0..N`, nested loops,
// and `match` (incl. SwitchIntTerm-shaped lowering on non-const scrutinees).
// Most of these patterns already worked once Week 3 multi-block + branch
// fixups landed; the new SwitchIntTerm lowering is what unlocks the last
// `match` shape via b.cond.
func TestONBBackendBinaryRunsLoopsAndMatchOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB loop/match executable smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "while_count_up",
			src: `fn main() {
    let mut i = 0
    while i < 3 {
        println(i)
        i = i + 1
    }
}`,
			want: "0\n1\n2\n",
		},
		{
			name: "for_range",
			src: `fn main() {
    for i in 0..3 {
        println(i)
    }
}`,
			want: "0\n1\n2\n",
		},
		{
			name: "nested_for",
			src: `fn main() {
    for i in 0..2 {
        for j in 0..2 {
            println(i * 10 + j)
        }
    }
}`,
			want: "0\n1\n10\n11\n",
		},
		{
			name: "while_calls_helper",
			src: `fn double(n: Int) -> Int { n * 2 }
fn main() {
    let mut i = 1
    while i <= 4 {
        println(double(i))
        i = i + 1
    }
}`,
			want: "2\n4\n6\n8\n",
		},
		{
			name: "match_via_fn",
			src: `fn label(n: Int) -> Int {
    match n {
        1 -> 10,
        2 -> 20,
        3 -> 30,
        _ -> 0,
    }
}
fn main() {
    println(label(1))
    println(label(2))
    println(label(3))
    println(label(4))
}`,
			want: "10\n20\n30\n0\n",
		},
		{
			name: "sum_to_n",
			src: `fn sumTo(n: Int) -> Int {
    let mut sum = 0
    let mut i = 1
    while i <= n {
        sum = sum + i
        i = i + 1
    }
    sum
}
fn main() {
    println(sumTo(5))
    println(sumTo(10))
}`,
			want: "15\n55\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(onb.EnvStrict, "1")
			req := newBackendRequest(t, EmitBinary, tc.src)
			req.Layout.Target = "aarch64-apple-darwin"
			result, err := ONBBackend{}.Emit(context.Background(), req)
			if err != nil {
				t.Fatalf("ONBBackend.Emit returned error: %v", err)
			}
			if result == nil || result.Artifacts.Binary == "" {
				t.Fatalf("missing binary artifact: %+v", result)
			}
			out, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
			if err != nil {
				t.Fatalf("binary returned error: %v\n%s", err, out)
			}
			if string(out) != tc.want {
				t.Fatalf("binary output = %q, want %q", out, tc.want)
			}
		})
	}
}

// TestONBBackendBinaryRunsIfElseOnDarwinARM64 exercises the Phase A2 Week 3
// control-flow slice end-to-end. The MIR builder shapes `if cond { ... } else
// { ... }` into 4 basic blocks (entry → cmp+branch, then, else, merge) plus
// `cmp` / `cset` / `cbnz` / `b` opcodes, and the encoder must lay them out
// with the right PC-relative offsets — wrong fixup math shows up as a wrong
// branch target / segfault, not a compile-time error.
func TestONBBackendBinaryRunsIfElseOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB if/else executable smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "gt_taken",
			src: `fn main() {
    let x = 5
    if x > 0 {
        println(1)
    } else {
        println(0)
    }
}`,
			want: "1\n",
		},
		{
			name: "gt_not_taken",
			src: `fn main() {
    let x = -3
    if x > 0 {
        println(1)
    } else {
        println(0)
    }
}`,
			want: "0\n",
		},
		{
			name: "eq_taken",
			src: `fn main() {
    let x = 7
    if x == 7 {
        println(100)
    } else {
        println(99)
    }
}`,
			want: "100\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(onb.EnvStrict, "1")
			req := newBackendRequest(t, EmitBinary, tc.src)
			req.Layout.Target = "aarch64-apple-darwin"
			result, err := ONBBackend{}.Emit(context.Background(), req)
			if err != nil {
				t.Fatalf("ONBBackend.Emit returned error: %v", err)
			}
			if result == nil || result.Artifacts.Binary == "" {
				t.Fatalf("missing binary artifact: %+v", result)
			}
			out, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
			if err != nil {
				t.Fatalf("binary returned error: %v\n%s", err, out)
			}
			if string(out) != tc.want {
				t.Fatalf("binary output = %q, want %q", out, tc.want)
			}
		})
	}
}

// TestONBBackendBinaryRunsUserFunctionCallOnDarwinARM64 exercises the Phase
// A2 Week 2 user-fn slice end-to-end: a helper function with two Int
// parameters and Int return that main calls and prints. OSTY_ONB_STRICT=1
// guards against silent fallback regression.
func TestONBBackendBinaryRunsUserFunctionCallOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB user-fn executable smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "add_call",
			src: `fn add(a: Int, b: Int) -> Int { a + b }
fn main() { println(add(40, 2)) }`,
			want: "42\n",
		},
		{
			name: "double_call",
			src: `fn double(n: Int) -> Int { n * 2 }
fn main() { println(double(21)) }`,
			want: "42\n",
		},
		{
			name: "sub_call",
			src: `fn sub(a: Int, b: Int) -> Int { a - b }
fn main() { println(sub(50, 8)) }`,
			want: "42\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(onb.EnvStrict, "1")
			req := newBackendRequest(t, EmitBinary, tc.src)
			req.Layout.Target = "aarch64-apple-darwin"
			result, err := ONBBackend{}.Emit(context.Background(), req)
			if err != nil {
				t.Fatalf("ONBBackend.Emit returned error: %v", err)
			}
			if result == nil || result.Artifacts.Binary == "" {
				t.Fatalf("missing binary artifact: %+v", result)
			}
			out, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
			if err != nil {
				t.Fatalf("binary returned error: %v\n%s", err, out)
			}
			if string(out) != tc.want {
				t.Fatalf("binary output = %q, want %q", out, tc.want)
			}
		})
	}
}

// recordingBackend captures Emit calls so we can assert that ONB delegated to
// LLVM exactly when expected, without dragging the real LLVM lowering pipeline
// into these unit tests.
type recordingBackend struct {
	name   Name
	calls  int
	last   Request
	result *Result
	err    error
}

func (r *recordingBackend) Name() Name { return r.name }
func (r *recordingBackend) Emit(_ context.Context, req Request) (*Result, error) {
	r.calls++
	r.last = req
	if r.result != nil {
		copy := *r.result
		copy.Emit = req.Emit
		return &copy, r.err
	}
	return nil, r.err
}

// onbDevTestEnv pins OSTY_ONB_* to known values for one test. t.Setenv handles
// snapshot/restore via t.Cleanup; the previous hand-rolled save/restore here
// existed because we anticipated t.Parallel, but none of the dev-loop tests
// run in parallel (the backend lifecycle is serialised by Go's test runner
// for env var safety anyway).
func onbDevTestEnv(t *testing.T, strict bool, timing bool) {
	t.Helper()
	if strict {
		t.Setenv(onb.EnvStrict, "1")
	} else {
		t.Setenv(onb.EnvStrict, "")
	}
	if timing {
		t.Setenv(onb.EnvTiming, "1")
	} else {
		t.Setenv(onb.EnvTiming, "")
	}
}

func TestONBBackendFallsBackToLLVMOnUnsupportedShape(t *testing.T) {
	onbDevTestEnv(t, false, false)

	req := newBackendRequest(t, EmitObject, `fn main() {
    let mut m: Map<String, Int> = {:}
    m.insert("a", 1)
    println(m.len())
}`)
	req.Layout.Target = "aarch64-apple-darwin"
	fallbackArtifacts := req.Artifacts(NameLLVM)
	fake := &recordingBackend{
		name: NameLLVM,
		result: &Result{
			Backend:   NameLLVM,
			Artifacts: fallbackArtifacts,
		},
	}
	result, err := ONBBackend{llvmFallback: fake}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit fallback returned error: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("llvm fallback call count = %d, want 1", fake.calls)
	}
	if fake.last.Emit != EmitObject {
		t.Fatalf("llvm fallback emit = %q, want object", fake.last.Emit)
	}
	if result == nil {
		t.Fatal("fallback result is nil")
	}
	if result.Backend != NameLLVM {
		t.Fatalf("fallback Result.Backend = %q, want %q (be honest about what built it)", result.Backend, NameLLVM)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("fallback result should attach an explanatory warning")
	}
	first := result.Warnings[0]
	if first == nil || !strings.Contains(first.Error(), "onb fallback") {
		t.Fatalf("fallback warning = %v, want onb fallback marker", first)
	}
}

func TestONBBackendStrictModeSurfacesShapeError(t *testing.T) {
	onbDevTestEnv(t, true, false)

	req := newBackendRequest(t, EmitObject, `fn main() {
    let mut m: Map<String, Int> = {:}
    m.insert("a", 1)
    println(m.len())
}`)
	req.Layout.Target = "aarch64-apple-darwin"
	fake := &recordingBackend{name: NameLLVM}
	_, err := ONBBackend{llvmFallback: fake}.Emit(context.Background(), req)
	if err == nil {
		t.Fatal("strict mode should surface ONB rejection error")
	}
	if !errors.Is(err, onb.ErrUnsupportedShape) {
		t.Fatalf("strict-mode error = %v, want errors.Is ErrUnsupportedShape", err)
	}
	if fake.calls != 0 {
		t.Fatalf("strict mode delegated to llvm %d times; want 0", fake.calls)
	}
}

func TestONBBackendASMEmitDoesNotFallback(t *testing.T) {
	onbDevTestEnv(t, false, false)

	req := newBackendRequest(t, EmitASM, `fn main() {
    let mut m: Map<String, Int> = {:}
    m.insert("a", 1)
    println(m.len())
}`)
	req.Layout.Target = "aarch64-apple-darwin"
	fake := &recordingBackend{name: NameLLVM}
	_, err := ONBBackend{llvmFallback: fake}.Emit(context.Background(), req)
	if err == nil {
		t.Fatal("ASM emit should hard-fail on shape rejection — user explicitly asked for ONB asm")
	}
	if !errors.Is(err, onb.ErrUnsupportedShape) {
		t.Fatalf("asm-mode error = %v, want errors.Is ErrUnsupportedShape", err)
	}
	if fake.calls != 0 {
		t.Fatalf("asm-mode delegated to llvm %d times; want 0", fake.calls)
	}
}

func TestONBBackendTimingLogged(t *testing.T) {
	onbDevTestEnv(t, false, true)

	req := newBackendRequest(t, EmitASM, `fn main() {}`)
	req.Layout.Target = "aarch64-apple-darwin"
	var buf bytes.Buffer
	_, err := ONBBackend{logSink: &buf}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit returned error: %v", err)
	}
	got := buf.String()
	if !strings.HasPrefix(got, "onb: emit ") {
		t.Fatalf("timing log = %q, want onb: emit prefix", got)
	}
	if !strings.Contains(got, "[native: aarch64-apple-darwin]") {
		t.Fatalf("timing log = %q, want native target tag", got)
	}
}

func TestONBBackendTimingLogsFallback(t *testing.T) {
	onbDevTestEnv(t, false, true)

	req := newBackendRequest(t, EmitObject, `fn main() {
    let mut m: Map<String, Int> = {:}
    m.insert("a", 1)
    println(m.len())
}`)
	req.Layout.Target = "aarch64-apple-darwin"
	fake := &recordingBackend{
		name: NameLLVM,
		result: &Result{
			Backend:   NameLLVM,
			Artifacts: req.Artifacts(NameLLVM),
		},
	}
	var buf bytes.Buffer
	_, err := ONBBackend{llvmFallback: fake, logSink: &buf}.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("ONBBackend.Emit fallback returned error: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "fallback to llvm") {
		t.Fatalf("timing log = %q, want fallback marker", got)
	}
	if !strings.Contains(got, "rvalue") && !strings.Contains(got, "instruction") && !strings.Contains(got, "outside phase") {
		t.Fatalf("timing log = %q, want quoted shape reason", got)
	}
}
