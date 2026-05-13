package runner

import (
	"reflect"
	"testing"
)

func TestSplitTopLevelComma(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"whitespace-only", "   ", nil},
		{"single", "just_one", []string{"just_one"}},
		{"simple", "a, b, c", []string{"a", " b", " c"}},
		{"ignores-parens", "a, (b, c), d", []string{"a", " (b, c)", " d"}},
		{"ignores-brackets", "a, [b, c, d], e", []string{"a", " [b, c, d]", " e"}},
		{"ignores-braces", "x, {a, b}, y", []string{"x", " {a, b}", " y"}},
		{"nested", "foo(a, b), bar([1, 2])", []string{"foo(a, b)", " bar([1, 2])"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SplitTopLevelComma(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("SplitTopLevelComma(%q) = %#v, want %#v", c.in, got, c.want)
			}
		})
	}
}

func TestIsSimpleIdentifierBinding(t *testing.T) {
	allowed := []string{"_", "name", "_leading", "snake_case", "camelCase", "x123"}
	for _, in := range allowed {
		if !IsSimpleIdentifierBinding(in) {
			t.Errorf("IsSimpleIdentifierBinding(%q) = false, want true", in)
		}
	}
	rejected := []string{"", "1abc", "with-hyphen", "a.b", "a,b", "a b"}
	for _, in := range rejected {
		if IsSimpleIdentifierBinding(in) {
			t.Errorf("IsSimpleIdentifierBinding(%q) = true, want false", in)
		}
	}
}

func TestParseAppendCallSimple(t *testing.T) {
	r := ParseAppendCall("append(items, x)")
	if !r.Ok || r.Base != "items" || r.Item != "x" {
		t.Errorf("ParseAppendCall(append(items, x)) = %#v, want {items x true}", r)
	}
}

func TestParseAppendCallTrimsArgs(t *testing.T) {
	r := ParseAppendCall("append( xs ,  value )")
	if !r.Ok || r.Base != "xs" || r.Item != "value" {
		t.Errorf("ParseAppendCall trims = %#v, want {xs value true}", r)
	}
}

func TestParseAppendCallNestedItem(t *testing.T) {
	r := ParseAppendCall("append(out, foo(a, b))")
	if !r.Ok || r.Base != "out" || r.Item != "foo(a, b)" {
		t.Errorf("ParseAppendCall nested = %#v, want {out foo(a, b) true}", r)
	}
}

func TestParseAppendCallRejects(t *testing.T) {
	cases := []string{
		"append(only_one)",       // wrong arity
		"push(xs, v)",            // not append
		"append(a, b) + 1",       // trailing
		"append(, x)",            // empty base
		"append(xs, )",           // empty item
		"",                       // empty
		"append",                 // no parens
	}
	for _, in := range cases {
		r := ParseAppendCall(in)
		if r.Ok {
			t.Errorf("ParseAppendCall(%q).Ok = true, want false", in)
		}
	}
}

func TestNormalizeTupleLoopBindings(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
		want string
	}{
		{"single-rejected", "only_one", false, ""},
		{"two-idents", "i, value", true, "i, value"},
		{"trims-parts", "  k ,  v  ", true, "k, v"},
		{"rejects-expr", "i, foo(x)", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizeTupleLoopBindings(c.in)
			if got.Ok != c.ok {
				t.Errorf("NormalizeTupleLoopBindings(%q).Ok = %v, want %v", c.in, got.Ok, c.ok)
			}
			if got.Joined != c.want {
				t.Errorf("NormalizeTupleLoopBindings(%q).Joined = %q, want %q", c.in, got.Joined, c.want)
			}
		})
	}
}

func TestRewriteJSForOfHeader(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
		want string
	}{
		{"simple", "for (item of items) {", true, "for item in items {"},
		{"const", "for (const item of items) {", true, "for item in items {"},
		{"let", "for (let x of xs) {", true, "for x in xs {"},
		{"var", "for (var x of xs) {", true, "for x in xs {"},
		{"array-destructure", "for (const [k, v] of entries) {", true, "for (k, v) in entries {"},
		{"reject-bare-for", "for x in xs {", false, ""},
		{"reject-pattern-lhs", "for (foo(x) of xs) {", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RewriteJSForOfHeader(c.in)
			if got.Ok != c.ok {
				t.Errorf("RewriteJSForOfHeader(%q).Ok = %v, want %v", c.in, got.Ok, c.ok)
			}
			if got.Rewritten != c.want {
				t.Errorf("RewriteJSForOfHeader(%q).Rewritten = %q, want %q", c.in, got.Rewritten, c.want)
			}
		})
	}
}

func TestRewritePythonRangeLoopHeader(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
		want string
	}{
		{"one-arg", "for i in range(10) {", true, "for i in 0..10 {"},
		{"two-args", "for i in range(2, 8) {", true, "for i in 2..8 {"},
		{"three-args-step-one", "for i in range(0, 10, 1) {", true, "for i in 0..10 {"},
		{"reject-non-unit-stride", "for i in range(0, 10, 2) {", false, ""},
		{"reject-four-args", "for i in range(0, 10, 1, x) {", false, ""},
		{"reject-bad-binding", "for i, j in range(10) {", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RewritePythonRangeLoopHeader(c.in)
			if got.Ok != c.ok {
				t.Errorf("RewritePythonRangeLoopHeader(%q).Ok = %v, want %v", c.in, got.Ok, c.ok)
			}
			if got.Rewritten != c.want {
				t.Errorf("RewritePythonRangeLoopHeader(%q).Rewritten = %q, want %q", c.in, got.Rewritten, c.want)
			}
		})
	}
}

func TestRewritePythonEnumerateLoopHeader(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
		want string
	}{
		{"bare-tuple", "for i, v in enumerate(xs) {", true, "for (i, v) in xs.enumerate() {"},
		{"paren-tuple", "for (i, v) in enumerate(items) {", true, "for (i, v) in items.enumerate() {"},
		{"reject-bare-ident", "for x in enumerate(xs) {", false, ""},
		{"reject-empty-arg", "for i, v in enumerate() {", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RewritePythonEnumerateLoopHeader(c.in)
			if got.Ok != c.ok {
				t.Errorf("RewritePythonEnumerateLoopHeader(%q).Ok = %v, want %v", c.in, got.Ok, c.ok)
			}
			if got.Rewritten != c.want {
				t.Errorf("RewritePythonEnumerateLoopHeader(%q).Rewritten = %q, want %q", c.in, got.Rewritten, c.want)
			}
		})
	}
}

func TestRewriteForeignLoopHeader(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		ok       bool
		wantKind string
	}{
		{"js-for-of", "for (item of items) {", true, "js_for_of_loop"},
		{"py-enumerate", "for i, v in enumerate(xs) {", true, "python_enumerate_loop"},
		{"py-range", "for i in range(10) {", true, "python_range_loop"},
		{"unknown", "if cond {", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RewriteForeignLoopHeader(c.in)
			if got.Ok != c.ok {
				t.Errorf("RewriteForeignLoopHeader(%q).Ok = %v, want %v", c.in, got.Ok, c.ok)
			}
			if got.Kind != c.wantKind {
				t.Errorf("RewriteForeignLoopHeader(%q).Kind = %q, want %q", c.in, got.Kind, c.wantKind)
			}
		})
	}
}

func TestIsRewritableLengthTypeKind(t *testing.T) {
	yes := []struct{ kind, name string }{
		{"primitive", "String"},
		{"primitive", "Bytes"},
		{"named", "List"},
		{"named", "Map"},
		{"named", "Set"},
		{"named", "OrderedMap"},
	}
	for _, c := range yes {
		if !IsRewritableLengthTypeKind(c.kind, c.name) {
			t.Errorf("IsRewritableLengthTypeKind(%q, %q) = false, want true", c.kind, c.name)
		}
	}
	no := []struct{ kind, name string }{
		{"primitive", "Int"},
		{"primitive", "Float"},
		{"primitive", ""},
		{"named", "MyStruct"},
		{"generic", "List"},
		{"", ""},
	}
	for _, c := range no {
		if IsRewritableLengthTypeKind(c.kind, c.name) {
			t.Errorf("IsRewritableLengthTypeKind(%q, %q) = true, want false", c.kind, c.name)
		}
	}
}
