package llvmgen

import (
	"strings"
	"testing"
)

func TestCompoundAssignIdentIntArithmetic(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let mut n = 100
    n += 5
    n -= 3
    n *= 2
    n /= 4
    n %= 7
    println(n)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/compound_assign_ident.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"add i64",
		"sub i64",
		"mul i64",
		"sdiv i64",
		"srem i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestCompoundAssignFieldThroughSelf(t *testing.T) {
	file := parseLLVMGenFile(t, `struct Counter {
    value: Int,

    fn bump(mut self, by: Int) {
        self.value += by
    }
}

fn main() {
    let mut c = Counter { value: 10 }
    c.bump(5)
    println(c.value)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/compound_assign_field.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	if !strings.Contains(got, "add i64") {
		t.Fatalf("generated IR missing field-compound add i64:\n%s", got)
	}
	if !strings.Contains(got, "insertvalue") {
		t.Fatalf("generated IR missing field insertvalue:\n%s", got)
	}
}

func TestCompoundAssignStringConcat(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let mut msg: String = "hello"
    msg += ", "
    msg += "world"
    println(msg)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/compound_assign_string.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}
	got := string(ir)
	if !strings.Contains(got, "osty_rt_strings_Concat") {
		t.Fatalf("generated IR missing string concat runtime call:\n%s", got)
	}
}

func TestCompoundAssignIndexTargetIntArithmetic(t *testing.T) {
	file := parseLLVMGenFile(t, `fn main() {
    let mut xs = [1, 2, 3]
    xs[0] += 10
    xs[1] -= 1
    xs[2] *= 3
    println(xs[0])
    println(xs[1])
    println(xs[2])
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/compound_assign_index.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"add i64",
		"sub i64",
		"mul i64",
		"osty_rt_list_get_i64",
		"osty_rt_list_set_i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
