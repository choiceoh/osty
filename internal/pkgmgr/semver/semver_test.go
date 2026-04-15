package semver

import "testing"

// TestParseVersion covers core, pre-release, and build metadata.
func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want Version
	}{
		{"1.2.3", Version{Major: 1, Minor: 2, Patch: 3}},
		{"v1.2.3", Version{Major: 1, Minor: 2, Patch: 3}},
		{"0.0.0", Version{}},
		{"1.2.3-alpha", Version{Major: 1, Minor: 2, Patch: 3, Pre: []string{"alpha"}}},
		{"1.2.3-alpha.1", Version{Major: 1, Minor: 2, Patch: 3, Pre: []string{"alpha", "1"}}},
		{"1.2.3+build.1", Version{Major: 1, Minor: 2, Patch: 3, Build: []string{"build", "1"}}},
		{"1.2.3-rc.1+meta", Version{Major: 1, Minor: 2, Patch: 3, Pre: []string{"rc", "1"}, Build: []string{"meta"}}},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseVersion(tc.in)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.in, err)
			}
			if got.String() != tc.want.String() {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

// TestParseVersionErrors rejects malformed inputs that SemVer 2.0.0
// explicitly disallows: leading zeros, missing parts, bad idents.
func TestParseVersionErrors(t *testing.T) {
	bad := []string{
		"",
		"1",
		"1.2",
		"1.2.3.4",
		"01.0.0", // leading zero
		"1.02.0",
		"1.2.3-",       // empty pre
		"1.2.3-01",     // numeric ident with leading zero
		"1.2.3-alpha+", // empty build
		"1.2.3-al_pha", // invalid char
	}
	for _, s := range bad {
		if _, err := ParseVersion(s); err == nil {
			t.Errorf("expected error for %q", s)
		}
	}
}

// TestCompare exercises every branch of §11 precedence.
func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		cmp  int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "2.0.0", -1},
		{"2.0.0", "1.0.0", 1},
		{"1.1.0", "1.0.0", 1},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.0-alpha", 1}, // stable > pre
		{"1.0.0-alpha", "1.0.0-alpha.1", -1},
		{"1.0.0-alpha.1", "1.0.0-alpha.beta", -1}, // numeric < alpha
		{"1.0.0-alpha.beta", "1.0.0-beta", -1},
		{"1.0.0-beta", "1.0.0-beta.2", -1},
		{"1.0.0-beta.2", "1.0.0-beta.11", -1}, // numeric compared numerically
		{"1.0.0-rc.1", "1.0.0", -1},
		{"1.0.0+build.1", "1.0.0+build.2", 0}, // build ignored
	}
	for _, tc := range cases {
		t.Run(tc.a+" vs "+tc.b, func(t *testing.T) {
			a, _ := ParseVersion(tc.a)
			b, _ := ParseVersion(tc.b)
			got := Compare(a, b)
			if got != tc.cmp {
				t.Errorf("Compare(%s, %s) = %d, want %d", tc.a, tc.b, got, tc.cmp)
			}
		})
	}
}

// TestReqMatch covers every requirement-operator branch including
// caret, tilde, wildcards, and conjunctions.
func TestReqMatch(t *testing.T) {
	cases := []struct {
		req   string
		match []string
		miss  []string
	}{
		{"1.2.3", []string{"1.2.3"}, []string{"1.2.4", "1.3.0"}},
		{"=1.2.3", []string{"1.2.3"}, []string{"1.2.4"}},
		{">=1.2.3", []string{"1.2.3", "1.2.4", "2.0.0"}, []string{"1.2.2"}},
		{">1.2.3", []string{"1.2.4"}, []string{"1.2.3"}},
		{"<=1.2.3", []string{"1.2.3", "1.0.0"}, []string{"1.2.4"}},
		{"<1.2.3", []string{"1.2.2"}, []string{"1.2.3"}},
		{"^1.2.3", []string{"1.2.3", "1.9.9"}, []string{"2.0.0", "1.2.2"}},
		{"^0.1.2", []string{"0.1.2", "0.1.9"}, []string{"0.2.0", "0.1.1"}},
		{"^0.0.1", []string{"0.0.1"}, []string{"0.0.2", "0.0.0"}},
		{"~1.2.3", []string{"1.2.3", "1.2.9"}, []string{"1.3.0", "1.2.2"}},
		{"1.*", []string{"1.0.0", "1.9.9"}, []string{"2.0.0", "0.9.0"}},
		{"1.2.*", []string{"1.2.0", "1.2.9"}, []string{"1.3.0", "1.1.9"}},
		{"*", []string{"0.0.0", "99.0.0"}, nil},
		{">=1.0 <2.0", []string{"1.0.0", "1.9.9"}, []string{"2.0.0", "0.9.0"}},
	}
	for _, tc := range cases {
		r, err := ParseReq(tc.req)
		if err != nil {
			t.Errorf("parse req %q: %v", tc.req, err)
			continue
		}
		for _, s := range tc.match {
			v, _ := ParseVersion(s)
			if !r.Match(v) {
				t.Errorf("%q should match %s", tc.req, s)
			}
		}
		for _, s := range tc.miss {
			v, _ := ParseVersion(s)
			if r.Match(v) {
				t.Errorf("%q should NOT match %s", tc.req, s)
			}
		}
	}
}

// TestPreReleaseHandling checks that caret/tilde ranges exclude
// pre-releases unless the requirement itself is pre-release, per
// SemVer 2.0.0 §11.4.
func TestPreReleaseHandling(t *testing.T) {
	r, err := ParseReq("^1.2.3")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if v, _ := ParseVersion("1.3.0-alpha"); r.Match(v) {
		t.Errorf("pre-release should not match stable range")
	}
	r2, err := ParseReq("^1.2.3-alpha")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if v, _ := ParseVersion("1.2.3-alpha.1"); !r2.Match(v) {
		t.Errorf("pre-release should match pre-release range")
	}
}

// TestMax finds the highest matching version in a list.
func TestMax(t *testing.T) {
	r, _ := ParseReq("^1.0")
	vs := []Version{
		mustV("0.9.0"),
		mustV("1.0.0"),
		mustV("1.2.5"),
		mustV("1.9.9"),
		mustV("2.0.0"),
	}
	got, ok := Max(r, vs)
	if !ok || got.String() != "1.9.9" {
		t.Errorf("Max ^1.0 = %v (ok=%v), want 1.9.9", got, ok)
	}
}

func mustV(s string) Version {
	v, err := ParseVersion(s)
	if err != nil {
		panic(err)
	}
	return v
}
