package llvmgen

import (
	"strings"
	"testing"
)

// TestStructParamScalarFieldAssignBugIsPinned pins the post-fix IR
// shape for struct-param scalar-field assignment. Skipped today
// because the codegen still passes struct params by value (callee
// mutates a local copy, caller never sees writes). The current
// buggy shape is already locked by
// `TestNativeOwnedModuleEntryStructFieldAssignParam` in
// ir_native_entry_test.go — when the chosen Phase-7 Slice 3 fix
// lands (codegen pass-by-pointer / `mut p:` migration / functional
// rewrite — see `project_selfhost_struct_pass_by_value` memory
// note), unskip this test and delete the by-value assertions in
// the older test together.
func TestStructParamScalarFieldAssignBugIsPinned(t *testing.T) {
	t.Skip("post-fix IR shape; unskip when struct-param pass-by-pointer codegen lands. See project_selfhost_struct_pass_by_value memory note.")

	src := `struct Counter { pub n: Int }

fn bump(c: Counter) {
    c.n = c.n + 1
}

fn main() {
    let mut c = Counter { n: 0 }
    bump(c)
    println("{c.n}")
}
`
	mod := lowerSrcLLVM(t, src)
	ir, err := GenerateModule(mod, Options{PackageName: "main", SourcePath: "/tmp/struct_param_mut.osty"})
	if err != nil {
		t.Fatalf("GenerateModule returned error: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"define void @bump(ptr",
		"call void @bump(ptr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("post-fix IR missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{
		"define void @bump(%Counter",
		"call void @bump(%Counter",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("post-fix IR still contains the by-value pattern %q:\n%s", unwanted, got)
		}
	}
}
