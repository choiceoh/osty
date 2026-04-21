package llvmgen

import (
	"strings"
	"testing"
)

func TestGenerateStringLenMethodDispatch(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let n = "hello".len()
    println(n)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/string_len_method.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare i64 @osty_rt_strings_ByteLen(ptr)",
		"call i64 @osty_rt_strings_ByteLen",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateNestedFieldListLenMethodDispatch(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Version {
    build: List<String>
}

fn main() {
    let v = Version { build: ["meta", "data"] }
    println(v.build.len())
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/nested_field_len.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"%Version = type { ptr }",
		"call i64 @osty_rt_list_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateDiscardedListPopNoLongerTripsLLVM015(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let mut values: List<Int> = [1, 2, 3]
    let _ = values.pop()
    println(values.len())
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/list_pop_discard.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"call void @osty_rt_list_pop_discard",
		"call i64 @osty_rt_list_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestGenerateExprStmtListPopNoLongerTripsLLVM015(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let mut values: List<Int> = [1, 2]
    values.pop()
    println(values.len())
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/list_pop_expr_stmt.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	if got := string(ir); !strings.Contains(got, "call void @osty_rt_list_pop_discard") {
		t.Fatalf("generated IR missing list pop discard call:\n%s", got)
	}
}

func TestGenerateIndexedNestedListLenMethodDispatch(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let stacks: List<List<Int>> = [[1, 2], [3]]
    let stack = stacks[0]
    println(stack.len())
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/indexed_nested_list_len.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	if got := string(ir); !strings.Contains(got, "call i64 @osty_rt_list_len") {
		t.Fatalf("generated IR missing list len call:\n%s", got)
	}
}

// Phase 1 of the first-class fn value lowering: a top-level fn used
// in value position materialises a closure env + thunk, and the
// subsequent call through the bound name dispatches indirectly.
func TestGenerateFnValueBoundLocalIndirectCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn identity(x: Int) -> Int {
    x
}

fn main() {
    let f = identity
    println(f(42))
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/fn_value_bound_local.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		// Thunk symbol for the top-level identity fn.
		"define private i64 @__osty_closure_thunk_identity(ptr %env, i64 %arg0)",
		// Thunk body delegates to real symbol.
		"call i64 @identity(i64 %arg0)",
		// Env allocated on the GC heap (not stack) so the value can
		// outlive the enclosing frame.
		"call ptr @osty.gc.alloc_v1(i64 1, i64 8,",
		"store ptr @__osty_closure_thunk_identity, ptr",
		// Indirect call at the use site: load fn ptr from env[0],
		// invoke through the loaded ptr with env as implicit arg 0.
		"= load ptr, ptr",
		"= call i64 (ptr, i64)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	// The env must be registered as a GC root slot so a collection
	// triggered between the env-alloc and the indirect call won't
	// reclaim it.
	if !strings.Contains(got, "osty.gc.root_bind_v1") {
		t.Fatalf("env not registered as a GC root:\n%s", got)
	}
}

// Regression: if a fn is used as a value AND as a direct call in the
// same module, the direct call path must keep emitting a plain
// `call @sym` (not through the thunk). The thunk exists for the
// value-position use only.
func TestGenerateFnValueCoexistsWithDirectCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn double(x: Int) -> Int {
    x + x
}

fn main() {
    let f = double
    let a = f(3)
    let b = double(4)
    println(a + b)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/fn_value_coexists.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	// The direct call `double(4)` must route through the real symbol,
	// not the thunk. The indirect call through `f` goes through the
	// thunk (evidenced by the env load + typed-signature call).
	if !strings.Contains(got, "call i64 @double(i64") {
		t.Fatalf("direct call to @double missing:\n%s", got)
	}
	if strings.Count(got, "__osty_closure_thunk_double") < 2 {
		// One occurrence in the `define` header, one in the `store`
		// that materialises the env (the GC-alloc line itself doesn't
		// reference the thunk name, only the post-alloc store does).
		t.Fatalf("expected thunk define + env store for @double, got:\n%s", got)
	}
}

func TestGenerateStringCharsNoLongerTripsLLVM015(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let chars = "abc".chars()
    println(chars.len())
}
`)

	_, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/string_chars_dispatch.osty",
	})
	if err == nil {
		t.Fatal("Generate succeeded unexpectedly; wanted the next non-LLVM015 limitation")
	}
	if strings.Contains(err.Error(), "LLVM015") {
		t.Fatalf("String.chars still failed in FieldExpr call dispatch: %v", err)
	}
}

// Phase 2: a struct field whose declared type is fn(...) becomes
// the callee via `obj.field(args)`. The field value is loaded as a
// ptr env and dispatched through the same indirect-call ABI.
func TestGenerateFnTypedStructFieldIndirectCall(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Hook {
    cb: fn(Int) -> Int,
}

fn inc(n: Int) -> Int {
    n + 1
}

fn main() {
    let h = Hook { cb: inc }
    println(h.cb(41))
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/fn_typed_struct_field.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		// Struct with fn-typed field lowers to a one-ptr aggregate.
		"%Hook = type { ptr }",
		// Thunk for inc so the value form has the env-first ABI.
		"define private i64 @__osty_closure_thunk_inc(ptr %env, i64 %arg0)",
		// The env is GC-allocated rather than stack-allocated.
		"call ptr @osty.gc.alloc_v1(i64 1, i64 8,",
		// The FieldExpr call path loads the fn ptr from env and
		// dispatches through it.
		"= call i64 (ptr, i64)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// Phase 3: a function-typed parameter becomes the callee at an
// indirect dispatch site inside the function body.
func TestGenerateFnTypedParameterHigherOrder(t *testing.T) {
	file := parseLLVMGenFile(t, `fn apply(f: fn(Int) -> Int, x: Int) -> Int {
    f(x)
}

fn inc(n: Int) -> Int {
    n + 1
}

fn main() {
    println(apply(inc, 41))
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/fn_typed_param.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		// apply takes a ptr env as its first user-visible param.
		"define i64 @apply(ptr %f, i64 %x)",
		// The call-site inside apply loads fn ptr from env and dispatches.
		"= call i64 (ptr, i64)",
		// main materialises a GC-heap env for `inc` and passes it to apply.
		"call ptr @osty.gc.alloc_v1(i64 1, i64 8,",
		"define private i64 @__osty_closure_thunk_inc(ptr %env, i64 %arg0)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
