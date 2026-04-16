package llvmgen

import (
	"strings"
	"testing"
)

func TestGenerateLetStructPatternDestructuring(t *testing.T) {
	file := parseLLVMGenFile(t, `use runtime.strings as strings {
    fn Split(s: String, sep: String) -> List<String>
}

struct Pair {
    first: Int
    second: Int
}

struct Bucket {
    pair: Pair
    items: List<String>
}

fn main() {
    let bucket @ Bucket {
        pair: Pair { first, second },
        items,
    } = Bucket {
        pair: Pair { first: 1, second: 2 },
        items: strings.Split("pear,apple", ","),
    }
    println(first)
    println(second)
    println(items.sorted()[0])
    println(bucket.pair.first)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/let_struct_pattern.osty",
	})
	if err != nil {
		t.Fatalf("Generate returned error: %v", err)
	}

	got := string(ir)
	for _, want := range []string{
		"extractvalue %Bucket",
		"extractvalue %Pair",
		"call ptr @osty_rt_list_sorted_string",
		"call i32 (ptr, ...) @printf",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}
