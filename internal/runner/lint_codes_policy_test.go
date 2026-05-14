package runner

import "testing"

func TestIsLintCode(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"basic-L0001", "L0001", true},
		{"basic-L0040", "L0040", true},
		{"basic-L9999", "L9999", true},
		{"single-digit-L1", "L1", true},
		{"single-digit-L0", "L0", true},
		{"reject-bare-L", "L", false},
		{"reject-empty", "", false},
		{"reject-E-prefix", "E0001", false},
		{"reject-W-prefix", "W0750", false},
		{"reject-lower-l", "l0001", false},
		{"reject-X-prefix", "X0001", false},
		{"reject-alpha-tail", "L00aa", false},
		{"reject-all-alpha", "LABC", false},
		{"reject-underscore", "L_1", false},
		{"reject-alias-unused", "unused", false},
		{"reject-alias-dead_code", "dead_code", false},
		{"reject-alias-all", "all", false},
		{"reject-trailing-space", "L0001 ", false},
		{"reject-leading-space", " L0001", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsLintCode(c.in); got != c.want {
				t.Errorf("IsLintCode(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestLintMergeStringSlices(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want []string
	}{
		{"empty", nil, nil, nil},
		{"a-only", []string{"a", "b"}, nil, []string{"a", "b"}},
		{"b-only", nil, []string{"x", "y"}, []string{"x", "y"}},
		{"union", []string{"a", "b"}, []string{"c", "d"}, []string{"a", "b", "c", "d"}},
		{"dedup-b-overlap-a", []string{"a", "b"}, []string{"b", "c"}, []string{"a", "b", "c"}},
		{"dedup-within-a", []string{"a", "a", "b"}, nil, []string{"a", "b"}},
		{"preserves-a-order", []string{"z", "a"}, []string{"a", "z"}, []string{"z", "a"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := LintMergeStringSlices(c.a, c.b)
			if len(got) != len(c.want) {
				t.Fatalf("len = %d, want %d (got=%v)", len(got), len(c.want), got)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}
