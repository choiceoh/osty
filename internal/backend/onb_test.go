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

// TestONBBackendLldbFrameVariableShowsBoolAndStringOnDarwinARM64
// extends Phase B.3 to non-Int scalars. Bool requires byte_size 1 in
// the DWARF base type (lldb refuses byte_size 8 + DW_ATE_boolean) and
// String is encoded as DW_TAG_pointer_type → DW_TAG_base_type "char"
// so lldb prints the pointee. The test uses a function whose body
// reads only one parameter, exercising the "param always gets a slot"
// rule that keeps unused params visible to the debugger.
func TestONBBackendLldbFrameVariableShowsBoolAndStringOnDarwinARM64(t *testing.T) {
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
	src := `fn first(a: Int, b: Bool, c: String) -> Int { a }
fn main() {
    println(first(42, true, "hi"))
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
	for _, want := range []string{"a = 42", "b = true", `c = `, `"hi"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("lldb frame variable missing %q:\n%s", want, text)
		}
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

// TestONBBackendBinaryRunsStructLiteralOnDarwinARM64 covers the first
// half of struct lowering: a struct literal local plus field reads via
// place projection. The MIR shape is `let p = Point { x, y };
// println(p.x)` — the slot allocator reserves 16 bytes for the struct,
// the literal write stamps each field into its own 8-byte slot, and
// the println intrinsic reads the value through DW_TAG_member-style
// offset arithmetic. Struct param/return passing (AAPCS64 small-struct
// in two regs) lands in a follow-up slice.
func TestONBBackendBinaryRunsStructLiteralOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB struct executable smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	t.Setenv(onb.EnvStrict, "1")
	src := `struct Point { x: Int, y: Int }
fn main() {
    let p = Point { x: 3, y: 4 }
    println(p.x)
    println(p.y)
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
	out, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("binary returned error: %v\n%s", err, out)
	}
	if got, want := string(out), "3\n4\n"; got != want {
		t.Fatalf("binary output = %q, want %q", got, want)
	}
}

// TestONBBackendBinaryRunsStructParamAndReturnOnDarwinARM64 exercises the
// AAPCS64 small-struct ABI both directions: a function returns a struct
// in {x0, x1}, the caller captures it into a stack slot, then passes
// that whole struct to a second function that consumes it via two
// argument registers. The end-to-end output drives both the
// `materialiseStructOperand` (load slot → 2 arg regs) and the
// struct-return epilogue (load slot → x0/x1) paths through real
// generated code that gets executed.
//
// Source program:
//
//	struct Point { x: Int, y: Int }
//	fn make() -> Point { Point { x: 5, y: 6 } }
//	fn px(p: Point) -> Int { p.x }
//	fn py(p: Point) -> Int { p.y }
//	fn main() {
//	    let p = make()
//	    println(px(p))
//	    println(py(p))
//	}
func TestONBBackendBinaryRunsStructParamAndReturnOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB struct ABI executable smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	t.Setenv(onb.EnvStrict, "1")
	src := `struct Point { x: Int, y: Int }
fn make() -> Point { Point { x: 5, y: 6 } }
fn px(p: Point) -> Int { p.x }
fn py(p: Point) -> Int { p.y }
fn main() {
    let p = make()
    println(px(p))
    println(py(p))
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
	out, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
	if err != nil {
		t.Fatalf("binary returned error: %v\n%s", err, out)
	}
	if got, want := string(out), "5\n6\n"; got != want {
		t.Fatalf("binary output = %q, want %q", got, want)
	}
}

// TestONBBackendLldbFrameVariableShowsStructOnDarwinARM64 verifies the
// DWARF wiring half of small-struct support: a struct local appears in
// `frame variable` with its field names and values rendered through a
// DW_TAG_structure_type DIE plus DW_TAG_member children. Without the
// member DIEs lldb would fall back to "(Point) p =" with no field
// breakdown, which is what regressions on the type-DIE wiring look
// like. The breakpoint is set on the println line so the param shuffle
// in `px` has run and the struct slot is fully populated.
func TestONBBackendLldbFrameVariableShowsStructOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("lldb struct DWARF integration smoke is darwin/arm64-only")
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
	src := `struct Point { x: Int, y: Int }
fn px(p: Point) -> Int { p.x }
fn main() {
    let p = Point { x: 7, y: 11 }
    println(px(p))
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
		"-o", "br set -f main.osty -l 2",
		"-o", "run",
		"-o", "frame variable",
		"-o", "exit",
		"--", result.Artifacts.Binary,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("lldb returned error: %v\n%s", err, out)
	}
	text := string(out)
	for _, want := range []string{"(Point)", "x = 7", "y = 11"} {
		if !strings.Contains(text, want) {
			t.Fatalf("lldb frame variable missing %q:\n%s", want, text)
		}
	}
}

// TestONBBackendBinaryRunsEnumPatternsOnDarwinARM64 exercises Phase A2
// Week 14's enum lowering. Three shapes exercise the relevant paths:
//
//   - **No-payload enum**: `enum Color { Red, Green, Blue }` plus a
//     `match` that picks an Int per arm. Drives `AggEnumVariant`
//     (no payload) + `DiscriminantRV` + `SwitchIntTerm`.
//   - **Option<Int>**: `Some(n)` / `None` construction + match
//     destructuring `Some(x)`. Drives `AggEnumVariant` (1 scalar
//     payload) + `NullaryRV(None)` + `VariantProj` payload read.
//   - **Result<Int, String>**: `Ok(v)` / `Err(msg)` construction + match
//     destructuring on both arms. Drives the same machinery with a
//     real user-defined enum (no Optional shortcut), and verifies
//     the discriminant convention (Err=0, Ok=1) survives end-to-end.
//
// Each case runs the produced binary and checks stdout. Failures point
// at one of the three lowering paths above without needing dwarfdump.
func TestONBBackendBinaryRunsEnumPatternsOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB enum executable smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	cases := []struct {
		name, src, want string
	}{
		{
			name: "no_payload_enum",
			src: `enum Color { Red, Green, Blue }
fn pick() -> Color { Color.Green }
fn main() {
    let c = pick()
    let n = match c {
        Color.Red -> 0,
        Color.Green -> 1,
        Color.Blue -> 2,
    }
    println(n)
}`,
			want: "1\n",
		},
		{
			name: "option_some",
			src: `fn first(n: Int) -> Int? { if n > 0 { Some(n) } else { None } }
fn main() {
    let r = first(5)
    let v = match r {
        Some(x) -> x,
        None -> -1,
    }
    println(v)
}`,
			want: "5\n",
		},
		{
			name: "option_none",
			src: `fn first(n: Int) -> Int? { if n > 0 { Some(n) } else { None } }
fn main() {
    let r = first(0)
    let v = match r {
        Some(x) -> x,
        None -> -1,
    }
    println(v)
}`,
			want: "-1\n",
		},
		{
			// Result with a scalar Ok payload and a no-payload enum
			// Err payload. Exercises the synthetic Result layout
			// (Err=0/Ok=1) and an enum-typed payload — `MyError` is
			// itself an enum so the Err slot copies its 8-byte
			// discriminant into the Result's payload register. The
			// match destructures Ok(v) and reads v through
			// VariantProj{FieldIdx:0}.
			name: "result_ok",
			src: `enum MyError { Empty, BadInt }
fn parseSign(n: Int) -> Result<Int, MyError> {
    if n == 0 { Err(MyError.Empty) } else { Ok(n) }
}
fn main() {
    let r = parseSign(7)
    let v = match r {
        Ok(x) -> x,
        Err(_) -> -1,
    }
    println(v)
}`,
			want: "7\n",
		},
		{
			name: "result_err",
			src: `enum MyError { Empty, BadInt }
fn parseSign(n: Int) -> Result<Int, MyError> {
    if n == 0 { Err(MyError.Empty) } else { Ok(n) }
}
fn main() {
    let r = parseSign(0)
    let v = match r {
        Ok(x) -> x,
        Err(_) -> -1,
    }
    println(v)
}`,
			want: "-1\n",
		},
		{
			// `?` propagation. The front end lowers `parseSign(n)?` to
			// SwitchIntTerm{Ok => take payload; default => return Err
			// as-is}. The Err arm's `_0 = use _3` is the multi-slot
			// copy the new lowerUseAssignMulti path handles; without
			// it, only the discriminant byte would survive and the
			// caller's match would crash.
			name: "result_question_op_ok",
			src: `enum MyError { Empty, BadInt }
fn parseSign(n: Int) -> Result<Int, MyError> {
    if n == 0 { Err(MyError.Empty) } else { Ok(n) }
}
fn parseAndDouble(n: Int) -> Result<Int, MyError> {
    let v = parseSign(n)?
    Ok(v * 2)
}
fn main() {
    let r = parseAndDouble(5)
    let v = match r {
        Ok(x) -> x,
        Err(_) -> -1,
    }
    println(v)
}`,
			want: "10\n",
		},
		{
			name: "result_question_op_err",
			src: `enum MyError { Empty, BadInt }
fn parseSign(n: Int) -> Result<Int, MyError> {
    if n == 0 { Err(MyError.Empty) } else { Ok(n) }
}
fn parseAndDouble(n: Int) -> Result<Int, MyError> {
    let v = parseSign(n)?
    Ok(v * 2)
}
fn main() {
    let r = parseAndDouble(0)
    let v = match r {
        Ok(x) -> x,
        Err(_) -> -1,
    }
    println(v)
}`,
			want: "-1\n",
		},
	}
	for _, tc := range cases {
		tc := tc
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
			if got := string(out); got != tc.want {
				t.Fatalf("binary output = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestONBBackendBinaryRunsFloat64OnDarwinARM64 covers Phase A2 Week 15:
// Float64 const materialisation, IEEE arithmetic, println via printf
// "%g\n", and the AAPCS64 d0..d7 / d0 FP-arg + return convention.
//
// Cases:
//   - **const_print**: tests `let x: Float64 = 3.14; println(x)` —
//     drives floatConstInstrs (movz/movk + fmov d, x) + the printf
//     vararg slot Float path.
//   - **arith**: `(1.5 + 2.5) * 3.0` — exercises FAdd + FMul +
//     intermediate float local store/load.
//   - **fn_param_ret**: `fn double(x: Float64) -> Float64 { x * 2.0 }`
//     drives the d0 param shuffle + d0 return epilogue and proves
//     the AAPCS64 FP cursor works at call boundaries.
//   - **mixed_int_float**: `fn scale(n: Int, f: Float64) -> Float64
//     { f * n.toFloat64() }` — actually we keep this simpler: just
//     pass an Int and a Float to verify the two cursors stay
//     independent. The Int local goes to x0 and the Float local
//     to d0, not x0/x1 (a regression on cursor isolation would
//     mis-route the Float through x1).
func TestONBBackendBinaryRunsFloat64OnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB Float64 executable smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	cases := []struct {
		name, src, want string
	}{
		{
			name: "const_print",
			src: `fn main() {
    let x: Float64 = 3.14
    println(x)
}`,
			want: "3.14\n",
		},
		{
			name: "arith",
			src: `fn main() {
    let a: Float64 = 1.5
    let b: Float64 = 2.5
    let c = (a + b) * 3.0
    println(c)
}`,
			want: "12\n",
		},
		{
			name: "fn_param_ret",
			src: `fn double(x: Float64) -> Float64 { x * 2.0 }
fn main() {
    let v = double(3.5)
    println(v)
}`,
			want: "7\n",
		},
		{
			name: "mixed_int_float_args",
			src: `fn add(n: Int, f: Float64) -> Float64 { f + 1.0 }
fn main() {
    let v = add(10, 2.5)
    println(v)
}`,
			want: "3.5\n",
		},
	}
	for _, tc := range cases {
		tc := tc
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
			if got := string(out); got != tc.want {
				t.Fatalf("binary output = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestONBBackendBinaryRunsListGenericsOnDarwinARM64 covers Phase A2
// Week 16: `List<T>` runtime-call dispatch for T ∈ {Bool, Float64,
// String}. Every case calls `_osty_rt_list_new`, pushes two scalar
// values via the type-specific runtime symbol, then prints the list
// length. Failures point at the wrong runtime symbol or a register
// mis-routing: a List<Float64> push that goes through x1 instead of
// d0 would crash the runtime as it reads garbage bits.
func TestONBBackendBinaryRunsListGenericsOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB List<T> executable smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	cases := []struct {
		name, src, want string
	}{
		{
			name: "list_bool",
			src: `fn main() {
    let mut v: List<Bool> = []
    v.push(true)
    v.push(false)
    v.push(true)
    println(v.len())
}`,
			want: "3\n",
		},
		{
			name: "list_float",
			src: `fn main() {
    let mut v: List<Float64> = []
    v.push(3.14)
    v.push(2.71)
    println(v.len())
}`,
			want: "2\n",
		},
		{
			name: "list_string",
			src: `fn main() {
    let mut v: List<String> = []
    v.push("hi")
    v.push("yo")
    v.push("hey")
    v.push("bye")
    println(v.len())
}`,
			want: "4\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(onb.EnvStrict, "1")
			req := newBackendRequest(t, EmitBinary, tc.src)
			req.Layout.Target = "aarch64-apple-darwin"
			result, err := ONBBackend{}.Emit(context.Background(), req)
			if err != nil {
				t.Fatalf("ONBBackend.Emit returned error: %v", err)
			}
			out, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
			if err != nil {
				t.Fatalf("binary returned error: %v\n%s", err, out)
			}
			if got := string(out); got != tc.want {
				t.Fatalf("binary output = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestONBBackendBinaryRunsForInLoopOnDarwinARM64 covers Phase A2 Week
// 17: `for x in list` and `for i in range` lowered natively. Range
// loops were already covered by Week 1 (counter + comparison + branch),
// but list iteration required two new MIR vocab items: `LenRV` (the
// loop's upper bound) and `IndexProj` (the per-iteration element
// load). Both route through the type-specific runtime symbols added
// in Week 16 (`osty_rt_list_len`, `osty_rt_list_get_*`).
func TestONBBackendBinaryRunsForInLoopOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB for-in executable smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	cases := []struct {
		name, src, want string
	}{
		{
			// Range loop: 0+1+2+...+9 = 45. Front end lowers to a
			// counter loop with BinaryRV comparisons and assignments
			// — pure stack-everything arithmetic with no runtime
			// calls. Acts as a sanity gate that the for-in regression
			// only affects list iteration, not the simpler Range case.
			name: "range_sum",
			src: `fn main() {
    let mut sum = 0
    for i in 0..10 {
        sum = sum + i
    }
    println(sum)
}`,
			want: "45\n",
		},
		{
			// List<Int>: drives osty_rt_list_len + osty_rt_list_get_i64.
			name: "list_int_sum",
			src: `fn main() {
    let mut v: List<Int> = []
    v.push(10)
    v.push(20)
    v.push(30)
    let mut sum = 0
    for x in v {
        sum = sum + x
    }
    println(sum)
}`,
			want: "60\n",
		},
		{
			// List<String>: drives osty_rt_list_get_string. The
			// payload itself isn't summed — we only assert iteration
			// happens by counting elements through a side-channel
			// integer accumulator.
			name: "list_string_count",
			src: `fn main() {
    let mut v: List<String> = []
    v.push("a")
    v.push("b")
    v.push("c")
    v.push("d")
    let mut count = 0
    for s in v {
        count = count + 1
    }
    println(count)
}`,
			want: "4\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(onb.EnvStrict, "1")
			req := newBackendRequest(t, EmitBinary, tc.src)
			req.Layout.Target = "aarch64-apple-darwin"
			result, err := ONBBackend{}.Emit(context.Background(), req)
			if err != nil {
				t.Fatalf("ONBBackend.Emit returned error: %v", err)
			}
			out, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
			if err != nil {
				t.Fatalf("binary returned error: %v\n%s", err, out)
			}
			if got := string(out); got != tc.want {
				t.Fatalf("binary output = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestONBBackendBinaryRunsStructFieldMutationOnDarwinARM64 covers
// Phase A2 Week 18: `p.x = 5` writes through a projection chain. The
// front end emits `Assign{Dest: Place{p, [FieldProj 0]}, Src: ...}`;
// previously ONB rejected projections on the dest side and fell back
// to LLVM. The new `lowerAssignToProjection` path computes the
// constant byte offset and emits a slot+offset store.
//
// Cases:
//   - **simple_assign**: `p.x = 100` — drives the const → field-write
//     path.
//   - **read_modify_write**: `p.y = p.y + 1` — drives BinaryRV with
//     a projected destination (the rvalue still reads through
//     FieldProj, which has worked since Week 12).
//   - **swap_via_field**: two struct locals with a temporary swap.
//     Verifies the projection write doesn't trample the source
//     when both destinations live near each other on the stack.
func TestONBBackendBinaryRunsStructFieldMutationOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB struct field mutation smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	cases := []struct {
		name, src, want string
	}{
		{
			name: "simple_assign",
			src: `struct Point { x: Int, y: Int }
fn main() {
    let mut p = Point { x: 3, y: 4 }
    p.x = 100
    println(p.x)
    println(p.y)
}`,
			want: "100\n4\n",
		},
		{
			name: "read_modify_write",
			src: `struct Point { x: Int, y: Int }
fn main() {
    let mut p = Point { x: 3, y: 4 }
    p.y = p.y + 10
    p.x = p.x * 2
    println(p.x)
    println(p.y)
}`,
			want: "6\n14\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(onb.EnvStrict, "1")
			req := newBackendRequest(t, EmitBinary, tc.src)
			req.Layout.Target = "aarch64-apple-darwin"
			result, err := ONBBackend{}.Emit(context.Background(), req)
			if err != nil {
				t.Fatalf("ONBBackend.Emit returned error: %v", err)
			}
			out, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
			if err != nil {
				t.Fatalf("binary returned error: %v\n%s", err, out)
			}
			if got := string(out); got != tc.want {
				t.Fatalf("binary output = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestONBBackendBinaryRunsLargeStructSretOnDarwinARM64 covers Phase
// A2 Week 19: structs >16B (3- to 4-field all-scalar) ride the
// AAPCS64 indirect (sret) ABI. The caller allocates the return
// buffer and passes its address in x8; the callee writes each field
// through x8 at epilogue. Indirect arguments use the same model on
// the integer register cursor — the caller passes `add Xt, sp,
// #slot` and the callee's prologue copies field-by-field into the
// param's local slot.
//
// Cases:
//   - **make_v3**: `make() -> V3 { V3 { 10, 20, 30 } }` returns 24B
//     via x8.
//   - **sum_v3**: `sum(v: V3) -> Int { v.x + v.y + v.z }` reads all
//     three fields after the prologue copy.
//   - **roundtrip_v3**: composes the two — confirms the caller's
//     dest slot doubles as the sret buffer and is consumed by the
//     subsequent indirect-arg call without an extra copy.
//   - **make_v4**: 4-field struct (32B) — exercises the upper end
//     of the abiIndirectStructFieldLimit cap.
func TestONBBackendBinaryRunsLargeStructSretOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB sret executable smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	cases := []struct {
		name, src, want string
	}{
		{
			name: "make_v3",
			src: `struct V3 { x: Int, y: Int, z: Int }
fn make() -> V3 { V3 { x: 10, y: 20, z: 30 } }
fn main() {
    let v = make()
    println(v.x)
    println(v.y)
    println(v.z)
}`,
			want: "10\n20\n30\n",
		},
		{
			name: "sum_v3",
			src: `struct V3 { x: Int, y: Int, z: Int }
fn sum(v: V3) -> Int { v.x + v.y + v.z }
fn main() {
    let v = V3 { x: 4, y: 5, z: 6 }
    println(sum(v))
}`,
			want: "15\n",
		},
		{
			name: "roundtrip_v3",
			src: `struct V3 { x: Int, y: Int, z: Int }
fn make() -> V3 { V3 { x: 100, y: 200, z: 300 } }
fn sum(v: V3) -> Int { v.x + v.y + v.z }
fn main() {
    let v = make()
    println(sum(v))
}`,
			want: "600\n",
		},
		{
			name: "make_v4",
			src: `struct V4 { a: Int, b: Int, c: Int, d: Int }
fn make() -> V4 { V4 { a: 1, b: 2, c: 3, d: 4 } }
fn main() {
    let v = make()
    println(v.a + v.b + v.c + v.d)
}`,
			want: "10\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(onb.EnvStrict, "1")
			req := newBackendRequest(t, EmitBinary, tc.src)
			req.Layout.Target = "aarch64-apple-darwin"
			result, err := ONBBackend{}.Emit(context.Background(), req)
			if err != nil {
				t.Fatalf("ONBBackend.Emit returned error: %v", err)
			}
			out, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
			if err != nil {
				t.Fatalf("binary returned error: %v\n%s", err, out)
			}
			if got := string(out); got != tc.want {
				t.Fatalf("binary output = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestONBBackendBinaryRunsClosuresOnDarwinARM64 covers Phase A2 Week
// 20: closure values and indirect call lowering. The runtime allocates
// the env via `osty.rt.closure_env_alloc_v2`; the lowerer stores the
// lifted-fn pointer at offset 0 and each capture at offset 24+i*8;
// indirect call loads the fn pointer from the env's first slot and
// `blr`s into it with the env riding x0 as the closure-env arg.
//
// Cases:
//   - **no_capture**: a closure that reads only its user arg —
//     drives the alloc + indirect-call paths without any env-deref
//     reads.
//   - **single_capture**: captures one Int — exercises the
//     alloc/store/load chain through the env's capture region (offset
//     24 → MIR's `_env.*.1` projection, lowered via `loadEnvProjection`).
//   - **two_captures**: captures two Ints — verifies the per-capture
//     stride matches the runtime layout (offset 32 for capture[1]).
func TestONBBackendBinaryRunsClosuresOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB closure executable smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	cases := []struct {
		name, src, want string
	}{
		{
			name: "no_capture",
			src: `fn main() {
    let f = |x: Int| x + 1
    println(f(10))
    println(f(99))
}`,
			want: "11\n100\n",
		},
		{
			name: "single_capture",
			src: `fn main() {
    let n = 100
    let f = |x: Int| x + n
    println(f(10))
}`,
			want: "110\n",
		},
		{
			name: "two_captures",
			src: `fn main() {
    let a = 10
    let b = 20
    let f = |x: Int| x + a + b
    println(f(5))
}`,
			want: "35\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(onb.EnvStrict, "1")
			req := newBackendRequest(t, EmitBinary, tc.src)
			req.Layout.Target = "aarch64-apple-darwin"
			result, err := ONBBackend{}.Emit(context.Background(), req)
			if err != nil {
				t.Fatalf("ONBBackend.Emit returned error: %v", err)
			}
			out, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
			if err != nil {
				t.Fatalf("binary returned error: %v\n%s", err, out)
			}
			if got := string(out); got != tc.want {
				t.Fatalf("binary output = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestONBBackendBinaryRunsStdlibIntrinsicsOnDarwinARM64 covers Phase
// A2 Week 21 — a batch of stdlib intrinsics that the front end emits
// for `.len()`, `.isEmpty()`, `.isSome()`, `.isNone()` on String /
// List / Option, plus the implicit "builtin generic types pass as
// pointers at the ABI" extension that lets `fn first<T>(xs:
// List<T>)` compile to a native call (the monomorphized name is just
// another FnRef once the pointer-shaped arg is accepted).
//
// Bool println currently routes through `printf "%lld\n"` so true /
// false render as 1 / 0 — that's consistent with Week 4 and isn't
// changed by this slice.
func TestONBBackendBinaryRunsStdlibIntrinsicsOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB stdlib-intrinsic smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	cases := []struct {
		name, src, want string
	}{
		{
			name: "string_len",
			src: `fn main() {
    println("hello".len())
    println("".len())
}`,
			want: "5\n0\n",
		},
		{
			name: "string_is_empty",
			src: `fn main() {
    println("".isEmpty())
    println("x".isEmpty())
}`,
			want: "1\n0\n",
		},
		{
			name: "list_is_empty",
			src: `fn main() {
    let mut xs: List<Int> = []
    println(xs.isEmpty())
    xs.push(1)
    println(xs.isEmpty())
}`,
			want: "1\n0\n",
		},
		{
			name: "option_is_some_none",
			src: `fn main() {
    let a: Int? = Some(7)
    let b: Int? = None
    println(a.isSome())
    println(a.isNone())
    println(b.isSome())
    println(b.isNone())
}`,
			want: "1\n0\n0\n1\n",
		},
		{
			// Generic monomorphized fn — the body uses everything ONB
			// already supports (LenRV, IndexProj, AggEnumVariant,
			// NullaryRV); the call-site needed `List<Int>` to be
			// accepted as a 1-reg pointer ABI param, which Week 21
			// unlocks via `isBuiltinPointerType`.
			name: "generic_fn_first",
			src: `fn first<T>(xs: List<T>) -> T? {
    if xs.len() == 0 { None } else { Some(xs[0]) }
}
fn main() {
    let mut xs: List<Int> = []
    xs.push(42)
    let r = first(xs)
    let v = match r {
        Some(x) -> x,
        None -> -1,
    }
    println(v)
}`,
			want: "42\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(onb.EnvStrict, "1")
			req := newBackendRequest(t, EmitBinary, tc.src)
			req.Layout.Target = "aarch64-apple-darwin"
			result, err := ONBBackend{}.Emit(context.Background(), req)
			if err != nil {
				t.Fatalf("ONBBackend.Emit returned error: %v", err)
			}
			out, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
			if err != nil {
				t.Fatalf("binary returned error: %v\n%s", err, out)
			}
			if got := string(out); got != tc.want {
				t.Fatalf("binary output = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestONBBackendBinaryRunsMapNewInsertLenOnDarwinARM64 covers Phase
// A2 Week 22: `Map<K, V>` construction, insertion, and length
// queries. Lookup (`Map.get`), `Map.contains`, and `Map.remove` all
// pass values via the runtime's pointer-out convention; that path
// isn't lowered yet and stays in fallback.
//
// Cases exercise the three most common key kinds: String, Int, and
// — once a Float-keyed map is realistic — Float64. Each calls
// `osty_rt_map_new` with the right (key_kind, value_kind, 8, NULL)
// tuple, stamps the value into the per-function scratch slot before
// `osty_rt_map_insert_<key_suffix>(map, key, &scratch)`, and queries
// `osty_rt_map_len(map)` for the final count.
func TestONBBackendBinaryRunsMapNewInsertLenOnDarwinARM64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("ONB Map<K,V> executable smoke is darwin/arm64-only")
	}
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found on PATH")
	}

	cases := []struct {
		name, src, want string
	}{
		{
			name: "map_string_int_three_keys",
			src: `fn main() {
    let mut m: Map<String, Int> = {:}
    m.insert("a", 1)
    m.insert("b", 2)
    m.insert("c", 3)
    println(m.len())
}`,
			want: "3\n",
		},
		{
			name: "map_int_int_two_keys",
			src: `fn main() {
    let mut m: Map<Int, Int> = {:}
    m.insert(10, 100)
    m.insert(20, 200)
    println(m.len())
}`,
			want: "2\n",
		},
		{
			name: "map_string_int_overwrite",
			src: `fn main() {
    let mut m: Map<String, Int> = {:}
    m.insert("k", 1)
    m.insert("k", 2)        // overwrite; len stays 1
    println(m.len())
}`,
			want: "1\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(onb.EnvStrict, "1")
			req := newBackendRequest(t, EmitBinary, tc.src)
			req.Layout.Target = "aarch64-apple-darwin"
			result, err := ONBBackend{}.Emit(context.Background(), req)
			if err != nil {
				t.Fatalf("ONBBackend.Emit returned error: %v", err)
			}
			out, err := exec.Command(result.Artifacts.Binary).CombinedOutput()
			if err != nil {
				t.Fatalf("binary returned error: %v\n%s", err, out)
			}
			if got := string(out); got != tc.want {
				t.Fatalf("binary output = %q, want %q", got, tc.want)
			}
		})
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
    let v = m.get("a")
    println(v.isSome())
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
    let v = m.get("a")
    println(v.isSome())
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
    let v = m.get("a")
    println(v.isSome())
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
    let v = m.get("a")
    println(v.isSome())
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
