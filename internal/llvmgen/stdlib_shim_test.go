package llvmgen

import (
	"strings"
	"testing"
)

func TestStdStringsCompareRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    let r = strings.compare("a", "b")
    println(r)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_strings_compare.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare i64 @osty_rt_strings_Compare(ptr, ptr)",
		"call i64 @osty_rt_strings_Compare",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdStringsHasPrefixRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    if strings.hasPrefix("osty", "ost") {
        println(1)
    } else {
        println(0)
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_strings_hasprefix.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare i1 @osty_rt_strings_HasPrefix(ptr, ptr)",
		"call i1 @osty_rt_strings_HasPrefix",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdStringsAliasRespectsRename(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as s

fn main() {
    let r = s.compare("a", "b")
    println(r)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_strings_alias.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	if got := string(ir); !strings.Contains(got, "call i64 @osty_rt_strings_Compare") {
		t.Fatalf("aliased std.strings.compare did not route to runtime:\n%s", got)
	}
}

func TestStdStringsJoinRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    let parts: List<String> = ["a", "b", "c"]
    let out = strings.join(parts, ", ")
    println(out)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_join.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare ptr @osty_rt_strings_Join(ptr, ptr)",
		"call ptr @osty_rt_strings_Join",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdStringsRepeatRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    let out = strings.repeat("ab", 3)
    println(out)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_repeat.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare ptr @osty_rt_strings_Repeat(ptr, i64)",
		"call ptr @osty_rt_strings_Repeat",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdStringsIndexOfRoutesToRuntimeAndOptionIntComposes(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    let at = strings.indexOf("abc", "b") ?? -1
    println(at)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_index_of.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare i64 @osty_rt_strings_IndexOf(ptr, ptr)",
		"call i64 @osty_rt_strings_IndexOf",
		"call ptr @osty.gc.alloc_v1(i64 1, i64 8,",
		"load i64, ptr",
		"phi i64",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdStringsReplaceRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    let out = strings.replace("food", "foo", "bar")
    println(out)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_replace.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare ptr @osty_rt_strings_Replace(ptr, ptr, ptr)",
		"call ptr @osty_rt_strings_Replace",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdStringsTrimStartRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    let out = strings.trimStart("  hi")
    println(out)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_trim_start.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare ptr @osty_rt_strings_TrimStart(ptr)",
		"call ptr @osty_rt_strings_TrimStart",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdStringsTrimEndRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    let out = strings.trimEnd("hi  ")
    println(out)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_trim_end.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare ptr @osty_rt_strings_TrimEnd(ptr)",
		"call ptr @osty_rt_strings_TrimEnd",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdStringsToUpperRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    let out = strings.toUpper("Abc!")
    println(out)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_to_upper.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare ptr @osty_rt_strings_ToUpper(ptr)",
		"call ptr @osty_rt_strings_ToUpper",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdStringsToLowerRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    let out = strings.toLower("AbC!")
    println(out)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_to_lower.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare ptr @osty_rt_strings_ToLower(ptr)",
		"call ptr @osty_rt_strings_ToLower",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdStringsToIntQuestionPreservesIntSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn parsedPlusOne() -> Result<Int, Error> {
    let n = strings.toInt("42")?
    Ok(n + 1)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_to_int_question.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare i1 @osty_rt_strings_IsValidInt(ptr)",
		"declare i64 @osty_rt_strings_ToInt(ptr)",
		"call i64 @osty_rt_strings_ToInt",
		"add i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdStringsToFloatQuestionPreservesFloatSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn parsedPlusTwo() -> Result<Float, Error> {
    let f = strings.toFloat("1.25")?
    Ok(f + 2.0)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_to_float_question.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare i1 @osty_rt_strings_IsValidFloat(ptr)",
		"declare double @osty_rt_strings_ToFloat(ptr)",
		"call double @osty_rt_strings_ToFloat",
		"fadd double",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdStringsToBytesRoutesToRuntimeAndBytesMethodsCompose(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    let n = strings.toBytes("abc").len()
    println(n)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_to_bytes.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare ptr @osty_rt_strings_ToBytes(ptr)",
		"call ptr @osty_rt_strings_ToBytes",
		"call i64 @osty_rt_bytes_len",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdStringsReplaceAllRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    let out = strings.replaceAll("a\r\nb\r", "\r", "")
    println(out)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_replace_all.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare ptr @osty_rt_strings_ReplaceAll(ptr, ptr, ptr)",
		"call ptr @osty_rt_strings_ReplaceAll",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdStringsCountRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    let total = strings.count("ababa", "aba")
    println(total)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_count.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare i64 @osty_rt_strings_Count(ptr, ptr)",
		"call i64 @osty_rt_strings_Count",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdStringsSliceRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    let piece = strings.slice("abcdef", 1, 4)
    println(piece)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_slice.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare ptr @osty_rt_strings_Slice(ptr, i64, i64)",
		"call ptr @osty_rt_strings_Slice",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestBareNoneSomeInPtrBackedOptional(t *testing.T) {
	file := parseLLVMGenFile(t, `fn pickNone() -> String? {
    return None
}

fn pickSome() -> String? {
    return Some("x")
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/option_none_some.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, "ret ptr null") {
		t.Fatalf("expected `ret ptr null` for bare None:\n%s", got)
	}
	if !strings.Contains(got, "ret ptr @.str0") {
		t.Fatalf("expected `ret ptr @.str0` for Some(literal):\n%s", got)
	}
}

func TestStdStringsSplitRoutesToRuntimeAndForInIterates(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    for part in strings.split("a,b,c", ",") {
        println(part)
    }
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_split_for.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_strings_Split(ptr, ptr)",
		"call ptr @osty_rt_strings_Split",
		"call i64 @osty_rt_list_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q (split-then-for-in plumbing broken):\n%s", want, got)
		}
	}
}

func TestStdStringsSplitBoundLocalElementKeepsStringSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    let parts = strings.split("a,b", ",")
    let first = parts[0]
    println(first.len())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_split_bound_local.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_strings_Split(ptr, ptr)",
		"call ptr @osty_rt_strings_Split",
		"declare i64 @osty_rt_strings_ByteLen(ptr)",
		"call i64 @osty_rt_strings_ByteLen",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q (split-bound local lost String sourceType):\n%s", want, got)
		}
	}
}

func TestStdStringsTrimSpaceRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    let s = strings.trimSpace("  hi  ")
    println(s)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_trim.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare ptr @osty_rt_strings_TrimSpace(ptr)",
		"call ptr @osty_rt_strings_TrimSpace",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdStringsHasSuffixRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    if strings.hasSuffix("osty", "sty") {
        println(1)
    } else {
        println(0)
    }
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_hassuffix.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare i1 @osty_rt_strings_HasSuffix(ptr, ptr)",
		"call i1 @osty_rt_strings_HasSuffix",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdStringsTrimPrefixSuffixRouteToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    let left = strings.trimPrefix("fn(Int)", "fn")
    let right = strings.trimSuffix(left, ")")
    println(right)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_trim_prefix_suffix.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare ptr @osty_rt_strings_TrimPrefix(ptr, ptr)",
		"declare ptr @osty_rt_strings_TrimSuffix(ptr, ptr)",
		"call ptr @osty_rt_strings_TrimPrefix",
		"call ptr @osty_rt_strings_TrimSuffix",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdStringsConcatRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    let out = strings.concat("a", "b")
    println(out)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_concat.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare ptr @osty_rt_strings_Concat(ptr, ptr)",
		"call ptr @osty_rt_strings_Concat",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdStringsContainsRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    if strings.contains("abcdef", "cd") {
        println(1)
    } else {
        println(0)
    }
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_strings_contains.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare i1 @osty_rt_strings_Contains(ptr, ptr)",
		"call i1 @osty_rt_strings_Contains",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdBytesContainsRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes

fn main() {
    if bytes.contains(bytes.from([b'a', b'b', b'c']), bytes.from([b'b'])) {
        println(1)
    } else {
        println(0)
    }
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_bytes_contains.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_from_list(ptr)",
		"declare i64 @osty_rt_bytes_index_of(ptr, ptr)",
		"call i64 @osty_rt_bytes_index_of",
		"icmp sge i64",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdBytesStartsWithRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes

fn main() {
    if bytes.startsWith(bytes.from([b'a', b'b', b'c']), bytes.from([b'a', b'b'])) {
        println(1)
    } else {
        println(0)
    }
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_bytes_starts_with.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_from_list(ptr)",
		"declare i64 @osty_rt_bytes_index_of(ptr, ptr)",
		"call i64 @osty_rt_bytes_index_of",
		"icmp eq i64",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdBytesEndsWithRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes

fn main() {
    if bytes.endsWith(bytes.from([b'a', b'b', b'c']), bytes.from([b'b', b'c'])) {
        println(1)
    } else {
        println(0)
    }
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_bytes_ends_with.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_from_list(ptr)",
		"declare i64 @osty_rt_bytes_last_index_of(ptr, ptr)",
		"declare i64 @osty_rt_bytes_len(ptr)",
		"call i64 @osty_rt_bytes_last_index_of",
		"icmp eq i64",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdBytesSplitRoutesToRuntimeAndForInIterates(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes

fn main() {
    for part in bytes.split(bytes.from([b'a', b',', b'b']), bytes.from([b','])) {
        println(part.len())
    }
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_bytes_split_for.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_split(ptr, ptr)",
		"call ptr @osty_rt_bytes_split",
		"call i64 @osty_rt_list_len",
		"call i64 @osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q (split-then-for-in plumbing broken):\n%s", want, got)
		}
	}
}

func TestStdBytesJoinRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes

fn main() {
    let n = bytes.join([bytes.from([b'a']), bytes.from([b'b'])], bytes.from([b','])).len()
    println(n)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_bytes_join_len.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_join(ptr, ptr)",
		"call ptr @osty_rt_bytes_join",
		"call i64 @osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestCollectStdStringsAliasesIgnoresRuntimeFFI(t *testing.T) {
	file := parseLLVMGenFile(t, `use runtime.strings as strings {
    fn HasPrefix(s: String, prefix: String) -> Bool
}

fn main() {
    if strings.HasPrefix("a", "a") {
        println(1)
    }
}
`)
	aliases := collectStdStringsAliases(file)
	if len(aliases) != 0 {
		t.Fatalf("expected no std.strings aliases from runtime FFI use, got %v", aliases)
	}
}

func TestStdBytesFromStringComposeWithLen(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes

fn main() {
    let n = bytes.fromString("abc").len()
    println(n)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_bytes_from_string_len.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_strings_ToBytes(ptr)",
		"call ptr @osty_rt_strings_ToBytes",
		"call i64 @osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdBytesAliasRespectsRenameAndOptionalByteComposes(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bin

fn main() {
    let ok = bin.get(bin.fromString("abc"), 1).isSome()
    println(ok)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_bytes_alias_get.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_ToBytes",
		"@osty_rt_bytes_get",
		"icmp ne ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdBytesFromListComposeWithLen(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes

fn main() {
    let n = bytes.from([b'A', b'B']).len()
    println(n)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_bytes_from_list_len.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_from_list(ptr)",
		"call ptr @osty_rt_bytes_from_list",
		"call i64 @osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdBytesIndexOfRoutesToRuntimeAndOptionIntComposes(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes

fn main() {
    let at = bytes.indexOf(bytes.from([b'a', b'b', b'c']), bytes.from([b'b'])) ?? -1
    println(at)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_bytes_index_of.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_from_list(ptr)",
		"declare i64 @osty_rt_bytes_index_of(ptr, ptr)",
		"call i64 @osty_rt_bytes_index_of",
		"call ptr @osty.gc.alloc_v1(i64 1, i64 8,",
		"load i64, ptr",
		"phi i64",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdBytesLastIndexOfRoutesToRuntimeAndOptionIntComposes(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes

fn main() {
    let at = bytes.lastIndexOf(bytes.from([b'a', b'b', b'a']), bytes.from([b'a'])) ?? -1
    println(at)
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/std_bytes_last_index_of.osty"})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_from_list(ptr)",
		"declare i64 @osty_rt_bytes_last_index_of(ptr, ptr)",
		"call i64 @osty_rt_bytes_last_index_of",
		"call ptr @osty.gc.alloc_v1(i64 1, i64 8,",
		"load i64, ptr",
		"phi i64",
	} {
		if !strings.Contains(string(ir), want) {
			t.Fatalf("generated IR missing %q:\n%s", want, string(ir))
		}
	}
}

func TestStdBytesConcatComposeWithLen(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes

fn main() {
    let n = bytes.concat(bytes.from([b'A']), bytes.from([b'B'])).len()
    println(n)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_bytes_concat_len.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_from_list(ptr)",
		"declare ptr @osty_rt_bytes_concat(ptr, ptr)",
		"call ptr @osty_rt_bytes_concat",
		"call i64 @osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdBytesSliceComposeWithLen(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes

fn main() {
    let n = bytes.slice(bytes.from([b'A', b'B', b'C']), 1, 3).len()
    println(n)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_bytes_slice_len.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_from_list(ptr)",
		"declare ptr @osty_rt_bytes_slice(ptr, i64, i64)",
		"call ptr @osty_rt_bytes_slice",
		"call i64 @osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdBytesRepeatComposeWithLen(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes

fn main() {
    let n = bytes.repeat(bytes.from([b'A']), 3).len()
    println(n)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_bytes_repeat_len.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_from_list(ptr)",
		"declare ptr @osty_rt_bytes_repeat(ptr, i64)",
		"call ptr @osty_rt_bytes_repeat",
		"call i64 @osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdBytesReplaceRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes

fn main() {
    let n = bytes.replace(bytes.from([b'a', b'b', b'c']), bytes.from([b'b']), bytes.from([b'Z'])).len()
    println(n)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_bytes_replace_len.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_from_list(ptr)",
		"declare ptr @osty_rt_bytes_replace(ptr, ptr, ptr)",
		"call ptr @osty_rt_bytes_replace",
		"call i64 @osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdBytesReplaceAllRoutesToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes

fn main() {
    let n = bytes.replaceAll(bytes.from([b'a', b'b', b'a']), bytes.from([b'a']), bytes.from([b'Z'])).len()
    println(n)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_bytes_replace_all_len.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_from_list(ptr)",
		"declare ptr @osty_rt_bytes_replace_all(ptr, ptr, ptr)",
		"call ptr @osty_rt_bytes_replace_all",
		"call i64 @osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdBytesTrimLeftRightRouteToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes

fn main() {
    let n = bytes.trimRight(bytes.trimLeft(bytes.from([b' ', b'a', b' ']), bytes.from([b' '])), bytes.from([b' '])).len()
    println(n)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_bytes_trim_left_right_len.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_from_list(ptr)",
		"declare ptr @osty_rt_bytes_trim_left(ptr, ptr)",
		"declare ptr @osty_rt_bytes_trim_right(ptr, ptr)",
		"call ptr @osty_rt_bytes_trim_left",
		"call ptr @osty_rt_bytes_trim_right",
		"call i64 @osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdBytesTrimAndTrimSpaceRouteToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes

fn main() {
    let a = bytes.trim(bytes.from([b'_', b'a', b'_']), bytes.from([b'_'])).len()
    let b = bytes.trimSpace(bytes.from([b' ', b'a', b'\n'])).len()
    println(a + b)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_bytes_trim_trim_space_len.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_trim(ptr, ptr)",
		"declare ptr @osty_rt_bytes_trim_space(ptr)",
		"call ptr @osty_rt_bytes_trim",
		"call ptr @osty_rt_bytes_trim_space",
		"call i64 @osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdBytesToUpperLowerRouteToRuntime(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes

fn main() {
    let a = bytes.toUpper(bytes.from([b'a', b'B'])).len()
    let b = bytes.toLower(bytes.from([b'A', b'b'])).len()
    println(a + b)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_bytes_to_upper_lower_len.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_to_upper(ptr)",
		"declare ptr @osty_rt_bytes_to_lower(ptr)",
		"call ptr @osty_rt_bytes_to_upper",
		"call ptr @osty_rt_bytes_to_lower",
		"call i64 @osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdBytesToHexRoutesToRuntimeAndStringMethodsCompose(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes

fn main() {
    let n = bytes.toHex(bytes.from([b'A', b'B'])).len()
    println(n)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_bytes_to_hex_len.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_to_hex(ptr)",
		"call ptr @osty_rt_bytes_to_hex",
		"call i64 @osty_rt_strings_ByteLen",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdBytesFromHexRoutesToRuntimeAndQuestionPreservesBytesSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes

fn decodedLen() -> Result<Int, Error> {
    let b = bytes.fromHex("4142")?
    Ok(b.len())
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_bytes_from_hex_question.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare i1 @osty_rt_bytes_is_valid_hex(ptr)",
		"declare ptr @osty_rt_bytes_from_hex(ptr)",
		"call i1 @osty_rt_bytes_is_valid_hex",
		"call ptr @osty_rt_bytes_from_hex",
		"call i64 @osty_rt_bytes_len",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStdBytesToStringQuestionPreservesStringSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.bytes as bytes

fn decodeLen() -> Result<Int, Error> {
    let s = bytes.toString(bytes.from([b'a', b'b']))?
    Ok(s.len())
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_bytes_to_string_question.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_bytes_from_list(ptr)",
		"declare i1 @osty_rt_bytes_is_valid_utf8(ptr)",
		"declare ptr @osty_rt_bytes_to_string(ptr)",
		"call ptr @osty_rt_bytes_to_string",
		"call i64 @osty_rt_strings_ByteLen",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestCollectStdBytesAliasesIgnoresRuntimeFFI(t *testing.T) {
	file := parseLLVMGenFile(t, `use runtime.bytes as bytes {
    fn Len(b: Bytes) -> Int
}

fn main() {
    println(bytes.Len("abc".toBytes()))
}
`)
	aliases := collectStdBytesAliases(file)
	if len(aliases) != 0 {
		t.Fatalf("expected no std.bytes aliases from runtime FFI use, got %v", aliases)
	}
}

// TestStdStringsJoinNestedInListLiteral pins the narrow shape the
// formatter_ast.osty probe walled on: a String-typed list literal with
// an alias-qualified `strings.join(...)` call as a middle element. The
// broader regression bundle lives in
// field_call_dispatch_test.go:TestGenerateListLiteralOfStringsPropagatesSourceType;
// this narrower test stays so an alias-stdlib-only regression would
// fail here first with a smaller repro.
func TestStdStringsJoinNestedInListLiteral(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.strings as strings

fn main() {
    let parts: List<String> = ["a", "b"]
    let out = strings.join(["<", strings.join(parts, ", "), ">"], "")
    println(out)
}
`)
	_, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_strings_nested_join.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
}

// TestStringLetMutInferredSourceType pins the let-mut-with-bare-""
// shape: `let mut line = "" ; [tag(), line, tag()]`. Paired with
// TestStdStringsJoinNestedInListLiteral and
// TestIfExprStringArmsAgreeSourceType so each of the three source-type
// propagation paths closed by PR #438 has a dedicated narrow repro.
func TestStringLetMutInferredSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn tag() -> String { "<t>" }

fn main() {
    let mut line = ""
    line = tag()
    let parts = [tag(), line, tag()]
    println(parts.len())
}
`)
	_, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/let_mut_string_source_type.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
}

// TestIfExprStringArmsAgreeSourceType pins the if-expression phi
// shape: `let op = if flag { "..=" } else { ".." }` used inside a
// later String-typed list literal. Both branches agree on String so
// mergeContainerMetadata / sameSourceType must preserve the tag.
func TestIfExprStringArmsAgreeSourceType(t *testing.T) {
	file := parseLLVMGenFile(t, `fn tag() -> String { "<t>" }

fn main() {
    let flag = true
    let op = if flag { "..=" } else { ".." }
    let parts = [tag(), op, tag()]
    println(parts.len())
}
`)
	_, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/if_expr_string_arms.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
}
