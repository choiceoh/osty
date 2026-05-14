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
