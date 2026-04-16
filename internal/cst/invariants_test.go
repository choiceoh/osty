package cst_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/cst"
	"github.com/osty/osty/internal/selfhost"
)

// TestInvariantsOffsetMonotonic asserts that within any interior node, child
// offsets are monotonic (lo[i] <= lo[i+1]) and every child sits inside the
// parent's [offset, end) range. Violations indicate a bug in width
// aggregation or emission order.
func TestInvariantsOffsetMonotonic(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, rel := range corpus {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				t.Skipf("corpus missing: %v", err)
				return
			}
			tree, _ := selfhost.ParseCST(raw)
			if tree == nil {
				t.Fatal("ParseCST returned nil tree")
			}
			tree.Root().Walk(func(r cst.Red) bool {
				if r.IsToken() {
					return true
				}
				parentLo, parentHi := r.TextRange()
				prevHi := parentLo
				for i := 0; i < r.ChildCount(); i++ {
					c := r.ChildAt(i)
					lo, hi := c.TextRange()
					if lo < prevHi {
						t.Errorf("%s: child %d of kind %v starts at %d but previous sibling ended at %d (overlap)",
							rel, i, r.Kind(), lo, prevHi)
					}
					if lo < parentLo || hi > parentHi {
						t.Errorf("%s: child %d of kind %v range [%d,%d) escapes parent range [%d,%d)",
							rel, i, r.Kind(), lo, hi, parentLo, parentHi)
					}
					prevHi = hi
				}
				return true
			})
		})
	}
}

// TestInvariantsParentReachesRoot walks every materialized node and asserts
// its Parent chain terminates (no cycles).
func TestInvariantsParentReachesRoot(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, rel := range corpus {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				t.Skipf("corpus missing: %v", err)
				return
			}
			tree, _ := selfhost.ParseCST(raw)
			if tree == nil {
				return
			}
			tree.Root().Walk(func(r cst.Red) bool {
				depth := 0
				cur := r
				for {
					p, ok := cur.Parent()
					if !ok {
						break
					}
					cur = p
					depth++
					if depth > 10_000 {
						t.Fatalf("%s: parent chain exceeded 10000 steps; probable cycle", rel)
					}
				}
				return true
			})
		})
	}
}

// TestInvariantsFindCoveringReturnsInRange asserts that for every sampled
// byte offset in source, FindCoveringNode returns a node containing that
// offset. Probes a handful of offsets per file to keep runtime bounded.
func TestInvariantsFindCoveringReturnsInRange(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, rel := range corpus {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				t.Skipf("corpus missing: %v", err)
				return
			}
			tree, _ := selfhost.ParseCST(raw)
			if tree == nil || len(tree.Source) == 0 {
				return
			}
			src := tree.Source
			for _, off := range []int{0, len(src) / 4, len(src) / 2, 3 * len(src) / 4, len(src) - 1} {
				if off < 0 || off >= len(src) {
					continue
				}
				found := tree.Root().FindCoveringNode(off)
				lo, hi := found.TextRange()
				if off < lo || off >= hi {
					t.Errorf("%s: FindCoveringNode(%d) returned %v with range [%d,%d); offset not covered",
						rel, off, found.Kind(), lo, hi)
				}
			}
		})
	}
}
