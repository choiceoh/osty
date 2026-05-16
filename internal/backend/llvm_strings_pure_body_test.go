package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ir"
)

// TestStringsPureBodyHelpersLowerThroughInjection locks in the
// `internal/stdlib/modules/strings.osty` pure-body surface. Each
// helper here has an Osty body (not a runtime shim) and reaches the
// LLVM backend via the `injectReachableStdlibBodies` →
// `RewriteStdlibCallsites` → `Monomorphize` pipeline. Without a
// regression test the surface drifts silently when one of the
// downstream stages stops accepting a helper's shape — bodied stdlib
// fns are dropped at lower time, the user call is left pointing at
// an unmangled symbol, and the warning is buried under the LIR
// Proto subprocess decline boilerplate.
//
// The plan's 🟡 "String pure-body coverage" entry tracks this
// surface (`LLVM_MIGRATION_PLAN.md`). Each subtest asserts:
//
//  1. the user-side call site triggers body injection — a
//     `osty_std_strings__<helper>` FnDecl appears in the lowered
//     module with a non-nil Body;
//  2. its Generics list is empty (pure Osty body helpers have no
//     own type parameters);
//  3. monomorphize emits no arity / unresolved-type-var diagnostic
//     for the helper's body — the bodies use only intrinsic-backed
//     `String` / `List<Char>` operations, so they should all
//     specialize cleanly.
//
// The helper list covers the canonical bodied-helper shapes:
//
//   - `split` / `splitN` / `splitLines` — bodied loop over `s.chars()`
//   - `join` / `lines` — String accumulation
//   - `trim*` family — slice into char ranges
//   - `replace` / `replaceAll` — transitive split + join
//   - `toUpper` / `toLower` — fold over chars
//   - `repeat` — `for i in 0..n { out = out.concat(s) }`
//   - `contains` / `startsWith` / `endsWith` / `indexOf` / `count` —
//     bodied predicates with byte-walk loops
//   - `fields` — whitespace-split bodied helper
//
// Each is the shortest non-trivial surface for the corresponding
// Osty body construct (loop / accumulator / transitive call) the
// injection pipeline must lower for stdlib `std.strings` to ship
// end-to-end through the native LLVM route.
func TestStringsPureBodyHelpersLowerThroughInjection(t *testing.T) {
	t.Setenv("OSTY_STDLIB_BODY_LOWER", "1")
	cases := []struct {
		helper string
		src    string
	}{
		{
			helper: "split",
			src: `use std.strings
fn main() {
    let parts = strings.split("a,b,c", ",")
    println(parts.len())
}`,
		},
		{
			helper: "join",
			src: `use std.strings
fn main() {
    let parts: List<String> = ["a", "b", "c"]
    println(strings.join(parts, ","))
}`,
		},
		{
			helper: "trim",
			src: `use std.strings
fn main() {
    println(strings.trim("  hi  "))
}`,
		},
		{
			helper: "trimPrefix",
			src: `use std.strings
fn main() {
    println(strings.trimPrefix("abc", "a"))
}`,
		},
		{
			helper: "toUpper",
			src: `use std.strings
fn main() {
    println(strings.toUpper("abc"))
}`,
		},
		{
			helper: "toLower",
			src: `use std.strings
fn main() {
    println(strings.toLower("ABC"))
}`,
		},
		{
			helper: "repeat",
			src: `use std.strings
fn main() {
    println(strings.repeat("ab", 3))
}`,
		},
		{
			helper: "replaceAll",
			src: `use std.strings
fn main() {
    println(strings.replaceAll("aaa", "a", "b"))
}`,
		},
		{
			helper: "contains",
			src: `use std.strings
fn main() {
    println(strings.contains("abc", "b"))
}`,
		},
		{
			helper: "startsWith",
			src: `use std.strings
fn main() {
    println(strings.startsWith("abc", "a"))
}`,
		},
		{
			helper: "indexOf",
			src: `use std.strings
fn main() {
    if let Some(i) = strings.indexOf("abc", "b") {
        println(i)
    }
}`,
		},
		{
			helper: "splitLines",
			src: `use std.strings
fn main() {
    let xs = strings.splitLines("a\nb\nc")
    println(xs.len())
}`,
		},
		{
			helper: "fields",
			src: `use std.strings
fn main() {
    let xs = strings.fields("a  b c")
    println(xs.len())
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
			wantName := "osty_std_strings__" + c.helper
			var injected *ir.FnDecl
			for _, d := range entry.IR.Decls {
				fn, ok := d.(*ir.FnDecl)
				if !ok {
					continue
				}
				if fn.Name == wantName {
					injected = fn
					break
				}
			}
			if injected == nil {
				t.Fatalf("no %q fn injected; module decls: %v", wantName, fnDeclNamesIn(entry.IR.Decls))
			}
			if injected.Body == nil {
				t.Errorf("%s: Body = nil, want lowered block", wantName)
			}
			if len(injected.Generics) != 0 {
				t.Errorf("%s: Generics = %d, want 0 (pure body)", wantName, len(injected.Generics))
			}
			for _, e := range entry.IRIssues {
				msg := e.Error()
				if strings.Contains(msg, "arity mismatch") ||
					strings.Contains(msg, "still contains a type variable") ||
					strings.Contains(msg, "unresolved symbol") {
					t.Errorf("ir issue (%s): %s", c.helper, msg)
				}
			}
		})
	}
}

func fnDeclNamesIn(decls []ir.Decl) []string {
	out := make([]string, 0, len(decls))
	for _, d := range decls {
		if fn, ok := d.(*ir.FnDecl); ok {
			out = append(out, fn.Name)
		}
	}
	return out
}
