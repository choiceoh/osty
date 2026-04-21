package llvmgen

import (
	"strings"
	"testing"
)

// List<T>.insert(index, value) — ordered insert via shift-right. The
// LSP self-host (`sorted.insert(i, tagged)` in lsp.osty) tripped
// LLVM015 [method_call_field] / "only println calls are supported"
// because the statement-form list method dispatcher only routed
// `push` and `pop`. This test locks the new osty_rt_list_insert_*
// runtime call.
func TestListInsertI64(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let mut xs: List<Int> = [10, 30]
    xs.insert(1, 20)
    println(xs.len())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/list_insert_i64.osty"})
	if err != nil {
		t.Fatalf("list.insert<Int> errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_list_insert_i64",
		"declare void @osty_rt_list_insert_i64(ptr, i64, i64)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("list.insert<Int> missing %q:\n%s", want, got)
		}
	}
}

// Aggregate-element insert — `list.insert(i, struct_value)` for a
// struct type that uses the aggregate (bytes-based) list ABI.
// LSP's `sorted.insert(i, tagged)` where `tagged: LspIndexedSymbolSortKey`
// is the canonical reproducer.
func TestListInsertAggregateStruct(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Point {
    x: Int,
    y: Int,
}

fn main() {
    let mut xs: List<Point> = [Point { x: 0, y: 0 }, Point { x: 2, y: 2 }]
    xs.insert(1, Point { x: 1, y: 1 })
    println(xs.len())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/list_insert_struct.osty"})
	if err != nil {
		t.Fatalf("list.insert<struct> errored: %v", err)
	}
	got := string(ir)
	// Either bytes_v1 (no pointer roots) or bytes_roots_v1 (with roots);
	// for an Int-only struct it'll be the v1 path.
	if !strings.Contains(got, "@osty_rt_list_insert_bytes_v1") &&
		!strings.Contains(got, "@osty_rt_list_insert_bytes_roots_v1") {
		t.Fatalf("list.insert<struct> did not invoke aggregate insert helper:\n%s", got)
	}
}

func TestListInsertString(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let mut xs: List<String> = ["a", "c"]
    xs.insert(1, "b")
    println(xs.len())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/list_insert_string.osty"})
	if err != nil {
		t.Fatalf("list.insert<String> errored: %v", err)
	}
	if got := string(ir); !strings.Contains(got, "@osty_rt_list_insert_ptr") {
		t.Fatalf("list.insert<String> did not invoke insert_ptr:\n%s", got)
	}
}
