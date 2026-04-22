package llvmgen

import (
	"testing"

	"github.com/osty/osty/internal/parser"
)

// TestMatchArmEmptyListInheritsSiblingHint verifies the sibling-arm list
// element hint propagation added to emitMatchExprValue. Before the fix,
// a match expression like
//
//	let xs = match tag {
//	    Tuple -> typedList,
//	    _     -> [],
//	}
//
// walled on LLVM013 "empty list literal requires an explicit List<T>
// type" because emitMatchArmBodyValue emitted the `_ -> []` arm with no
// hint. The fix pre-scans arms in emitMatchExprValue, pushes the
// inferred List<T> element onto g.matchArmListHints, and the empty-arm
// body reads it back off the stack.
//
// This is the exact shape that toolchain/mir_lower.osty:2369 produces
// and that tripped the merged-toolchain AST probe on main at
// 4eccfe8.
func TestMatchArmEmptyListInheritsSiblingHint(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "int_list_sibling",
			src: `pub struct Holder { pub xs: List<Int> }
pub fn pick(tag: Int, src: Holder) -> List<Int> {
    match tag {
        0 -> src.xs,
        _ -> [],
    }
}
fn main() {}
`,
		},
		{
			name: "string_list_sibling",
			src: `pub struct Holder { pub xs: List<String> }
pub fn pick(tag: Int, src: Holder) -> List<String> {
    match tag {
        0 -> src.xs,
        _ -> [],
    }
}
fn main() {}
`,
		},
		{
			name: "let_binding_no_annotation",
			src: `pub struct Holder { pub xs: List<Int> }
pub fn pick(tag: Int, src: Holder) -> Int {
    let xs = match tag {
        0 -> src.xs,
        _ -> [],
    }
    xs.len()
}
fn main() {}
`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			file, diags := parser.ParseDiagnostics([]byte(c.src))
			if len(diags) > 0 {
				t.Fatalf("parse: %v", diags[0])
			}
			_, err := generateFromAST(file, Options{PackageName: "main", SourcePath: "/tmp/" + c.name + ".osty"})
			if err != nil {
				t.Fatalf("expected clean lowering, got wall: %v", err)
			}
		})
	}
}
