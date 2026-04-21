package llvmgen

import (
	"strings"
	"testing"
)

// List<T>.clear() — truncate to length 0. The toolchain native-merged
// probe first-walled on `buf.clear()` in ty.osty's generic-arg splitter
// (where rebinding `buf = []` would've lost bootstrap-gen's element
// inference on Windows regen) with LLVM015 [method_call_field] /
// "only println calls are supported" because the statement-form list
// method dispatcher only routed push/pop/insert. This test locks the
// new osty_rt_list_clear runtime call so the regression re-surfaces
// immediately if a future refactor drops the dispatch.
func TestListClearPtrElems(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let mut xs: List<String> = ["a", "b", "c"]
    xs.clear()
    println(xs.len())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/list_clear_string.osty"})
	if err != nil {
		t.Fatalf("list.clear<String> errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_list_clear",
		"declare void @osty_rt_list_clear(ptr)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("list.clear<String> missing %q:\n%s", want, got)
		}
	}
}

func TestListClearI64(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let mut xs: List<Int> = [1, 2, 3]
    xs.clear()
    println(xs.len())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/list_clear_i64.osty"})
	if err != nil {
		t.Fatalf("list.clear<Int> errored: %v", err)
	}
	if got := string(ir); !strings.Contains(got, "@osty_rt_list_clear") {
		t.Fatalf("list.clear<Int> did not invoke osty_rt_list_clear:\n%s", got)
	}
}
