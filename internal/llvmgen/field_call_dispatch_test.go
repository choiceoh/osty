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

// Statement-position twin of the higher-order path above: a fn-typed
// parameter returning unit still has to dispatch through the indirect
// call ABI. This is the exact shape specialized Map.forEach bodies hit
// after monomorphization (`f(key, value)`).
func TestGenerateFnTypedParameterStmtIndirectCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn apply2(f: fn(Int, Int)) {
    f(1, 2)
}

fn printPair(a: Int, b: Int) {
    println(a)
    println(b)
}

fn main() {
    apply2(printPair)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/fn_typed_param_stmt.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"define void @apply2(ptr %f)",
		"call void (ptr, i64, i64)",
		"call ptr @osty.rt.closure_env_alloc_v1(i64 0, ptr",
		"define private void @__osty_closure_thunk_printPair(ptr %env, i64 %arg0, i64 %arg1)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateMapGetLowersAsOptionReturningIntrinsic locks the root-cause
// fix: Map.get(key) -> V? now goes through the real
// osty_rt_map_get_<K> runtime helper (bool return + out-param), with
// V=ptr using the pre-zeroed-slot fast path so a miss yields a null
// Option without branching. This is the intrinsic future bodied
// helpers (getOr, containsKey, update, ...) compose on top of.
func TestGenerateMapGetLowersAsOptionReturningIntrinsic(t *testing.T) {
	file := parseLLVMGenFile(t, `fn lookup(m: Map<String, String>, k: String) -> String? {
    m.get(k)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/map_get.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare i1 @osty_rt_map_get_string(ptr, ptr, ptr)",
		"alloca ptr",
		"store ptr null, ptr",
		"call i1 @osty_rt_map_get_string(",
		"load ptr, ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	// The old contains+get_or_abort special case must not reappear.
	if strings.Contains(got, "osty_rt_map_contains_string") ||
		strings.Contains(got, "osty_rt_map_get_or_abort_string") {
		t.Fatalf("map.get wrongly routed through legacy contains/get_or_abort helpers:\n%s", got)
	}
}

// TestGenerateMapGetOrComposesOnGetAndCoalesce verifies Map.getOr is now
// lowered as the stdlib bodied shape: `self.get(key) ?? default` — i.e.
// Map.get intrinsic producing Option<V>, then a ptr-backed coalesce.
// Replaces the earlier contains+get_or_abort+phi hack so future
// canonical helpers (update, mergeWith, ...) inherit the same
// composable stack.
func TestGenerateMapGetOrComposesOnGetAndCoalesce(t *testing.T) {
	file := parseLLVMGenFile(t, `fn lookup(m: Map<String, String>, k: String, d: String) -> String {
    m.getOr(k, d)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/map_getor.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare i1 @osty_rt_map_get_string(ptr, ptr, ptr)",
		"call i1 @osty_rt_map_get_string(",
		"icmp eq ptr",
		"br i1 ",
		"= phi ptr [",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "osty_rt_map_contains_string") ||
		strings.Contains(got, "osty_rt_map_get_or_abort_string") {
		t.Fatalf("map.getOr still routes through legacy helpers:\n%s", got)
	}
}

// TestGenerateMapGetScalarValueBoxLowering verifies the V=scalar path
// of Map.get: the helper writes i64 into a stack slot, the present
// branch GC-allocs a box, copies the payload in, and the phi merges
// ptr-to-box vs null. Consumers see the boxed-Option ABI (ptr) with
// a valid `load i64, ptr` in the unwrap path — confirmed later by
// the getOr scalar test.
func TestGenerateMapGetScalarValueBoxLowering(t *testing.T) {
	file := parseLLVMGenFile(t, `fn lookup(m: Map<String, Int>, k: String) -> Int? {
    m.get(k)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/map_get_int.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare i1 @osty_rt_map_get_string(ptr, ptr, ptr)",
		"alloca i64",
		"call i1 @osty_rt_map_get_string(",
		// Present branch GC-allocs an 8-byte box for i64.
		"call ptr @osty.gc.alloc_v1(i64 1, i64 8,",
		"store i64 ",
		"= phi ptr [",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateMapGetOrScalarValueUsesDirectLookup verifies Map.getOr
// for scalar V uses the bool-returning map_get runtime directly: a
// stack slot receives the payload on hit, the hit branch loads it, and
// the miss branch falls back to the scalar default without allocating
// an Option box.
func TestGenerateMapGetOrScalarValueUnwrapsBox(t *testing.T) {
	file := parseLLVMGenFile(t, `fn count(m: Map<String, Int>, k: String) -> Int {
    m.getOr(k, 0)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/map_getor_int.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call i1 @osty_rt_map_get_string(",
		"alloca i64",
		"br i1 ",
		"load i64, ptr",
		"= phi i64 [",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "call ptr @osty.gc.alloc_v1") {
		t.Fatalf("scalar getOr should not allocate an Option box:\n%s", got)
	}
	if strings.Contains(got, "osty_rt_map_contains_string") ||
		strings.Contains(got, "osty_rt_map_get_or_abort_string") {
		t.Fatalf("scalar getOr still routes through legacy helpers:\n%s", got)
	}
}

// TestGenerateMapUpdateComposesOnGetAndInsert covers the most-used
// canonical helper (CLAUDE.md §B.9.1.64.1): `m.update(k, f)` with
// f: fn(V?) -> V. The lowering is get intrinsic + fn-value indirect
// call + insert intrinsic — no iteration, no special-case inline.
// Using a top-level fn as f keeps the test focused on the composition
// rather than closure capture.
func TestGenerateMapUpdateComposesOnGetAndInsert(t *testing.T) {
	file := parseLLVMGenFile(t, `fn bump(n: Int?) -> Int {
    (n ?? 0) + 1
}

fn tally(m: Map<String, Int>, k: String) {
    m.update(k, bump)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/map_update.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call i1 @osty_rt_map_get_string(",
		// fn-value thunk for `bump`.
		"define private i64 @__osty_closure_thunk_bump(ptr %env, ptr %arg0)",
		// Indirect call ABI: ret (ptr, arg-type) through loaded fn ptr.
		"= call i64 (ptr, ptr)",
		// Insert after callback returns new V.
		"call void @osty_rt_map_insert_string(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "osty_rt_map_get_or_abort_string") {
		t.Fatalf("update wrongly routed through legacy get_or_abort:\n%s", got)
	}
}

// TestGenerateMapMergeWithCombinesOnCollision locks the mergeWith
// shape: new map allocation, two-pass snapshot iteration (copy self,
// then merge other with combine-on-collision), and the get+branch-on-
// null per-key decision. combine is called only on collision.
// Iteration goes through osty_rt_map_keys (snapshot) + per-key get —
// NOT the raw key_at/value_at walk, so concurrent mutation of self
// or other can't trip out-of-bounds.
func TestGenerateMapMergeWithCombinesOnCollision(t *testing.T) {
	file := parseLLVMGenFile(t, `fn addCombine(a: Int, b: Int) -> Int {
    a + b
}

fn merge(a: Map<String, Int>, b: Map<String, Int>) -> Map<String, Int> {
    a.mergeWith(b, addCombine)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/map_mergewith.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call ptr @osty_rt_map_new(",
		"call ptr @osty_rt_map_keys(",      // snapshot keys, not key_at index walk
		"call i1 @osty_rt_map_get_string(", // per-key get (atomic under lock)
		"= call i64 (ptr, i64, i64)",       // combine indirect call
		"call void @osty_rt_map_insert_string(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	// Snapshot iteration must NOT use key_at/value_at (index walk is
	// unsafe under concurrent mutation).
	if strings.Contains(got, "osty_rt_map_key_at_string") ||
		strings.Contains(got, "osty_rt_map_value_at") {
		t.Fatalf("mergeWith still uses index walk key_at/value_at — snapshot regressed:\n%s", got)
	}
}

// TestGenerateMapMapValuesRebuildsWithNewValueType verifies mapValues
// constructs a new map whose V type is the callback's return type
// (R), routes every entry through the indirect fn call, and uses the
// snapshot iterator so concurrent mutation is safe.
func TestGenerateMapMapValuesRebuildsWithNewValueType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn stringify(n: Int) -> String {
    "{n}"
}

fn labels(m: Map<String, Int>) -> Map<String, String> {
    m.mapValues(stringify)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/map_mapvalues.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call ptr @osty_rt_map_new(",
		"call ptr @osty_rt_map_keys(",      // snapshot, not index walk
		"call i1 @osty_rt_map_get_string(", // per-key lookup
		// fn-value indirect call: (ptr env, i64 v) -> ptr
		"= call ptr (ptr, i64)",
		"call void @osty_rt_map_insert_string(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "osty_rt_map_key_at_string") ||
		strings.Contains(got, "osty_rt_map_value_at") {
		t.Fatalf("mapValues regressed to index walk:\n%s", got)
	}
}

// TestGenerateMapUpdateRunsUnderLock verifies the update composition
// (get + callback + insert) is wrapped in a per-map lock/unlock pair.
// The lock is the root-cause fix for the "observation-then-mutation"
// race — without it, a concurrent insert could slip between the get
// and the insert, silently losing data.
func TestGenerateMapUpdateRunsUnderLock(t *testing.T) {
	file := parseLLVMGenFile(t, `fn bump(n: Int?) -> Int {
    (n ?? 0) + 1
}

fn tally(m: Map<String, Int>, k: String) {
    m.update(k, bump)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/map_update_locked.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare void @osty_rt_map_lock(ptr)",
		"declare void @osty_rt_map_unlock(ptr)",
		"call void @osty_rt_map_lock(",
		"call void @osty_rt_map_unlock(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("update must be wrapped in map_lock/unlock, missing %q:\n%s", want, got)
		}
	}
	// Sanity: lock must precede unlock in the IR stream.
	lockIdx := strings.Index(got, "call void @osty_rt_map_lock(")
	unlockIdx := strings.Index(got, "call void @osty_rt_map_unlock(")
	if lockIdx < 0 || unlockIdx < 0 || unlockIdx < lockIdx {
		t.Fatalf("unlock (%d) must appear after lock (%d):\n%s", unlockIdx, lockIdx, got)
	}
}

// TestGenerateMapRetainIfUsesSnapshotIteration verifies retainIf no
// longer uses index-based key_at/value_at (which races against
// concurrent mutators that could change map length mid-walk). It
// iterates the keys snapshot returned by osty_rt_map_keys + per-key
// get, matching the stdlib body's safe semantics.
func TestGenerateMapRetainIfUsesSnapshotIteration(t *testing.T) {
	file := parseLLVMGenFile(t, `fn keepPositive(k: String, v: Int) -> Bool {
    v > 0
}

fn prune(m: Map<String, Int>) {
    m.retainIf(keepPositive)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/map_retainif_snapshot.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call ptr @osty_rt_map_keys(",
		"call i1 @osty_rt_map_get_string(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("retainIf should iterate snapshot, missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "osty_rt_map_key_at_string") ||
		strings.Contains(got, "osty_rt_map_value_at") {
		t.Fatalf("retainIf still uses index walk key_at/value_at:\n%s", got)
	}
}

// TestGenerateMapRetainIfTwoPass verifies `m.retainIf(pred)` emits the
// collect-then-remove shape: pass 1 walks a frozen keys snapshot,
// invokes pred, and pushes failing keys into a victim List<K>; pass
// 2 iterates victims and calls map_remove. Two passes because map
// mutation during iteration is undefined; snapshot iteration because
// a concurrent mutator can't be allowed to break the walk.
func TestGenerateMapRetainIfTwoPass(t *testing.T) {
	file := parseLLVMGenFile(t, `fn keepPositive(k: String, v: Int) -> Bool {
    v > 0
}

fn prune(m: Map<String, Int>) {
    m.retainIf(keepPositive)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/map_retainif.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call ptr @osty_rt_map_keys(",      // snapshot keys for pass 1
		"call i1 @osty_rt_map_get_string(", // per-key get under lock
		// fn-value thunk for pred.
		"define private i1 @__osty_closure_thunk_keepPositive(ptr %env, ptr %arg0, i64 %arg1)",
		"= call i1 (ptr, ptr, i64)",
		"call ptr @osty_rt_list_new()",
		"call void @osty_rt_list_push_ptr(",
		// Second pass calls map_remove per victim.
		"call i1 @osty_rt_map_remove_string(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateMapForInLowersAsIndexedWalk locks map iteration
// infrastructure: `for (k, v) in m` over Map<String, Int> lowers as
// map_len + index loop + key_at + value_at slot accessors. This is the
// primitive `retainIf`/`mergeWith`/`mapValues` compose on top of — it
// MUST work on arbitrary user code, not just stdlib helpers.
func TestGenerateMapForInLowersAsIndexedWalk(t *testing.T) {
	file := parseLLVMGenFile(t, `fn sum(m: Map<String, Int>) -> Int {
    let mut total = 0
    for (k, v) in m {
        total = total + v
    }
    total
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/map_for_in.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare i64 @osty_rt_map_len(ptr)",
		"declare ptr @osty_rt_map_key_at_string(ptr, i64)",
		"declare void @osty_rt_map_value_at(ptr, i64, ptr)",
		"call i64 @osty_rt_map_len(",
		"call ptr @osty_rt_map_key_at_string(",
		"call void @osty_rt_map_value_at(",
		"alloca i64",
		"load i64, ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestGenerateMapGetBoolValueUses1ByteStackSlot exercises the scalar
// getOr fast path for i1 V: the lookup writes into a 1-byte stack slot,
// the hit branch loads the bool, and the miss branch falls back to the
// literal default without allocating an Option box.
func TestGenerateMapGetBoolValueUses1ByteStackSlot(t *testing.T) {
	file := parseLLVMGenFile(t, `fn flag(m: Map<String, Bool>, k: String) -> Bool {
    m.getOr(k, false)
}
`)
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/map_getor_bool.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"call i1 @osty_rt_map_get_string(",
		"alloca i1",
		"load i1, ptr",
		"= phi i1 [",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "call ptr @osty.gc.alloc_v1") {
		t.Fatalf("bool getOr should not allocate an Option box:\n%s", got)
	}
	if strings.Contains(got, "osty_rt_map_contains_string") ||
		strings.Contains(got, "osty_rt_map_get_or_abort_string") {
		t.Fatalf("bool getOr still routes through legacy helpers:\n%s", got)
	}
}
