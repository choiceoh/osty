package gen_test

import (
	"strings"
	"testing"
)

func TestIterTranspile(t *testing.T) {
	src := "use std.iter\n\nfn main() {\n" +
		"    let xs = iter.range(0, 5)\n" +
		"    let doubled = xs.map(|x| x * 2)\n" +
		"    let big = doubled.filter(|x| x > 4)\n" +
		"    let first3 = big.takeWhile(|x| x < 100)\n" +
		"    let total = first3.fold(0, |acc, x| acc + x)\n" +
		"    println(\"{total}\")\n}\n"

	goSrc, err := transpileWithStdlib(t, src)
	if err != nil {
		t.Fatalf("transpile range/map/filter/takeWhile/fold: %v\n%s", err, goSrc)
	}

	src2 := "use std.iter\n\nfn main() {\n" +
		"    let xs: List<Int> = [1, 2, 3]\n" +
		"    let it = iter.from(xs)\n" +
		"    let empty = iter.empty::<Int>()\n" +
		"    println(\"{it.count()}\")\n" +
		"    println(\"{empty.count()}\")\n}\n"

	goSrc2, err2 := transpileWithStdlib(t, src2)
	if err2 != nil {
		t.Fatalf("transpile from/empty: %v\n%s", err2, goSrc2)
	}

	src3 := "use std.iter\n\nfn main() {\n" +
		"    let left = iter.range(0, 5).skip(2)\n" +
		"    let right = iter.from([9, 10])\n" +
		"    let out = left.chain(right).toList()\n" +
		"    println(\"{out[0]} {out[2]} {out[4]}\")\n}\n"

	goSrc3, err3 := transpileWithStdlib(t, src3)
	if err3 != nil {
		t.Fatalf("transpile skip/chain: %v\n%s", err3, goSrc3)
	}
	out3 := strings.TrimSpace(runGo(t, goSrc3))
	if out3 != "2 4 10" {
		t.Fatalf("skip/chain output = %q\n--- source ---\n%s", out3, goSrc3)
	}

	for _, want := range []string{"make([]int", "for"} {
		if !strings.Contains(string(goSrc), want) {
			t.Errorf("expected %q in generated Go:\n%s", want, goSrc)
		}
	}
	t.Logf("range output:\n%s", goSrc)
	t.Logf("from/empty output:\n%s", goSrc2)
}
