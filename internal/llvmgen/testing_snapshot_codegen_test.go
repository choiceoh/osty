package llvmgen

import (
	"strings"
	"testing"
)

// TestTestingSnapshotLowersToRuntimeHelper locks in that
// `testing.snapshot(name, output)` is intercepted by the LLVM
// dispatcher and lowered to a call into `osty_rt_test_snapshot`. The
// generator also hard-codes the test source path as a string literal
// so the runtime can locate the golden file without consulting the
// process working directory. Without the intercept the call would
// fall through to the "testing.X is not supported" diagnostic.
func TestTestingSnapshotLowersToRuntimeHelper(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.testing as testing

fn main() {
    testing.snapshot("golden", "hello\nworld\n")
}
`)
	src := `use std.testing as testing

fn main() {
    testing.snapshot("golden", "hello\nworld\n")
}
`
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/testing_snapshot_probe.osty",
		Source:      []byte(src),
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare void @osty_rt_test_snapshot(ptr, ptr, ptr)",
		"call void @osty_rt_test_snapshot(",
		"/tmp/testing_snapshot_probe.osty",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestTestingAssertEqStringsEmitsStructuralDiff confirms that an
// assertEq comparison between two String values now calls
// osty_rt_strings_DiffLines on the failure path so the emitted
// failure message includes a line-level diff. Non-string assertEq
// (Int/Int, Bool/Bool) must NOT pay the diff cost — the runtime
// intrinsic is only declared when at least one call site needs it.
func TestTestingAssertEqStringsEmitsStructuralDiff(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.testing as testing

fn main() {
    let a = "alpha\nbeta\n"
    let b = "alpha\nBETA\n"
    testing.assertEq(a, b)
}
`)
	src := `use std.testing as testing

fn main() {
    let a = "alpha\nbeta\n"
    let b = "alpha\nBETA\n"
    testing.assertEq(a, b)
}
`
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/testing_asserteq_strings.osty",
		Source:      []byte(src),
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_strings_DiffLines(ptr, ptr)",
		"call ptr @osty_rt_strings_DiffLines(",
		"diff (- left, + right):",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

// TestTestingAssertEqListIntEmitsStructuralDiff confirms the diff
// path now extends beyond String/String: two List<Int> values route
// through osty_rt_list_primitive_to_string first (elem_kind=1) and
// then feed the stringified forms to osty_rt_strings_DiffLines. Past
// releases stopped at `= [...]` source-text-only because there was
// no runtime formatter for lists; the helper closes that gap.
func TestTestingAssertEqListIntEmitsStructuralDiff(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.testing as testing

fn main() {
    let a: List<Int> = [1, 2, 3]
    let b: List<Int> = [1, 99, 3]
    testing.assertEq(a, b)
}
`)
	src := `use std.testing as testing

fn main() {
    let a: List<Int> = [1, 2, 3]
    let b: List<Int> = [1, 99, 3]
    testing.assertEq(a, b)
}
`
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/testing_asserteq_list_int.osty",
		Source:      []byte(src),
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_list_primitive_to_string(ptr, i64)",
		"call ptr @osty_rt_list_primitive_to_string(",
		"declare ptr @osty_rt_strings_DiffLines(ptr, ptr)",
		"call ptr @osty_rt_strings_DiffLines(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("List<Int> assertEq IR missing %q:\n%s", want, got)
		}
	}
}

func TestTestingAssertEqIntegersSkipsDiff(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.testing as testing

fn main() {
    testing.assertEq(1, 2)
}
`)
	src := `use std.testing as testing

fn main() {
    testing.assertEq(1, 2)
}
`
	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/testing_asserteq_ints.osty",
		Source:      []byte(src),
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	if strings.Contains(got, "osty_rt_strings_DiffLines") {
		t.Fatalf("Int/Int assertEq should not pull in DiffLines; IR leaked it:\n%s", got)
	}
}
