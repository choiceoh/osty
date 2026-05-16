package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ir"
)

// TestInjectReachableStdlibBodiesPicksUpOptionAndResultCombinators
// covers the closure-taking combinators on prelude `Option<T>` /
// `Result<T, E>` values. Before `BuiltinTypeOwningModule` registered
// these owners, `opt.map(|x| ...)`-style call sites stayed as
// MethodCall nodes through monomorph and MIR — the LLVM backend
// then fell back to the per-intrinsic dispatch in
// `toolchain/mir_generator.osty`, which only handled `isSome` /
// `isNone` / `unwrap` and walled on any combinator carrying a
// closure argument or method-local generic.
//
// With the owners registered, the method body lands as a free fn
// `osty_std_option__Option__map(self: Option<T>, f: fn(T) -> U) -> U?`
// (and analogously for Result), and the user-side call site is
// rewritten in place into a CallExpr against that mangled symbol
// with the receiver prepended as the first positional argument —
// exactly the shape `TestLLVMBackendEmitListHigherOrder` asserts
// for `List<T>.map`.
//
// Asserts:
//  1. the relevant `osty_std_option__Option__<m>` and
//     `osty_std_result__Result__<m>` free fns are produced;
//  2. they carry an explicit `self` first param of the owning
//     NamedType (so monomorph can substitute the receiver's
//     concrete generics into the body's `match self`);
//  3. the rewritten call sites in `main` no longer carry
//     MethodCall nodes for those methods — they're CallExprs
//     against the mangled names.
func TestInjectReachableStdlibBodiesPicksUpOptionAndResultCombinators(t *testing.T) {
	t.Setenv("OSTY_STDLIB_BODY_LOWER", "1")
	src := `fn main() {
    let opt = Some(5)
    let doubled = opt.map(|x| x * 2)
    let filtered = doubled.filter(|x| x > 0)
    let chained = filtered.andThen(|x| Some(x + 1))
    println(chained.unwrapOr(0))

    let r: Result<Int, String> = Ok(10)
    let r2 = r.map(|x| x + 1)
    let r3 = r2.andThen(|x| Ok(x * 2))
    println(r3.unwrapOr(0))
}`
	file, res, chk := parseBackendFile(t, src)
	entry, err := LowerEntryIR("main", "main.osty", file, res, chk)
	if err != nil {
		t.Fatalf("LowerEntryIR: %v", err)
	}
	mod := entry.IR
	if mod == nil {
		t.Fatal("nil module")
	}

	// Each injected method is rewritten by Monomorphize into a
	// `_Z<len><mangled>I<type-codes>E…` Itanium-style symbol — the
	// stable substring is the source-name infix `osty_std_<module>__<Type>__<method>`.
	// We assert the infix appears so the test stays stable across
	// future mangling tweaks while still proving the method's body
	// reached the output module.
	want := []string{
		"osty_std_option__Option__map",
		"osty_std_option__Option__filter",
		"osty_std_option__Option__andThen",
		"osty_std_result__Result__map",
		"osty_std_result__Result__andThen",
	}
	have := map[string]bool{}
	var mainFn *ir.FnDecl
	for _, d := range mod.Decls {
		fn, ok := d.(*ir.FnDecl)
		if !ok {
			continue
		}
		if fn.Name == "main" {
			mainFn = fn
			continue
		}
		have[fn.Name] = true
		if !strings.Contains(fn.Name, "osty_std_option__Option__") &&
			!strings.Contains(fn.Name, "osty_std_result__Result__") {
			continue
		}
		if len(fn.Params) == 0 || fn.Params[0].Name != "self" {
			t.Errorf("%s: first param = %+v, want self", fn.Name, fn.Params)
			continue
		}
		if len(fn.Generics) != 0 {
			t.Errorf("%s: post-monomorph generics = %d, want 0 (fully specialized)", fn.Name, len(fn.Generics))
		}
		nt, ok := fn.Params[0].Type.(*ir.NamedType)
		if !ok {
			t.Errorf("%s: self.Type = %T, want *NamedType", fn.Name, fn.Params[0].Type)
			continue
		}
		switch {
		case strings.Contains(fn.Name, "osty_std_option__Option__"):
			// After monomorph the self type is the specialization's
			// mangled `_ZTS…Option…` nominal, not the source
			// `option.Option`. Accept either: the substring
			// "Option" survives both encodings.
			if !strings.Contains(nt.Name, "Option") {
				t.Errorf("%s: self.Type.Name = %q, want substring Option", fn.Name, nt.Name)
			}
		case strings.Contains(fn.Name, "osty_std_result__Result__"):
			if !strings.Contains(nt.Name, "Result") {
				t.Errorf("%s: self.Type.Name = %q, want substring Result", fn.Name, nt.Name)
			}
		}
	}
	for _, w := range want {
		found := false
		for k := range have {
			if strings.Contains(k, w) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing injected free fn whose name contains %q (have %v)", w, sortedKeys(have))
		}
	}

	if mainFn == nil {
		t.Fatal("no main fn in module")
	}
	rewrittenCombinators := []string{"map", "filter", "andThen"}
	leftAsMethodCall := map[string]bool{}
	rewrittenAsCallExpr := map[string]bool{}
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		switch x := n.(type) {
		case *ir.MethodCall:
			for _, m := range rewrittenCombinators {
				if x.Name == m {
					leftAsMethodCall[m] = true
				}
			}
		case *ir.CallExpr:
			id, ok := x.Callee.(*ir.Ident)
			if !ok {
				return true
			}
			for _, m := range rewrittenCombinators {
				if strings.Contains(id.Name, "__Option__"+m) || strings.Contains(id.Name, "__Result__"+m) {
					rewrittenAsCallExpr[m] = true
				}
			}
		}
		return true
	}), mainFn)
	for _, m := range rewrittenCombinators {
		if leftAsMethodCall[m] {
			t.Errorf("main contains unrewritten MethodCall.%s — Option/Result owner mapping not picked up", m)
		}
		if !rewrittenAsCallExpr[m] {
			t.Errorf("main missing CallExpr against mangled Option/Result %s — call-site rewrite did not run", m)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
