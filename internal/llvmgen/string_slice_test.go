package llvmgen

import (
	"strings"
	"testing"
)

// String slice syntax — `s[start..end]`, `s[start..=end]`, `s[..end]`,
// `s[start..]` — lowers to `osty_rt_strings_Slice`. Without this the
// self-host parser.osty `raw[1..n - 1]` tripped LLVM013 in the index
// path because RangeExpr was never a value-position expression.
func TestStringSliceHalfOpenRange(t *testing.T) {
	file := parseLLVMGenFile(t, `fn trimQuotes(raw: String) -> String {
    let n = raw.len()
    raw[1..n - 1]
}

fn main() {
    println(trimQuotes("\"ab\""))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/slice_half_open.osty"})
	if err != nil {
		t.Fatalf("half-open slice errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_Slice",
		"declare ptr @osty_rt_strings_Slice(ptr, i64, i64)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestStringSliceInclusiveRange(t *testing.T) {
	file := parseLLVMGenFile(t, `fn firstTwo(s: String) -> String {
    s[0..=1]
}

fn main() {
    println(firstTwo("hello"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/slice_incl.osty"})
	if err != nil {
		t.Fatalf("inclusive slice errored: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, "@osty_rt_strings_Slice") {
		t.Fatalf("inclusive slice did not call runtime Slice:\n%s", got)
	}
	// Inclusive range adds 1 to the upper bound before the Slice call
	if !strings.Contains(got, "add i64") {
		t.Fatalf("inclusive slice missing `add i64` upper-bound adjustment:\n%s", got)
	}
}

func TestStringSliceOpenHigh(t *testing.T) {
	file := parseLLVMGenFile(t, `fn dropFirst(s: String) -> String {
    s[1..]
}

fn main() {
    println(dropFirst("xyz"))
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/slice_open_high.osty"})
	if err != nil {
		t.Fatalf("open-high slice errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_strings_Slice",
		"@osty_rt_strings_ByteLen",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("open-high slice missing %q:\n%s", want, got)
		}
	}
}
