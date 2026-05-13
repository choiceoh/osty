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
