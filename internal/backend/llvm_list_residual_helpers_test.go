package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ir"
)

// TestListResidualHelpersSpecializeCleanly locks in the
// `internal/stdlib/modules/collections.osty` "residual" combinator
// surface — `enumerate`, `chunked`, `windowed`, `partition`, `zip` —
// flowing through the full body-injection + struct-template-injection
// + monomorphize pipeline without leaving unresolved type variables
// on the way.
//
// Plan entry: 🟡 "List<T> residual collection helper shapes" on
// LLVM_MIGRATION_PLAN.md Tier A. The methods return shapes the
// stdlib-type-injector previously walled on (`List<List<T>>`,
// `List<(Int, T)>`, `(List<T>, List<T>)`) and the call-site +
// scan-order interaction left the templates' bodies pointing at
// generic `T` after specialization — monomorph then emitted
// `monomorph: type arg 0 of struct List still contains a type
// variable ((Int, T))` warnings and silently dropped the bodies.
//
// The wall closed in this PR: `scanExpr` was running its generic
// `rewriteExprType` (which queues struct specs eagerly) BEFORE
// `rewriteGenericCall` (which substitutes the call's return type
// from the resolved env). For a generic free-fn call site —
// including stdlib bodied helpers rewritten via
// `RewriteStdlibMethodCallsites` — the return type carries the
// method's own TypeVars (e.g. enumerate's return `List<(Int, T)>`
// with bare `T`). rewriteExprType saw the bare `T` and queued
// `requestStructType(List, [(Int, T)])` which `containsTypeVar`
// rejected. The CallExpr branch now runs `rewriteGenericCall`
// first, so by the time rewriteExprType walks `c.T` the type is
// already concretized to `List<(Int, Int)>`.
//
// Asserts per helper: (a) the user-side call triggers body
// injection — a mangled
// `_Z*osty_std_collections__List__<helper>I…E…` FnDecl appears in
// the lowered module with a non-nil Body and Generics = 0 (fully
// specialized); (b) monomorphize emits no arity / unresolved
// type-var diagnostic.
func TestListResidualHelpersSpecializeCleanly(t *testing.T) {
	cases := []struct {
		helper string
		src    string
	}{
		{
			helper: "enumerate",
			src: `fn main() {
    let xs: List<Int> = [10, 20, 30]
    let pairs = xs.enumerate()
    println(pairs.len())
}`,
		},
		{
			helper: "chunked",
			src: `fn main() {
    let xs: List<Int> = [1, 2, 3, 4, 5]
    let chunks = xs.chunked(2)
    println(chunks.len())
}`,
		},
		{
			helper: "windowed",
			src: `fn main() {
    let xs: List<Int> = [1, 2, 3, 4, 5]
    let wins = xs.windowed(3, 1)
    println(wins.len())
}`,
		},
		{
			helper: "partition",
			src: `fn main() {
    let xs: List<Int> = [1, 2, 3, 4, 5]
    let (yes, no) = xs.partition(|x| x % 2 == 0)
    println(yes.len())
}`,
		},
		{
			helper: "zip",
			src: `fn main() {
    let xs: List<Int> = [1, 2, 3]
    let ys: List<Int> = [10, 20, 30]
    let pairs = xs.zip(ys)
    println(pairs.len())
}`,
		},
	}
	for _, c := range cases {
		t.Run(c.helper, func(t *testing.T) {
			file, res, chk := parseBackendFile(t, c.src)
			entry, err := LowerEntryIR("main", "main.osty", file, res, chk)
			if err != nil {
				t.Fatalf("LowerEntryIR: %v", err)
			}
			var found *ir.FnDecl
			needle := "osty_std_collections__List__" + c.helper
			for _, d := range entry.IR.Decls {
				fn, ok := d.(*ir.FnDecl)
				if !ok {
					continue
				}
				if strings.Contains(fn.Name, needle) {
					found = fn
					break
				}
			}
			if found == nil {
				names := []string{}
				for _, d := range entry.IR.Decls {
					if fn, ok := d.(*ir.FnDecl); ok {
						names = append(names, fn.Name)
					}
				}
				t.Fatalf("no specialization for %q in module; have %v", needle, names)
			}
			if found.Body == nil {
				t.Errorf("%s: Body = nil, want lowered block", found.Name)
			}
			if len(found.Generics) != 0 {
				t.Errorf("%s: Generics = %d, want 0 (fully specialized)", found.Name, len(found.Generics))
			}
			for _, e := range entry.IRIssues {
				msg := e.Error()
				if strings.Contains(msg, "still contains a type variable") ||
					strings.Contains(msg, "arity mismatch") {
					t.Errorf("monomorph regression (%s): %s", c.helper, msg)
				}
			}
		})
	}
}
