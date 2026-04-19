package selfhost

import "testing"

func TestCheckSourceStructuredRecordsTypedExprCoverage(t *testing.T) {
	src := []byte(`fn main() {
    let x = 1
    let y = x + 2
    y
}
`)

	checked := CheckSourceStructured(src)
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0", checked.Summary.Errors)
	}

	kinds := map[string]int{}
	for _, node := range checked.TypedNodes {
		kinds[node.Kind]++
	}

	for _, want := range []string{"Ident", "Binary", "IntLit"} {
		if kinds[want] == 0 {
			t.Fatalf("typed node kinds = %#v, want %q to be recorded", kinds, want)
		}
	}
}
