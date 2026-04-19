package doctest

import "testing"

func TestSanitizeIdent(t *testing.T) {
	cases := map[string]string{
		"add":        "add",
		"add-two":    "add_two",
		"parse::<T>": "parse___T_",
		"":           "",
		"Foo99":      "Foo99",
		"9leading":   "_leading",
		"µ_weird":    "__weird",
		"with space": "with_space",
	}
	for in, want := range cases {
		if got := sanitizeIdent(in); got != want {
			t.Errorf("sanitizeIdent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFnName(t *testing.T) {
	cases := []struct {
		owner string
		ord   int
		want  string
	}{
		{"greet", 1, "test_doc_greet_1"},
		{"", 3, "test_doc_file_3"},
		{"add-two", 2, "test_doc_add_two_2"},
	}
	for _, c := range cases {
		got := FnName(Doctest{Owner: c.owner, OrdinalInOwner: c.ord})
		if got != c.want {
			t.Errorf("FnName(%q, %d) = %q, want %q", c.owner, c.ord, got, c.want)
		}
	}
}
