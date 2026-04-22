package llvmgen

import (
	"strings"
	"testing"
)

// String method-call dispatch — `s.trimPrefix(p)`, `s.trimSuffix(p)`,
// `s.contains(n)`, `s.endsWith(p)` — must lower to the same runtime
// helpers that the free-function `strings.*` forms already use. Prior
// to this commit the toolchain's `trimmed.trimPrefix("runtime.")` call
// in llvmgen.osty tripped LLVM015 because only `startsWith` was
// dispatched; the rest fell through to the "not a known fn" wall.
func TestStringTrimPrefixMethodCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn drop(prefix: String, s: String) -> String {
    s.trimPrefix(prefix)
}

fn main() {
    println(drop("runtime.", "runtime.strings"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_trim_prefix_method.osty"})
	if err != nil {
		t.Fatalf("trimPrefix method call errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_TrimPrefix",
		"declare ptr @osty_rt_strings_TrimPrefix(ptr, ptr)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("trimPrefix method call missing %q:\n%s", want, got)
		}
	}
}

func TestStringTrimSuffixMethodCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn drop(suffix: String, s: String) -> String {
    s.trimSuffix(suffix)
}

fn main() {
    println(drop(".osty", "lexer.osty"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_trim_suffix_method.osty"})
	if err != nil {
		t.Fatalf("trimSuffix method call errored: %v", err)
	}
	if got := string(ir); !strings.Contains(got, "@osty_rt_strings_TrimSuffix") {
		t.Fatalf("trimSuffix method call did not invoke runtime helper:\n%s", got)
	}
}

func TestStringContainsMethodCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn has(s: String, n: String) -> Bool {
    s.contains(n)
}

fn main() {
    println(has("hello, world", "world"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_contains_method.osty"})
	if err != nil {
		t.Fatalf("contains method call errored: %v", err)
	}
	if got := string(ir); !strings.Contains(got, "@osty_rt_strings_Contains") {
		t.Fatalf("contains method call did not invoke runtime helper:\n%s", got)
	}
}

func TestStringEndsWithMethodCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn endsTest(s: String, suffix: String) -> Bool {
    s.endsWith(suffix)
}

fn main() {
    println(endsTest("foo.osty", ".osty"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_ends_with_method.osty"})
	if err != nil {
		t.Fatalf("endsWith method call errored: %v", err)
	}
	if got := string(ir); !strings.Contains(got, "@osty_rt_strings_HasSuffix") {
		t.Fatalf("endsWith method call did not invoke HasSuffix runtime helper:\n%s", got)
	}
}

func TestStringRepeatMethodCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn dup(s: String, n: Int) -> String {
    s.repeat(n)
}

fn main() {
    println(dup("ab", 3))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_repeat_method.osty"})
	if err != nil {
		t.Fatalf("repeat method call errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_Repeat",
		"declare ptr @osty_rt_strings_Repeat(ptr, i64)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("repeat method call missing %q:\n%s", want, got)
		}
	}
}

func TestStringJoinMethodCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn render(parts: List<String>) -> String {
    ", ".join(parts)
}

fn main() {
    println(render(["a", "b", "c"]))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_join_method.osty"})
	if err != nil {
		t.Fatalf("join method call errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_Join",
		"declare ptr @osty_rt_strings_Join(ptr, ptr)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("join method call missing %q:\n%s", want, got)
		}
	}
}

func TestStringLinesMethodCarriesListMetadata(t *testing.T) {
	file := parseLLVMGenFile(t, `fn lineCount(s: String) -> Int {
    s.lines().len()
}

fn main() {
    println(lineCount("a\nb\nc"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_lines_method.osty"})
	if err != nil {
		t.Fatalf("lines method call errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_Split",
		"@osty_rt_list_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("lines method chain missing %q:\n%s", want, got)
		}
	}
}

func TestStringCharCountMethodCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn size(s: String) -> Int {
    s.charCount()
}

fn main() {
    println(size("가a"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_char_count_method.osty"})
	if err != nil {
		t.Fatalf("charCount method call errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_Chars",
		"@osty_rt_list_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("charCount method call missing %q:\n%s", want, got)
		}
	}
}

func TestStringGetMethodCallReturnsOptionalByte(t *testing.T) {
	file := parseLLVMGenFile(t, `fn firstOrSpace(s: String) -> Byte {
    s.get(0) ?? ' '.toInt().toByte()
}

fn main() {
    println(firstOrSpace("abc").toInt())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_get_method.osty"})
	if err != nil {
		t.Fatalf("get method call errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_Bytes",
		"@osty_rt_list_len",
		"@osty_rt_list_get_i8",
		"call ptr @osty.gc.alloc_v1(i64 1, i64 1,",
		"load i8, ptr",
		"phi i8",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("get method call missing %q:\n%s", want, got)
		}
	}
}

func TestStringGetMethodPreservesOptionalSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn hasFirst(s: String) -> Bool {
    s.get(0).isSome()
}

fn main() {
    println(hasFirst("abc"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_get_is_some.osty"})
	if err != nil {
		t.Fatalf("get().isSome() errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_Bytes",
		"@osty_rt_list_get_i8",
		"icmp ne ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("get().isSome() missing %q:\n%s", want, got)
		}
	}
}

func TestStringIndexOfMethodCallReturnsOptionalInt(t *testing.T) {
	file := parseLLVMGenFile(t, `fn posOrMinusOne(s: String, needle: String) -> Int {
    s.indexOf(needle) ?? -1
}

fn main() {
    println(posOrMinusOne("abc", "b"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_index_of_method.osty"})
	if err != nil {
		t.Fatalf("indexOf method call errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_IndexOf",
		"call ptr @osty.gc.alloc_v1(i64 1, i64 8,",
		"load i64, ptr",
		"phi i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("indexOf method call missing %q:\n%s", want, got)
		}
	}
}

func TestStringIndexOfMethodPreservesOptionalSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn hasNeedle(s: String, needle: String) -> Bool {
    s.indexOf(needle).isSome()
}

fn main() {
    println(hasNeedle("abc", "b"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_index_of_is_some.osty"})
	if err != nil {
		t.Fatalf("indexOf().isSome() errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_IndexOf",
		"icmp ne ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("indexOf().isSome() missing %q:\n%s", want, got)
		}
	}
}

func TestStringReplaceMethodCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn swap(s: String) -> String {
    s.replace("foo", "bar")
}

fn main() {
    println(swap("food"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_replace_method.osty"})
	if err != nil {
		t.Fatalf("replace method call errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_Replace",
		"declare ptr @osty_rt_strings_Replace(ptr, ptr, ptr)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("replace method call missing %q:\n%s", want, got)
		}
	}
}

func TestStringTrimStartMethodCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn dropLeft(s: String) -> String {
    s.trimStart()
}

fn main() {
    println(dropLeft("  hi"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_trim_start_method.osty"})
	if err != nil {
		t.Fatalf("trimStart method call errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_TrimStart",
		"declare ptr @osty_rt_strings_TrimStart(ptr)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("trimStart method call missing %q:\n%s", want, got)
		}
	}
}

func TestStringTrimEndMethodCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn dropRight(s: String) -> String {
    s.trimEnd()
}

fn main() {
    println(dropRight("hi  "))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_trim_end_method.osty"})
	if err != nil {
		t.Fatalf("trimEnd method call errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_TrimEnd",
		"declare ptr @osty_rt_strings_TrimEnd(ptr)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("trimEnd method call missing %q:\n%s", want, got)
		}
	}
}

func TestStringToUpperMethodCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn loud(s: String) -> String {
    s.toUpper()
}

fn main() {
    println(loud("Abc!"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_to_upper_method.osty"})
	if err != nil {
		t.Fatalf("toUpper method call errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_ToUpper",
		"declare ptr @osty_rt_strings_ToUpper(ptr)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("toUpper method call missing %q:\n%s", want, got)
		}
	}
}

func TestStringToLowerMethodCall(t *testing.T) {
	file := parseLLVMGenFile(t, `fn soft(s: String) -> String {
    s.toLower()
}

fn main() {
    println(soft("AbC!"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_to_lower_method.osty"})
	if err != nil {
		t.Fatalf("toLower method call errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_ToLower",
		"declare ptr @osty_rt_strings_ToLower(ptr)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("toLower method call missing %q:\n%s", want, got)
		}
	}
}

func TestStringToBytesMethodCarriesBytesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn size(s: String) -> Int {
    s.toBytes().len()
}

fn main() {
    println(size("abc"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_to_bytes_len.osty"})
	if err != nil {
		t.Fatalf("toBytes().len() errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_ToBytes",
		"@osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("toBytes().len() missing %q:\n%s", want, got)
		}
	}
}

func TestStringToBytesMethodPreservesBytesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn empty(s: String) -> Bool {
    s.toBytes().isEmpty()
}

fn main() {
    println(empty(""))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_to_bytes_is_empty.osty"})
	if err != nil {
		t.Fatalf("toBytes().isEmpty() errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_ToBytes",
		"@osty_rt_bytes_is_empty",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("toBytes().isEmpty() missing %q:\n%s", want, got)
		}
	}
}

func TestBytesGetMethodReturnsOptionalByte(t *testing.T) {
	file := parseLLVMGenFile(t, `fn second(b: Bytes) -> Byte? {
    b.get(1)
}

fn main() {
    let b = "abc".toBytes()
    println(second(b).isSome())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_get_method.osty"})
	if err != nil {
		t.Fatalf("Bytes.get() errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_len",
		"@osty_rt_bytes_get",
		"call ptr @osty.gc.alloc_v1(i64 1, i64 1,",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.get() missing %q:\n%s", want, got)
		}
	}
}

func TestStringToBytesGetMethodPreservesOptionalByteSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn hasFirst(s: String) -> Bool {
    s.toBytes().get(0).isSome()
}

fn main() {
    println(hasFirst("abc"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_to_bytes_get_is_some.osty"})
	if err != nil {
		t.Fatalf("toBytes().get().isSome() errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_ToBytes",
		"@osty_rt_bytes_get",
		"icmp ne ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("toBytes().get().isSome() missing %q:\n%s", want, got)
		}
	}
}

func TestBytesStaticLenDispatchesThroughRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `fn size() -> Int {
    Bytes.len(Bytes.from([b'A', b'B']))
}

fn main() {
    println(size())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_static_len.osty"})
	if err != nil {
		t.Fatalf("Bytes.len(Bytes.from(...)) errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.len(Bytes.from(...)) missing %q:\n%s", want, got)
		}
	}
}

func TestBytesStaticGetPreservesOptionalByteSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn hasSecond() -> Bool {
    Bytes.get(Bytes.from([b'A', b'B']), 1).isSome()
}

fn main() {
    println(hasSecond())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_static_get_is_some.osty"})
	if err != nil {
		t.Fatalf("Bytes.get(Bytes.from(...)).isSome() errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_get",
		"icmp ne ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.get(Bytes.from(...)).isSome() missing %q:\n%s", want, got)
		}
	}
}

func TestBytesFromMethodChainPreservesBytesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn hasSecond() -> Bool {
    Bytes.from([b'A', b'B']).get(1).isSome()
}

fn main() {
    println(hasSecond())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_from_method_chain.osty"})
	if err != nil {
		t.Fatalf("Bytes.from(...).get().isSome() errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_get",
		"icmp ne ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.from(...).get().isSome() missing %q:\n%s", want, got)
		}
	}
}

func TestBytesIndexOfMethodReturnsOptionalInt(t *testing.T) {
	file := parseLLVMGenFile(t, `fn posOrMinusOne() -> Int {
    Bytes.from([b'a', b'b', b'c']).indexOf(Bytes.from([b'b'])) ?? -1
}

fn main() {
    println(posOrMinusOne())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_index_of_method.osty"})
	if err != nil {
		t.Fatalf("Bytes.indexOf method call errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_index_of",
		"call ptr @osty.gc.alloc_v1(i64 1, i64 8,",
		"load i64, ptr",
		"phi i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.indexOf method call missing %q:\n%s", want, got)
		}
	}
}

func TestBytesContainsMethodRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `fn hasNeedle() -> Bool {
    Bytes.from([b'a', b'b', b'c']).contains(Bytes.from([b'b']))
}

fn main() {
    println(hasNeedle())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_contains_method.osty"})
	if err != nil {
		t.Fatalf("Bytes.contains method call errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_index_of",
		"icmp sge i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.contains method call missing %q:\n%s", want, got)
		}
	}
}

func TestBytesStaticContainsRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `fn hasNeedle() -> Bool {
    Bytes.contains(Bytes.from([b'a', b'b', b'c']), Bytes.from([b'b']))
}

fn main() {
    println(hasNeedle())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_static_contains.osty"})
	if err != nil {
		t.Fatalf("Bytes.contains(...) errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_index_of",
		"icmp sge i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.contains(...) missing %q:\n%s", want, got)
		}
	}
}

func TestBytesStartsWithMethodRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `fn hasPrefix() -> Bool {
    Bytes.from([b'a', b'b', b'c']).startsWith(Bytes.from([b'a', b'b']))
}

fn main() {
    println(hasPrefix())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_starts_with_method.osty"})
	if err != nil {
		t.Fatalf("Bytes.startsWith method call errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_index_of",
		"icmp eq i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.startsWith method call missing %q:\n%s", want, got)
		}
	}
}

func TestBytesStaticStartsWithRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `fn hasPrefix() -> Bool {
    Bytes.startsWith(Bytes.from([b'a', b'b', b'c']), Bytes.from([b'a', b'b']))
}

fn main() {
    println(hasPrefix())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_static_starts_with.osty"})
	if err != nil {
		t.Fatalf("Bytes.startsWith(...) errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_index_of",
		"icmp eq i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.startsWith(...) missing %q:\n%s", want, got)
		}
	}
}

func TestBytesEndsWithMethodRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `fn hasSuffix() -> Bool {
    Bytes.from([b'a', b'b', b'c']).endsWith(Bytes.from([b'b', b'c']))
}

fn main() {
    println(hasSuffix())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_ends_with_method.osty"})
	if err != nil {
		t.Fatalf("Bytes.endsWith method call errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_last_index_of",
		"@osty_rt_bytes_len",
		"icmp eq i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.endsWith method call missing %q:\n%s", want, got)
		}
	}
}

func TestBytesStaticEndsWithRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `fn hasSuffix() -> Bool {
    Bytes.endsWith(Bytes.from([b'a', b'b', b'c']), Bytes.from([b'b', b'c']))
}

fn main() {
    println(hasSuffix())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_static_ends_with.osty"})
	if err != nil {
		t.Fatalf("Bytes.endsWith(...) errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_last_index_of",
		"@osty_rt_bytes_len",
		"icmp eq i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.endsWith(...) missing %q:\n%s", want, got)
		}
	}
}

func TestBytesStaticIndexOfPreservesOptionalIntSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn hasNeedle() -> Bool {
    Bytes.indexOf(Bytes.from([b'a', b'b', b'c']), Bytes.from([b'b'])).isSome()
}

fn main() {
    println(hasNeedle())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_static_index_of_is_some.osty"})
	if err != nil {
		t.Fatalf("Bytes.indexOf(...).isSome() errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_index_of",
		"icmp ne ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.indexOf(...).isSome() missing %q:\n%s", want, got)
		}
	}
}

func TestBytesLastIndexOfMethodReturnsOptionalInt(t *testing.T) {
	file := parseLLVMGenFile(t, `fn posOrMinusOne() -> Int {
    Bytes.from([b'a', b'b', b'a']).lastIndexOf(Bytes.from([b'a'])) ?? -1
}

fn main() {
    println(posOrMinusOne())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_last_index_of_method.osty"})
	if err != nil {
		t.Fatalf("Bytes.lastIndexOf method call errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_last_index_of",
		"call ptr @osty.gc.alloc_v1(i64 1, i64 8,",
		"load i64, ptr",
		"phi i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.lastIndexOf method call missing %q:\n%s", want, got)
		}
	}
}

func TestBytesStaticLastIndexOfPreservesOptionalIntSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn hasNeedle() -> Bool {
    Bytes.lastIndexOf(Bytes.from([b'a', b'b', b'a']), Bytes.from([b'a'])).isSome()
}

fn main() {
    println(hasNeedle())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_static_last_index_of_is_some.osty"})
	if err != nil {
		t.Fatalf("Bytes.lastIndexOf(...).isSome() errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_last_index_of",
		"icmp ne ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.lastIndexOf(...).isSome() missing %q:\n%s", want, got)
		}
	}
}

func TestBytesSplitMethodCarriesBytesElementSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn firstChunkLen() -> Int {
    let parts = Bytes.from([b'a', b',', b'b']).split(Bytes.from([b',']))
    parts[0].len()
}

fn main() {
    println(firstChunkLen())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_split_method_len.osty"})
	if err != nil {
		t.Fatalf("Bytes.split method chain errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_split",
		"call ptr @osty_rt_list_get_ptr",
		"call i64 @osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.split method chain missing %q:\n%s", want, got)
		}
	}
}

func TestBytesJoinStaticHelperChainsIntoLen(t *testing.T) {
	file := parseLLVMGenFile(t, `fn joinedLen() -> Int {
    Bytes.join(Bytes.from([b',']), [Bytes.from([b'a']), Bytes.from([b'b'])]).len()
}

fn main() {
    println(joinedLen())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_join_static_len.osty"})
	if err != nil {
		t.Fatalf("Bytes.join static helper errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_join",
		"call ptr @osty_rt_bytes_join",
		"call i64 @osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.join static helper missing %q:\n%s", want, got)
		}
	}
}

func TestBytesConcatMethodChainPreservesBytesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn size() -> Int {
    Bytes.from([b'A']).concat(Bytes.from([b'B'])).len()
}

fn main() {
    println(size())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_concat_method_len.osty"})
	if err != nil {
		t.Fatalf("Bytes.concat method chain errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_concat",
		"@osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.concat method chain missing %q:\n%s", want, got)
		}
	}
}

func TestBytesStaticConcatCarriesBytesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn size() -> Int {
    Bytes.concat(Bytes.from([b'A']), Bytes.from([b'B'])).len()
}

fn main() {
    println(size())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_static_concat_len.osty"})
	if err != nil {
		t.Fatalf("Bytes.concat static helper errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_concat",
		"@osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.concat static helper missing %q:\n%s", want, got)
		}
	}
}

func TestBytesSliceMethodChainPreservesBytesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn size() -> Int {
    Bytes.from([b'A', b'B', b'C']).slice(1, 3).len()
}

fn main() {
    println(size())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_slice_method_len.osty"})
	if err != nil {
		t.Fatalf("Bytes.slice method chain errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_slice",
		"@osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.slice method chain missing %q:\n%s", want, got)
		}
	}
}

func TestBytesStaticSliceCarriesBytesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn size() -> Int {
    Bytes.slice(Bytes.from([b'A', b'B', b'C']), 1, 3).len()
}

fn main() {
    println(size())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_static_slice_len.osty"})
	if err != nil {
		t.Fatalf("Bytes.slice static helper errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_slice",
		"@osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.slice static helper missing %q:\n%s", want, got)
		}
	}
}

func TestBytesRepeatMethodChainPreservesBytesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn size() -> Int {
    Bytes.from([b'A']).repeat(3).len()
}

fn main() {
    println(size())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_repeat_method_len.osty"})
	if err != nil {
		t.Fatalf("Bytes.repeat method chain errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_repeat",
		"@osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.repeat method chain missing %q:\n%s", want, got)
		}
	}
}

func TestBytesStaticRepeatCarriesBytesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn size() -> Int {
    Bytes.repeat(Bytes.from([b'A']), 3).len()
}

fn main() {
    println(size())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_static_repeat_len.osty"})
	if err != nil {
		t.Fatalf("Bytes.repeat static helper errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_repeat",
		"@osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.repeat static helper missing %q:\n%s", want, got)
		}
	}
}

func TestBytesReplaceMethodChainPreservesBytesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn size() -> Int {
    Bytes.from([b'a', b'b', b'c']).replace(Bytes.from([b'b']), Bytes.from([b'Z'])).len()
}

fn main() {
    println(size())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_replace_method_len.osty"})
	if err != nil {
		t.Fatalf("Bytes.replace method chain errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_replace",
		"@osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.replace method chain missing %q:\n%s", want, got)
		}
	}
}

func TestBytesStaticReplaceAllCarriesBytesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn size() -> Int {
    Bytes.replaceAll(Bytes.from([b'a', b'b', b'a']), Bytes.from([b'a']), Bytes.from([b'Z'])).len()
}

fn main() {
    println(size())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_static_replace_all_len.osty"})
	if err != nil {
		t.Fatalf("Bytes.replaceAll static helper errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_replace_all",
		"@osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.replaceAll static helper missing %q:\n%s", want, got)
		}
	}
}

func TestBytesTrimLeftRightMethodChainPreservesBytesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn size() -> Int {
    Bytes.from([b' ', b' ', b'a', b' ', b' ']).trimLeft(Bytes.from([b' '])).trimRight(Bytes.from([b' '])).len()
}

fn main() {
    println(size())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_trim_left_right_method_len.osty"})
	if err != nil {
		t.Fatalf("Bytes.trimLeft/trimRight method chain errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_trim_left",
		"@osty_rt_bytes_trim_right",
		"@osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.trimLeft/trimRight method chain missing %q:\n%s", want, got)
		}
	}
}

func TestBytesStaticTrimCarriesBytesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn size() -> Int {
    Bytes.trim(Bytes.from([b'_', b'a', b'_']), Bytes.from([b'_'])).len()
}

fn main() {
    println(size())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_static_trim_len.osty"})
	if err != nil {
		t.Fatalf("Bytes.trim static helper errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_trim",
		"@osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.trim static helper missing %q:\n%s", want, got)
		}
	}
}

func TestBytesStaticTrimSpaceCarriesBytesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn size() -> Int {
    Bytes.trimSpace(Bytes.from([b' ', b'a', b' ', b'\n'])).len()
}

fn main() {
    println(size())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_static_trim_space_len.osty"})
	if err != nil {
		t.Fatalf("Bytes.trimSpace static helper errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_trim_space",
		"@osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.trimSpace static helper missing %q:\n%s", want, got)
		}
	}
}

func TestBytesToUpperMethodChainPreservesBytesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn size() -> Int {
    Bytes.from([b'a', b'B', b'!']).toUpper().len()
}

fn main() {
    println(size())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_to_upper_method_len.osty"})
	if err != nil {
		t.Fatalf("Bytes.toUpper method chain errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_to_upper",
		"@osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.toUpper method chain missing %q:\n%s", want, got)
		}
	}
}

func TestBytesStaticToLowerCarriesBytesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn size() -> Int {
    Bytes.toLower(Bytes.from([b'A', b'b', b'!'])).len()
}

fn main() {
    println(size())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_static_to_lower_len.osty"})
	if err != nil {
		t.Fatalf("Bytes.toLower static helper errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_to_lower",
		"@osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.toLower static helper missing %q:\n%s", want, got)
		}
	}
}

func TestBytesToHexMethodChainPreservesStringSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn hasPrefix() -> Bool {
    Bytes.from([b'A', b'B']).toHex().startsWith("41")
}

fn main() {
    println(hasPrefix())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_to_hex_method_starts_with.osty"})
	if err != nil {
		t.Fatalf("Bytes.toHex method chain errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_from_list",
		"@osty_rt_bytes_to_hex",
		"@osty_rt_strings_HasPrefix",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.toHex method chain missing %q:\n%s", want, got)
		}
	}
}

func TestBytesStaticFromHexQuestionCarriesBytesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn decodedLen() -> Result<Int, Error> {
    let b = Bytes.fromHex("4142")?
    Ok(b.len())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_from_hex_question.osty"})
	if err != nil {
		t.Fatalf("Bytes.fromHex()? errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_is_valid_hex",
		"@osty_rt_bytes_from_hex",
		"@osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.fromHex()? missing %q:\n%s", want, got)
		}
	}
}

func TestBytesToStringMethodQuestionPreservesStringSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn decodeLen() -> Result<Int, Error> {
    let s = "ab".toBytes().toString()?
    Ok(s.len())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_to_string_method_question.osty"})
	if err != nil {
		t.Fatalf("Bytes.toString()? method chain errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_is_valid_utf8",
		"@osty_rt_bytes_to_string",
		"@osty_rt_strings_ByteLen",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.toString()? method chain missing %q:\n%s", want, got)
		}
	}
}

func TestBytesStaticToStringQuestionPreservesStringSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn decodeLen() -> Result<Int, Error> {
    let s = Bytes.toString("ab".toBytes())?
    Ok(s.len())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/bytes_static_to_string_question.osty"})
	if err != nil {
		t.Fatalf("Bytes.toString(...)? static helper errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_bytes_is_valid_utf8",
		"@osty_rt_bytes_to_string",
		"@osty_rt_strings_ByteLen",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Bytes.toString(...)? static helper missing %q:\n%s", want, got)
		}
	}
}

// Chained method calls — `s.trimPrefix(a).trimSuffix(b).contains(n)` —
// must keep flowing through the String dispatcher rather than dropping
// the source-type hint after the first call. This locks in the
// `sourceType: String` propagation on trimPrefix / trimSuffix outputs.
func TestStringMethodChain(t *testing.T) {
	file := parseLLVMGenFile(t, `fn pipeline(s: String) -> Bool {
    s.trimPrefix("[").trimSuffix("]").contains(",")
}

fn main() {
    println(pipeline("[a,b]"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_method_chain.osty"})
	if err != nil {
		t.Fatalf("chained string methods errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_TrimPrefix",
		"@osty_rt_strings_TrimSuffix",
		"@osty_rt_strings_Contains",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("chain missing %q:\n%s", want, got)
		}
	}
}

func TestStringRepeatMethodChainPreservesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn hasEcho(s: String) -> Bool {
    s.repeat(2).contains(s)
}

fn main() {
    println(hasEcho("ab"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_repeat_chain.osty"})
	if err != nil {
		t.Fatalf("repeat-chain string methods errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_Repeat",
		"@osty_rt_strings_Contains",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("repeat-chain missing %q:\n%s", want, got)
		}
	}
}

func TestStringReplaceMethodChainPreservesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn hasBar(s: String) -> Bool {
    s.replace("foo", "bar").contains("bar")
}

fn main() {
    println(hasBar("food"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_replace_chain.osty"})
	if err != nil {
		t.Fatalf("replace-chain string methods errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_Replace",
		"@osty_rt_strings_Contains",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("replace-chain missing %q:\n%s", want, got)
		}
	}
}

func TestStringTrimStartEndMethodChainPreservesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn hasBody(s: String) -> Bool {
    s.trimStart().trimEnd().contains("body")
}

fn main() {
    println(hasBody("  body  "))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_trim_start_end_chain.osty"})
	if err != nil {
		t.Fatalf("trimStart/trimEnd chain errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_TrimStart",
		"@osty_rt_strings_TrimEnd",
		"@osty_rt_strings_Contains",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("trimStart/trimEnd chain missing %q:\n%s", want, got)
		}
	}
}

func TestStringToUpperMethodChainPreservesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn loudLen(s: String) -> Int {
    s.toUpper().len()
}

fn main() {
    println(loudLen("abc"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_to_upper_chain.osty"})
	if err != nil {
		t.Fatalf("String.toUpper().len() errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_ToUpper",
		"@osty_rt_strings_ByteLen",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("String.toUpper().len() missing %q:\n%s", want, got)
		}
	}
}

func TestStringToLowerMethodChainPreservesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn softHasA(s: String) -> Bool {
    s.toLower().contains("a")
}

fn main() {
    println(softHasA("ABC"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_to_lower_chain.osty"})
	if err != nil {
		t.Fatalf("String.toLower().contains() errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_ToLower",
		"@osty_rt_strings_Contains",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("String.toLower().contains() missing %q:\n%s", want, got)
		}
	}
}

func TestStringToIntMethodQuestionPreservesIntSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn parsedPlusOne() -> Result<Int, Error> {
    let n = "42".toInt()?
    Ok(n + 1)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_to_int_question.osty"})
	if err != nil {
		t.Fatalf("String.toInt()? errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_IsValidInt",
		"@osty_rt_strings_ToInt",
		"add i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("String.toInt()? missing %q:\n%s", want, got)
		}
	}
}

func TestStringToFloatMethodQuestionPreservesFloatSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn parsedPlusTwo() -> Result<Float, Error> {
    let f = "1.25".toFloat()?
    Ok(f + 2.0)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/string_to_float_question.osty"})
	if err != nil {
		t.Fatalf("String.toFloat()? errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_IsValidFloat",
		"@osty_rt_strings_ToFloat",
		"fadd double",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("String.toFloat()? missing %q:\n%s", want, got)
		}
	}
}
