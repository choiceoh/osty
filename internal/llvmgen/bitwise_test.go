package llvmgen

import (
	"strings"
	"testing"
)

func TestGenerateBitwiseIntOps(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    println(~-43)
    println((1 << 5) | (1 << 3) | 2)
    println((255 >> 2) ^ 21)
    println(58 & 43)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/bitwise_int_ops.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"xor i64",
		"shl i64",
		"or i64",
		"ashr i64",
		"and i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
