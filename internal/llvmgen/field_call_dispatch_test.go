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

// TestGenerateCollectionIsEmptyLowersAsLenEqZero verifies that
// `list.isEmpty()`, `map.isEmpty()`, and `set.isEmpty()` all inline
// the stdlib default body (`self.len() == 0`) as a runtime `*_len`
// call followed by `icmp eq i64 ..., 0`, instead of first-walling on
// LLVM015 [method_call_field] for the default method. Map's `len` /
// `isEmpty` share the same runtime helper (`osty_rt_map_len`) since
// the Go wrapper was only declared — the C body had to land alongside
// the dispatch.
func TestGenerateCollectionIsEmptyLowersAsLenEqZero(t *testing.T) {
	file := parseLLVMGenFile(t, `fn allEmpty(xs: List<Int>, m: Map<String, Int>, s: Set<Int>) -> Bool {
    xs.isEmpty() && m.isEmpty() && s.isEmpty()
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/isempty.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call i64 @osty_rt_list_len(",
		"call i64 @osty_rt_map_len(",
		"call i64 @osty_rt_set_len(",
		"icmp eq i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
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

// TestGenerateReturnedListOfStringsLiteralPreservesStringHint verifies
// that a function returning `List<String>` with a bare list-of-literals
// body propagates the `listElemString=true` hint through
// `emitReturningBlock` and into `emitListExprWithHint`. Before this
// wiring, the return-stmt path hardcoded `false` for the isString
// flag, so the first element of the literal inherited that and then
// every subsequent element's `isStringElem=true` tripped the
// heterogeneous-ptr check. This is the concrete shape in
// `toolchain/llvmgen.osty:llvmGcRuntimeDeclarations`.
func TestGenerateReturnedListOfStringsLiteralPreservesStringHint(t *testing.T) {
	file := parseLLVMGenFile(t, `pub fn declarations() -> List<String> {
    [
        "declare ptr @osty.gc.alloc_v1(i64, i64, ptr)",
        "declare void @osty.gc.pre_write_v1(ptr, ptr, i64)",
        "declare void @osty.gc.post_write_v1(ptr, ptr, i64)",
    ]
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/return_list_of_strings.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call ptr @osty_rt_list_new()",
		"call void @osty_rt_list_push_ptr(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateNonAsciiStringLiteralLowersAsByteEscapes verifies plain
// String literals containing multi-byte UTF-8 code points (BOM,
// Korean, emoji) now lower through `llvmCStringEscape` as one `\HH`
// escape per UTF-8 byte instead of tripping
// `LLVM011 [string_non_ascii]`. Previously the backend restricted
// literals to printable ASCII + \n \t \r \x1f; now the gate is a
// no-op because the escaper walks bytes and byte-escapes everything
// outside the printable ASCII range (minus `"` / `\`).
//
// The toolchain lexer at `toolchain/frontend.osty:600` (`unit ==
// "\u{FEFF}"`) was the probe's first wall after the `list_mixed_ptr`
// fix landed; other sites like the monomorphization key builder use
// `\u{1F}` and are also covered here.
func TestGenerateNonAsciiStringLiteralLowersAsByteEscapes(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let bom = "\u{FEFF}"
    let sep = "\u{1F}"
    let hello = "안녕"
    println(bom)
    println(sep)
    println(hello)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/non_ascii_literal.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	// BOM = U+FEFF → UTF-8 EF BB BF.
	// Unit Separator = U+001F → UTF-8 1F.
	// 안 = U+C548 → UTF-8 EC 95 88; 녕 = U+B155 → UTF-8 EB 85 95.
	for _, want := range []string{
		`\EF\BB\BF`,
		`\1F`,
		`\EC\95\88\EB\85\95`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing byte escape %q:\n%s", want, got)
		}
	}
}

// TestGenerateListLiteralOfStringsPropagatesSourceType covers three
// paths that previously dropped the `String` source type from list
// literal elements and first-walled on
// `LLVM011 list literal mixes String and non-String ptr-backed values`:
//
//  1. alias-qualified stdlib strings call (`strings.join(...)`) —
//     now routed through staticStdStringsCallSourceType so the
//     return-type is known to be String.
//  2. plain String literal (`""`) bound through `let mut` and reused
//     in a later list literal — staticExprSourceType walks the
//     binding's sourceType, which is now tagged at literal-emission
//     time.
//  3. if-expression whose both branches are String literals — the
//     merged phi value now carries the agreed String source type
//     (mergeContainerMetadata -> sameSourceType).
//
// All three shapes appear in `toolchain/formatter_ast.osty`:
// `strings.join([strings.join(parts, ", "), ...], "")`,
// `lines.push(strings.join([ostyAstIndent(1), line], ""))` (line is
// `let mut line = ""`), and `strings.join([pat, op, pat], "")` where
// `op` binds an if-expression.
func TestGenerateListLiteralOfStringsPropagatesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn indent(level: Int) -> String {
    strings.repeat("  ", level)
}

fn render(parts: List<String>, flag: Int) -> String {
    let mut line: String = ""
    line = strings.join(parts, ", ")
    let op = if flag == 1 { "<" } else { ">" }
    let chunks = [indent(1), line, strings.join([op, " end"], "")]
    strings.join(chunks, "")
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/list_mixed_ptr.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call ptr @osty_rt_list_new()",
		"call ptr @osty_rt_strings_Join",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// A List<String>-typed function body whose last expression is a
// multi-element list literal of plain String literals previously
// tripped `list_mixed_ptr` because emitExprWithHintAndSourceType only
// derived `listElemString = true` from the return sourceType when the
// caller had not already wired `listElemTyp`. Now the elemString flag
// is backfilled whenever sourceType encodes List<String> and matches
// the caller's listElemTyp.
func TestGenerateListOfStringLiteralsReturnKeepsStringFlag(t *testing.T) {
	file := parseLLVMGenFile(t, `pub fn gcDecls() -> List<String> {
    [
        "declare ptr @osty.gc.alloc_v1(i64, i64, ptr)",
        "declare void @osty.gc.pre_write_v1(ptr, ptr, i64)",
    ]
}

fn main() {
    let xs = gcDecls()
}
`)
	_, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/list_ret_strings.osty"})
	if err != nil {
		t.Fatalf("List<String> return of plain String literals tripped: %v", err)
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
		// Env allocated on the GC heap via the Phase A4 dedicated
		// closure allocator (RUNTIME_GC_DELTA §2.4), so the trace
		// callback for captures is installed at construction even
		// though Phase 1 passes 0 captures.
		"call ptr @osty.rt.closure_env_alloc_v1(i64 0, ptr",
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

// TestGenerateStringCharsLowersToRuntimeCall verifies String.chars()
// dispatches to osty_rt_strings_Chars and yields a GC-managed list whose
// element width is i32 (the Char lowering). `chars.len()` must keep
// working through the untyped osty_rt_list_len symbol.
func TestGenerateStringCharsLowersToRuntimeCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let chars = "abc".chars()
    println(chars.len())
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/string_chars_dispatch.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_strings_Chars(ptr)",
		"call ptr @osty_rt_strings_Chars(",
		"declare i64 @osty_rt_list_len(ptr)",
		"call i64 @osty_rt_list_len(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected IR to contain %q, got:\n%s", want, got)
		}
	}
}

// TestGenerateStringCharsIterationUsesBytesV1 verifies that iterating a
// list produced by String.chars() goes through the non-typed runtime
// path (osty_rt_list_get_bytes) with a 4-byte slot — matching the
// Char → i32 lowering — rather than falling into the typed i64/ptr
// symbol set.
func TestGenerateStringCharsIterationUsesBytesV1(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    for c in "abc".chars() {
        println(c.toInt())
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/string_chars_for_in.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call ptr @osty_rt_strings_Chars(",
		// for-in over i32 elements routes through the _v1 bytes helper with a 4-byte slot.
		"call void @osty_rt_list_get_bytes_v1(",
		"alloca i32",
		"load i32, ptr %t",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected IR to contain %q, got:\n%s", want, got)
		}
	}
	// The typed i64/ptr paths are for other element types — they must not
	// fire for a List<Char>.
	if strings.Contains(got, "call i64 @osty_rt_list_get_i64(") ||
		strings.Contains(got, "call ptr @osty_rt_list_get_ptr(") {
		t.Fatalf("chars() iteration wrongly hit typed runtime:\n%s", got)
	}
}

// TestGenerateStringBytesLowersToRuntimeCall verifies String.bytes()
// dispatches to osty_rt_strings_Bytes and produces a byte-width list.
func TestGenerateStringBytesLowersToRuntimeCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let bs = "abc".bytes()
    println(bs.len())
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/string_bytes_dispatch.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_strings_Bytes(ptr)",
		"call ptr @osty_rt_strings_Bytes(",
		"declare i64 @osty_rt_list_len(ptr)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected IR to contain %q, got:\n%s", want, got)
		}
	}
}

// TestGenerateStringBytesIterationUsesBytesV1 mirrors the chars()
// iteration check for bytes(): for-in over i8 elements must use the
// _v1 bytes helper with a 1-byte slot — not the typed i64/i1/ptr path.
func TestGenerateStringBytesIterationUsesBytesV1(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    for b in "abc".bytes() {
        println(b.toInt())
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/string_bytes_for_in.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call ptr @osty_rt_strings_Bytes(",
		"call void @osty_rt_list_get_bytes_v1(",
		"alloca i8",
		"load i8, ptr %t",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected IR to contain %q, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "call i64 @osty_rt_list_get_i64(") ||
		strings.Contains(got, "call ptr @osty_rt_list_get_ptr(") {
		t.Fatalf("bytes() iteration wrongly hit typed runtime:\n%s", got)
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
		// Phase A4 (RUNTIME_GC_DELTA §2.4): closure env goes through
		// the dedicated runtime allocator so its capture-tracing
		// callback is installed at construction. Phase 1 passes 0
		// captures; Phase 4 will grow the count.
		"call ptr @osty.rt.closure_env_alloc_v1(i64 0, ptr",
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
		// main materialises a GC-heap env for inc and passes it to
		// apply via the Phase A4 closure-dedicated allocator.
		"call ptr @osty.rt.closure_env_alloc_v1(i64 0, ptr",
		"define private i64 @__osty_closure_thunk_inc(ptr %env, i64 %arg0)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
