package llvmgen

import (
	"strings"
	"testing"
)

// Map<K,V>.clear() — truncate to zero entries. The GC example's
// collector reset paths (`self.forwarding.clear()` on a
// Map<Int, GcForwardingEntry> at examples/gc/lib.osty:2506 / :4510)
// walled the same way buf.clear() did on ty.osty — statement-form
// map dispatcher only routed insert/remove/update/retainIf. Locks in
// the new osty_rt_map_clear runtime call.
func TestMapClearI64Int(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let mut m: Map<Int, Int> = {:}
    m.insert(1, 2)
    m.clear()
    println(m.len())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/map_clear_i64_i64.osty"})
	if err != nil {
		t.Fatalf("Map<Int,Int>.clear errored: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"@osty_rt_map_clear",
		"declare void @osty_rt_map_clear(ptr)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Map.clear missing %q:\n%s", want, got)
		}
	}
}

func TestMapClearStringPtr(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let mut m: Map<String, Int> = {:}
    m.insert("a", 1)
    m.clear()
    println(m.len())
}
`)
	ir, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/map_clear_string_int.osty"})
	if err != nil {
		t.Fatalf("Map<String,Int>.clear errored: %v", err)
	}
	if got := string(ir); !strings.Contains(got, "@osty_rt_map_clear") {
		t.Fatalf("Map<String,Int>.clear did not invoke osty_rt_map_clear:\n%s", got)
	}
}
