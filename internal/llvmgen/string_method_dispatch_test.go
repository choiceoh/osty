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
