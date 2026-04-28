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

func TestGenerateTuplePatternDirectlyInListFor(t *testing.T) {
	ir := generateLLVMForTest(t, `fn main() {
    let kinds: List<(String, Int)> = [
        ("Int", 1),
        ("Byte", 2),
    ]
    for (ownerName, ty) in kinds {
        println(ownerName)
        println(ty)
    }
}
`)

	assertGeneratedIRContains(t, ir, "%Tuple.ptr.i64 = type { ptr, i64 }")
	assertGeneratedIRContains(t, ir, "call void @osty_rt_list_get_bytes_v1(")
	assertGeneratedIRContains(t, ir, "extractvalue %Tuple.ptr.i64")
	assertGeneratedIRContains(t, ir, "call void @osty_rt_io_write")
}

func TestGenerateTupleTypeDefFromFunctionSignature(t *testing.T) {
	ir := generateLLVMForTest(t, `fn sum(pair: (Int, Int)) -> Int {
    let alias: (Int, Int) = pair
    let (a, b) = alias
    a + b
}
`)

	assertGeneratedIRContains(t, ir, "%Tuple.i64.i64 = type { i64, i64 }")
	assertGeneratedIRContains(t, ir, "define i64 @sum(%Tuple.i64.i64 %pair)")
	assertGeneratedIRContains(t, ir, "extractvalue %Tuple.i64.i64")
}
