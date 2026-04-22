package llvmgen

import (
	"strings"
	"testing"
)

func TestFieldAssignMaterializesNonAddressableLocalBinding(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Decl {
    name: String,
    arity: Int,
}

fn rewrite(decl: Decl) -> Int {
    decl.arity = 7
    decl.arity
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "core",
		SourcePath:  "/tmp/non_addressable_field_assign.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"alloca %Decl",
		"insertvalue %Decl",
		"store %Decl",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
