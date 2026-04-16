package llvmgen

import "testing"

func TestGenerateTupleTableDrivenLoop(t *testing.T) {
	ir := generateLLVMForTest(t, `fn clamp(v: Int, lo: Int, hi: Int) -> Int {
    if v < lo { lo } else if v > hi { hi } else { v }
}

fn main() {
    let cases = [
        (5, 0, 10, 5),
        (-1, 0, 10, 0),
        (99, 0, 10, 10),
    ]
    for c in cases {
        let (v, lo, hi, expected) = c
        println(clamp(v, lo, hi) - expected)
    }
}
`)

	assertGeneratedIRContains(t, ir, "%Tuple.i64.i64.i64.i64 = type { i64, i64, i64, i64 }")
	assertGeneratedIRContains(t, ir, "insertvalue %Tuple.i64.i64.i64.i64")
	assertGeneratedIRContains(t, ir, "call void @osty_rt_list_push_bytes_v1(")
	assertGeneratedIRContains(t, ir, "call void @osty_rt_list_get_bytes_v1(")
	assertGeneratedIRContains(t, ir, "extractvalue %Tuple.i64.i64.i64.i64")
}
