package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/backend/stage0"
	"github.com/osty/osty/internal/llvmabi"
)

// realStage0Run compiles `src` through the full front-end (parse →
// resolve → check → IR → MIR) and feeds the resulting MIR to the
// stage0 emitter. The returned error is non-nil whenever stage0
// declines so the test can either assert acceptance or log a
// reproducible gap report for the next bootstrap phase.
func realStage0Run(t *testing.T, src string) ([]byte, error) {
	t.Helper()
	req := newBackendRequest(t, EmitLLVMIR, src)
	if req.Entry.MIR == nil {
		t.Fatal("entry.MIR was nil — front-end did not lower")
	}
	return stage0.EmitMIR(req.Entry.MIR, llvmabi.Options{PackageName: req.Entry.PackageName})
}

// dumpMIRGap is the canonical "stage0 missed a real-MIR shape" log
// helper. Each call prints the function-by-function MIR shape so the
// next bootstrap phase has a copy-pasteable description of the gap.
func dumpMIRGap(t *testing.T, src string, err error) {
	t.Helper()
	t.Logf("stage0 declined real MIR — bootstrap gap")
	t.Logf("  source: %q", src)
	t.Logf("  reason: %v", err)
	req := newBackendRequest(t, EmitLLVMIR, src)
	if req.Entry.MIR == nil {
		return
	}
	for i, fn := range req.Entry.MIR.Functions {
		if fn == nil {
			continue
		}
		t.Logf("  fn[%d] %q params=%d locals=%d blocks=%d", i, fn.Name, len(fn.Params), len(fn.Locals), len(fn.Blocks))
		for j, bb := range fn.Blocks {
			if bb == nil {
				continue
			}
			t.Logf("    bb[%d] id=%d instrs=%d term=%T", j, bb.ID, len(bb.Instrs), bb.Term)
			for k, instr := range bb.Instrs {
				t.Logf("      instr[%d] %T", k, instr)
			}
		}
	}
}

// realProbeCase pairs a source snippet with the LLVM substring(s) we
// expect when stage0 covers it. When `skipNow` is true the test logs
// the gap rather than failing — useful as a discovery harness for
// future bootstrap phases.
type realProbeCase struct {
	name    string
	src     string
	wantIR  []string
	skipNow bool
}

func TestStage0RealMIRBaseline(t *testing.T) {
	// These cases work today — they cover the P1~P3c surface against
	// real front-end MIR output (rather than synthetic structs).
	// Keep them green: any regression here means stage0 stopped
	// matching what the front-end actually emits.
	cases := []realProbeCase{
		{
			name: "main_empty_body",
			src:  `fn main() {}`,
			wantIR: []string{
				"define i32 @main()",
				"ret i32 0",
			},
		},
		{
			name: "int_literal_return",
			src: `fn answer() -> Int { 42 }
fn main() {}`,
			wantIR: []string{
				"define i64 @answer()",
				"ret i64 42",
			},
		},
		{
			name: "negative_int_literal",
			src: `fn neg() -> Int { -1 }
fn main() {}`,
			wantIR: []string{
				"define i64 @neg()",
				"ret i64 -1",
			},
		},
		{
			name: "bool_literal_true",
			src: `fn yes() -> Bool { true }
fn main() {}`,
			wantIR: []string{
				"define i1 @yes()",
				"ret i1 true",
			},
		},
		{
			name: "param_passthrough",
			src: `fn id(x: Int) -> Int { x }
fn main() {}`,
			wantIR: []string{
				"define i64 @id(i64 %x)",
				"ret i64 %x",
			},
		},
		{
			name: "binary_add",
			src: `fn add(a: Int, b: Int) -> Int { a + b }
fn main() {}`,
			wantIR: []string{
				"define i64 @add(i64 %a, i64 %b)",
				"add i64",
			},
		},
		{
			name: "binary_sub",
			src: `fn diff(a: Int, b: Int) -> Int { a - b }
fn main() {}`,
			wantIR: []string{
				"sub i64",
			},
		},
		{
			name: "binary_mul",
			src: `fn prod(a: Int, b: Int) -> Int { a * b }
fn main() {}`,
			wantIR: []string{
				"mul i64",
			},
		},
		{
			name: "comparison_lt",
			src: `fn lt(a: Int, b: Int) -> Bool { a < b }
fn main() {}`,
			wantIR: []string{
				"icmp slt i64",
				"ret i1",
			},
		},
		{
			name: "if_else_const",
			src: `fn sign(x: Int) -> Int { if x > 0 { 1 } else { -1 } }
fn main() {}`,
			wantIR: []string{
				"icmp sgt",
				"phi i64",
			},
		},
		{
			name: "let_then_use",
			src: `fn double(x: Int) -> Int {
    let y = x
    y + y
}
fn main() {}`,
			wantIR: []string{
				"add i64 %x, %x",
			},
		},
		{
			name: "function_call",
			src: `fn add(a: Int, b: Int) -> Int { a + b }
fn caller() -> Int { add(1, 2) }
fn main() {}`,
			wantIR: []string{
				"call i64 @add(i64 1, i64 2)",
			},
		},
		{
			name: "while_loop_count",
			src: `fn count_to(n: Int) -> Int {
    let mut acc = 0
    while acc < n {
        acc = acc + 1
    }
    acc
}
fn main() {}`,
			wantIR: []string{
				"define i64 @count_to(i64 %n)",
				"%acc.slot = alloca i64",
				"store i64 0, ptr %acc.slot",
				"br label %header.1",
				"icmp slt i64",
				"br i1",
				"add i64",
				"store i64",
				"ret i64",
			},
		},
		{
			name: "for_in_range_sum",
			src: `fn sum(n: Int) -> Int {
    let mut acc = 0
    for i in 0..n {
        acc = acc + i
    }
    acc
}
fn main() {}`,
			wantIR: []string{
				"define i64 @sum(i64 %n)",
				"%acc.slot = alloca i64",
				"%i.slot = alloca i64",
				"store i64 0, ptr %acc.slot",
				"store i64 0, ptr %i.slot",
				"br label %header.1",
				"icmp slt i64",
				"br i1",
				"add i64",
				"br label %post.3",
				"post.3:",
				"add i64",
				"store i64",
				"br label %header.1",
				"ret i64",
			},
		},
		{
			name: "println_int_param",
			src: `fn show(n: Int) -> Int {
    println(n)
    n
}
fn main() {}`,
			wantIR: []string{
				"declare i32 @printf(ptr, ...)",
				`@.fmt.stage0.println.int = private unnamed_addr constant [6 x i8] c"%lld\0A\00"`,
				"define i64 @show(i64 %n)",
				"call i32 (ptr, ...) @printf(ptr @.fmt.stage0.println.int, i64 %n)",
				"ret i64 %n",
			},
		},
		{
			name: "println_const_in_loop",
			src: `fn shout(n: Int) -> Int {
    let mut acc = 0
    while acc < n {
        println(acc)
        acc = acc + 1
    }
    acc
}
fn main() {}`,
			wantIR: []string{
				"declare i32 @printf(ptr, ...)",
				"call i32 (ptr, ...) @printf(ptr @.fmt.stage0.println.int, i64 ",
			},
		},
		{
			name: "string_literal_return",
			src: `fn greet() -> String { "hi" }
fn main() {}`,
			wantIR: []string{
				`@.str.0 = private unnamed_addr constant [3 x i8] c"hi\00"`,
				"define ptr @greet()",
				"ret ptr @.str.0",
			},
		},
		{
			name: "struct_field_read_x",
			src: `struct Point { x: Int, y: Int }
fn first(p: Point) -> Int { p.x }
fn main() {}`,
			wantIR: []string{
				"%Point = type { i64, i64 }",
				"define i64 @first(ptr %p)",
				"getelementptr inbounds %Point, ptr %p, i32 0, i32 0",
				"load i64",
			},
		},
		{
			name: "struct_field_read_y",
			src: `struct Pair { a: Int, b: Int }
fn second(p: Pair) -> Int { p.b }
fn main() {}`,
			wantIR: []string{
				"%Pair = type { i64, i64 }",
				"getelementptr inbounds %Pair, ptr %p, i32 0, i32 1",
				"load i64",
			},
		},
		{
			name: "println_string_literal",
			src:  `fn main() { println("hi") }`,
			wantIR: []string{
				`@.fmt.stage0.println.str = private unnamed_addr constant [4 x i8] c"%s\0A\00"`,
				`@.str.0 = private unnamed_addr constant [3 x i8] c"hi\00"`,
				"call i32 (ptr, ...) @printf(ptr @.fmt.stage0.println.str, ptr @.str.0)",
			},
		},
		{
			name: "list_literal_len",
			src: `fn answer() -> Int {
    let xs: List<Int> = [1, 2, 3]
    xs.len()
}
fn main() {}`,
			wantIR: []string{
				"declare ptr @osty_rt_list_new()",
				"declare void @osty_rt_list_push_i64(ptr, i64)",
				"declare i64 @osty_rt_list_len(ptr)",
				"define i64 @answer()",
				"%0 = call ptr @osty_rt_list_new()",
				"call void @osty_rt_list_push_i64(ptr %0, i64 1)",
				"call void @osty_rt_list_push_i64(ptr %0, i64 2)",
				"call void @osty_rt_list_push_i64(ptr %0, i64 3)",
				"%1 = call i64 @osty_rt_list_len(ptr %0)",
				"ret i64 %1",
			},
		},
		{
			name: "string_concat_const_param",
			src: `fn greeting(name: String) -> String { "hello, " + name }
fn main() {}`,
			wantIR: []string{
				"declare ptr @osty_rt_strings_Concat(ptr, ptr)",
				`@.str.0 = private unnamed_addr constant [8 x i8] c"hello, \00"`,
				"define ptr @greeting(ptr %name)",
				"%0 = call ptr @osty_rt_strings_Concat(ptr @.str.0, ptr %name)",
				"ret ptr %0",
			},
		},
		{
			name: "string_concat_two_params",
			src: `fn join(a: String, b: String) -> String { a + b }
fn main() {}`,
			wantIR: []string{
				"declare ptr @osty_rt_strings_Concat(ptr, ptr)",
				"define ptr @join(ptr %a, ptr %b)",
				"%0 = call ptr @osty_rt_strings_Concat(ptr %a, ptr %b)",
			},
		},
		{
			name: "recursive_factorial",
			src: `fn fact(n: Int) -> Int {
    if n <= 1 { 1 } else { n * fact(n - 1) }
}
fn main() {}`,
			wantIR: []string{
				"define i64 @fact(i64 %n)",
				"icmp sle i64 %n, 1",
				"call i64 @fact(i64",
				"phi i64",
			},
		},
		{
			name: "struct_constructor_const_fields",
			src: `struct Point { x: Int, y: Int }
fn origin() -> Point { Point { x: 0, y: 0 } }
fn main() {}`,
			wantIR: []string{
				"%Point = type { i64, i64 }",
				// Named struct returns are boxed through osty_rt_stage0_alloc
				// and returned as ptr (see aggregateReturnIsHeapPtr) so call
				// sites — which treat user struct values as opaque ptr — see
				// a matching ABI.
				"define ptr @origin()",
				"%0 = insertvalue %Point poison, i64 0, 0",
				"%1 = insertvalue %Point %0, i64 0, 1",
				"store %Point %1, ptr %stage0.agg.ret.slot",
				"ret ptr %stage0.agg.ret.slot",
			},
		},
		{
			name: "struct_constructor_param_fields",
			src: `struct Point { x: Int, y: Int }
fn make(x: Int, y: Int) -> Point { Point { x: x, y: y } }
fn main() {}`,
			wantIR: []string{
				"%Point = type { i64, i64 }",
				"define ptr @make(i64 %x, i64 %y)",
				"%0 = insertvalue %Point poison, i64 %x, 0",
				"%1 = insertvalue %Point %0, i64 %y, 1",
				"store %Point %1, ptr %stage0.agg.ret.slot",
				"ret ptr %stage0.agg.ret.slot",
			},
		},
		{
			name: "tuple_int_int_return",
			src: `fn pair() -> (Int, Int) { (1, 2) }
fn main() {}`,
			wantIR: []string{
				"%.tuple.0 = type { i64, i64 }",
				"define %.tuple.0 @pair()",
				"%0 = insertvalue %.tuple.0 poison, i64 1, 0",
				"%1 = insertvalue %.tuple.0 %0, i64 2, 1",
				"ret %.tuple.0 %1",
			},
		},
		{
			name: "list_literal_index_first",
			src: `fn first() -> Int {
    let xs: List<Int> = [10, 20, 30]
    xs[0]
}
fn main() {}`,
			wantIR: []string{
				"declare ptr @osty_rt_list_new()",
				"declare void @osty_rt_list_push_i64(ptr, i64)",
				"declare i64 @osty_rt_list_get_i64(ptr, i64)",
				"define i64 @first()",
				"%0 = call ptr @osty_rt_list_new()",
				"call void @osty_rt_list_push_i64(ptr %0, i64 10)",
				"call void @osty_rt_list_push_i64(ptr %0, i64 20)",
				"call void @osty_rt_list_push_i64(ptr %0, i64 30)",
				"= call i64 @osty_rt_list_get_i64(ptr %0, i64 0)",
				"ret i64 %stage0.list.get",
			},
		},
		{
			name: "list_literal_index_second",
			src: `fn second() -> Int {
    let xs: List<Int> = [100, 200, 300]
    xs[1]
}
fn main() {}`,
			wantIR: []string{
				"= call i64 @osty_rt_list_get_i64(ptr %0, i64 1)",
			},
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got, err := realStage0Run(t, c.src)
			if err != nil {
				dumpMIRGap(t, c.src, err)
				t.Fatalf("stage0 declined %q: %v", c.name, err)
			}
			for _, want := range c.wantIR {
				if !strings.Contains(string(got), want) {
					t.Errorf("emitted IR for %q missing %q:\n%s", c.name, want, got)
				}
			}
		})
	}
}

// TestStage0RealMIRGapProbe records known-gap shapes — patterns we
// suspect stage0 cannot handle yet. When skipNow=true the test logs
// the gap reason and skips. As future bootstrap phases extend
// coverage, flip skipNow=false to lock in the new capability.
func TestStage0RealMIRGapProbe(t *testing.T) {
	cases := []realProbeCase{}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			_, err := realStage0Run(t, c.src)
			if err == nil {
				t.Fatalf("stage0 unexpectedly accepted %q — flip skipNow=false and add wantIR assertions", c.name)
			}
			if c.skipNow {
				dumpMIRGap(t, c.src, err)
				t.Skipf("known stage0 gap %q (recorded for future bootstrap phase)", c.name)
				return
			}
			t.Fatalf("stage0 declined %q: %v", c.name, err)
		})
	}
}
