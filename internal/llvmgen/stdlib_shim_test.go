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
