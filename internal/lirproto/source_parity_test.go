package lirproto

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	ostyir "github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/llvmgen"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

func TestLowerMIRSourceParityWithCurrentMIRGenerator(t *testing.T) {
	tests := []struct {
		name   string
		source string
		common []string
	}{
		{
			name: "scalar_arithmetic",
			source: `fn answer(n: Int) -> Int {
    n + 1
}
`,
			common: []string{
				"define i64 @answer(i64",
				"add i64",
				"store i64",
				"ret i64",
			},
		},
		{
			name: "scalar_branch",
			source: `fn choose(ok: Bool) -> Int {
    if ok {
        return 1
    }
    return 2
}
`,
			common: []string{
				"define i64 @choose(i1",
				"br i1",
				"store i64 1",
				"store i64 2",
				"ret i64",
			},
		},
		{
			name: "primitive_byte_to_char",
			source: `fn byteAsChar(b: Byte) -> Char {
    b.toChar()
}
`,
			common: []string{
				"define i32 @byteAsChar(i8",
				"zext i8",
				"to i32",
				"store i32",
				"ret i32",
			},
		},
		{
			name: "direct_call",
			source: `fn inc(n: Int) -> Int {
    n + 1
}

fn useInc() -> Int {
    inc(41)
}
`,
			common: []string{
				"define i64 @inc(i64",
				"define i64 @useInc()",
				"call i64 @inc(i64 41)",
				"store i64",
				"ret i64",
			},
		},
		{
			name: "tuple_literal_read",
			source: `fn pack() -> Int {
    let t = (1, 2)
    t.0 + t.1
}
`,
			common: []string{
				"%Tuple.i64.i64 = type { i64, i64 }",
				"define i64 @pack()",
				"insertvalue %Tuple.i64.i64 undef, i64 1, 0",
				"insertvalue %Tuple.i64.i64",
				"extractvalue %Tuple.i64.i64",
				"add i64",
				"ret i64",
			},
		},
		{
			name: "struct_literal_read",
			source: `struct Point {
    x: Int,
    y: Int,
}

fn sum() -> Int {
    let p = Point { x: 1, y: 2 }
    p.x + p.y
}
`,
			common: []string{
				"%Point = type { i64, i64 }",
				"define i64 @sum()",
				"insertvalue %Point undef, i64 1, 0",
				"insertvalue %Point",
				"extractvalue %Point",
				"add i64",
				"ret i64",
			},
		},
		{
			name: "nested_struct_projected_assign",
			source: `struct Inner {
    value: Int,
}

struct Outer {
    inner: Inner,
    flag: Int,
}

fn update() -> Int {
    let mut o = Outer { inner: Inner { value: 0 }, flag: 1 }
    o.inner.value = 42
    o.inner.value + o.flag
}
`,
			common: []string{
				"%Inner = type { i64 }",
				"%Outer = type { %Inner, i64 }",
				"define i64 @update()",
				"extractvalue %Outer",
				"insertvalue %Inner",
				"insertvalue %Outer",
				"store %Outer",
				"add i64",
				"ret i64",
			},
		},
		{
			name: "projected_call_result",
			source: `struct Cell {
    value: Int,
    other: Int,
}

fn makeValue() -> Int {
    7
}

fn fill() -> Int {
    let mut cell = Cell { value: 0, other: 1 }
    cell.value = makeValue()
    cell.value + cell.other
}
`,
			common: []string{
				"%Cell = type { i64, i64 }",
				"define i64 @makeValue()",
				"define i64 @fill()",
				"call i64 @makeValue()",
				"load %Cell",
				"insertvalue %Cell",
				"store %Cell",
				"extractvalue %Cell",
				"add i64",
				"ret i64",
			},
		},
		{
			name: "projected_intrinsic_result",
			source: `struct Cell {
    value: Int,
    other: Int,
}

fn fillIntrinsic(b: Byte) -> Int {
    let mut cell = Cell { value: 0, other: 1 }
    cell.value = b.toInt()
    cell.value + cell.other
}
`,
			common: []string{
				"%Cell = type { i64, i64 }",
				"define i64 @fillIntrinsic(i8",
				"zext i8",
				"to i64",
				"load %Cell",
				"insertvalue %Cell",
				"store %Cell",
				"extractvalue %Cell",
				"add i64",
				"ret i64",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sourcePath := "/tmp/lir_proto_source_" + tc.name + ".osty"
			lirOut := renderSourceLIR(t, tc.source, sourcePath)
			mirOut := renderSourceCurrentMIRGenerator(t, tc.source, sourcePath)
			for _, want := range tc.common {
				assertSourceContains(t, "LIR Proto", lirOut, want)
				assertSourceContains(t, "current MIR generator", mirOut, want)
			}
		})
	}
}

func TestLowerMIRSourcePrintIntrinsicUsesRuntimeIOABI(t *testing.T) {
	src := `fn say() {
    println(7)
}
`
	sourcePath := "/tmp/lir_proto_source_println_int.osty"
	mod := lowerSourceToMIR(t, src)
	assertSourceMIRHasIntrinsic(t, mod, "say", mir.IntrinsicPrintln)

	res := NewLowerer(Config{
		PackageName: "main",
		SourcePath:  sourcePath,
	}).LowerMIR(mod)
	if !res.OK() {
		t.Fatalf("LIR Proto diagnostics = %+v, want OK", res.Diagnostics)
	}
	got := Render(res.Module)
	for _, want := range []string{
		"define void @say()",
		"declare ptr @osty_rt_int_to_string(i64)",
		"declare void @osty_rt_io_write(ptr, i1, i1)",
		"call ptr @osty_rt_int_to_string(i64 7)",
		"call void @osty_rt_io_write(",
		"i1 true, i1 false",
	} {
		assertSourceContains(t, "LIR Proto", got, want)
	}
	if strings.Contains(got, "@printf") {
		t.Fatalf("LIR Proto source print unexpectedly used printf:\n%s", got)
	}
}

func renderSourceLIR(t *testing.T, src string, sourcePath string) string {
	t.Helper()
	mod := lowerSourceToMIR(t, src)
	res := NewLowerer(Config{
		PackageName: "main",
		SourcePath:  sourcePath,
	}).LowerMIR(mod)
	if !res.OK() {
		t.Fatalf("LIR Proto diagnostics = %+v, want OK", res.Diagnostics)
	}
	return Render(res.Module)
}

func renderSourceCurrentMIRGenerator(t *testing.T, src string, sourcePath string) string {
	t.Helper()
	mod := lowerSourceToMIR(t, src)
	out, err := llvmgen.GenerateFromMIR(mod, llvmgen.Options{
		PackageName: "main",
		SourcePath:  sourcePath,
	})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	return string(out)
}

func lowerSourceToMIR(t *testing.T, src string) *mir.Module {
	t.Helper()
	source := []byte(src)
	file, parseDiags := parser.ParseDiagnostics(source)
	if len(parseDiags) != 0 {
		t.Fatalf("parse diagnostics = %v", parseDiags)
	}
	if file == nil {
		t.Fatal("parser returned nil file")
	}
	reg := stdlib.LoadCached()
	res := resolve.ResolveFileSourceDefault(source, file, reg)
	if len(res.Diags) != 0 {
		t.Fatalf("resolve diagnostics = %v", res.Diags)
	}
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        source,
	})
	if len(chk.Diags) != 0 {
		t.Fatalf("check diagnostics = %v", chk.Diags)
	}
	hirMod, issues := ostyir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower issues = %v", issues)
	}
	monoMod, monoErrs := ostyir.Monomorphize(hirMod)
	if len(monoErrs) != 0 {
		t.Fatalf("ir.Monomorphize errors = %v", monoErrs)
	}
	if errs := ostyir.Validate(monoMod); len(errs) != 0 {
		t.Fatalf("ir.Validate errors = %v", errs)
	}
	mirMod := mir.Lower(monoMod)
	if mirMod == nil {
		t.Fatal("mir.Lower returned nil")
	}
	if errs := mir.Validate(mirMod); len(errs) != 0 {
		t.Fatalf("mir.Validate errors = %v", errs)
	}
	return mirMod
}

func assertSourceMIRHasIntrinsic(t *testing.T, mod *mir.Module, fnName string, kind mir.IntrinsicKind) {
	t.Helper()
	if mod == nil {
		t.Fatal("nil MIR module")
	}
	for _, fn := range mod.Functions {
		if fn == nil || fn.Name != fnName {
			continue
		}
		for _, bb := range fn.Blocks {
			if bb == nil {
				continue
			}
			for _, instr := range bb.Instrs {
				intr, ok := instr.(*mir.IntrinsicInstr)
				if ok && intr.Kind == kind {
					return
				}
			}
		}
		t.Fatalf("MIR function %q has no %s intrinsic", fnName, kind)
	}
	t.Fatalf("MIR module has no function %q", fnName)
}

func assertSourceContains(t *testing.T, label, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("%s output missing %q:\n%s", label, want, got)
	}
}
