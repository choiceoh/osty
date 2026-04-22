package llvmgen

import (
	"strings"
	"testing"
)

func TestIndexedFieldAssignOnListElement(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Instr {
    hasDest: Bool,
    dest: Int,
}

fn main() {
    let mut instrs: List<Instr> = [Instr { hasDest: true, dest: 1 }]
    instrs[0].hasDest = false
    if instrs[0].hasDest {
        println(1)
    } else {
        println(0)
    }
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/indexed_field_assign.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"call void @osty_rt_list_get_bytes",
		"call void @osty_rt_list_set_bytes",
		"insertvalue %Instr",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
