package runner

import "testing"

func TestDefaultHeadingFor(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"E0001", "Errors starting at E0001"},
		{"E0500", "Errors starting at E0500"},
		{"W0750", "Warnings starting at W0750"},
		{"L0001", "Lint warnings starting at L0001"},
		{"X1234", "Miscellaneous"},
		{"", "Miscellaneous"},
	}
	for _, c := range cases {
		if got := DefaultHeadingFor(c.code); got != c.want {
			t.Errorf("DefaultHeadingFor(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestStripRangeSuffix(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Lexical (E0001-E0099)", "Lexical"},
		{"Manifest (E2017)", "Manifest"},
		{"Lexical", "Lexical"},
		{"", ""},
		{"  Lexical  ", "Lexical"},
		{"Foo (bar) (E0001)", "Foo (bar)"},
	}
	for _, c := range cases {
		if got := StripRangeSuffix(c.in); got != c.want {
			t.Errorf("StripRangeSuffix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUnsafeForBootstrapGen(t *testing.T) {
	yes := []string{
		"match x { A => B }",
		"let f = fn (x) => { x }",
	}
	for _, in := range yes {
		if !UnsafeForBootstrapGen(in) {
			t.Errorf("UnsafeForBootstrapGen(%q) = false, want true", in)
		}
	}
	no := []string{
		"fn main() {}", // brace only
		"{ a, b }",     // brace only
		"x => y",       // fat arrow only
		"let x = 1",    // neither
		"",
	}
	for _, in := range no {
		if UnsafeForBootstrapGen(in) {
			t.Errorf("UnsafeForBootstrapGen(%q) = true, want false", in)
		}
	}
}

func TestProseExample(t *testing.T) {
	yes := []string{"foo(...)", "a ... b", "a … b", "x → y"}
	for _, in := range yes {
		if !ProseExample(in) {
			t.Errorf("ProseExample(%q) = false, want true", in)
		}
	}
	no := []string{"let x = 1", "", "a -> b"}
	for _, in := range no {
		if ProseExample(in) {
			t.Errorf("ProseExample(%q) = true, want false", in)
		}
	}
}

func TestFamilyForHeading(t *testing.T) {
	cases := []struct {
		name    string
		heading string
		want    string
	}{
		{"exact-lexical", "Lexical", "FamilyLexical"},
		{"exact-expressions", "Expressions", "FamilyExpression"},
		{"exact-scaffolding", "Scaffolding", "FamilyScaffold"},
		{"strips-range-suffix", "Lexical (E0001-E0099)", "FamilyLexical"},
		{"prefix-manifest", "Manifest — TOML syntax.", "FamilyManifest"},
		{"prefix-lint", "Lint — naming", "FamilyLint"},
		{"prefix-type-checking", "Type checking — generic instantiation", "FamilyTypeChecking"},
		{"v06-hidden-dep", "v0.6 — Hidden-Dependency-Surface", "FamilyAnnotation"},
		{"v06-capabilities", "G36 — Capabilities", "FamilyTypeChecking"},
		{"v06-publishing", "G44 — Publishing", "FamilyManifest"},
		{"unknown", "Wholly Unknown Heading", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FamilyForHeading(c.heading); got != c.want {
				t.Errorf("FamilyForHeading(%q) = %q, want %q", c.heading, got, c.want)
			}
		})
	}
}

func TestCodesdocHeadingPrefixRulesOrder(t *testing.T) {
	rules := CodesdocHeadingPrefixRules()
	if len(rules) != 5 {
		t.Fatalf("rule count = %d, want 5", len(rules))
	}
	if rules[0].Prefix != "Manifest" || rules[0].Family != "FamilyManifest" {
		t.Errorf("rules[0] = %+v, want {Manifest FamilyManifest}", rules[0])
	}
	if rules[2].Prefix != "Type checking" {
		t.Errorf("rules[2].Prefix = %q, want Type checking", rules[2].Prefix)
	}
}

func TestRangeSuffix(t *testing.T) {
	cases := []struct {
		name    string
		heading string
		lo      string
		hi      string
		want    string
	}{
		{"multiple-codes", "Lexical", "E0001", "E0099", "Lexical (E0001–E0099)"},
		{"single-code", "Deprecation warning", "W0750", "W0750", "Deprecation warning (W0750)"},
		{"empty-range", "Lexical", "", "", "Lexical"},
		{"already-suffixed", "Lexical (E0001-E0099)", "E0100", "E0199", "Lexical (E0001-E0099)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RangeSuffix(c.heading, c.lo, c.hi); got != c.want {
				t.Errorf("RangeSuffix(%q, %q, %q) = %q, want %q",
					c.heading, c.lo, c.hi, got, c.want)
			}
		})
	}
}

func TestHarvestPhaseFor(t *testing.T) {
	cases := []struct {
		family string
		want   string
	}{
		{"FamilyLexical", "resolve"},
		{"FamilyDeclaration", "resolve"},
		{"FamilyExpression", "resolve"},
		{"FamilyTypePattern", "resolve"},
		{"FamilyAnnotation", "resolve"},
		{"FamilyResolution", "resolve"},
		{"FamilyControlFlow", "check"},
		{"FamilyTypeChecking", "check"},
		{"FamilyWarning", "check"},
		{"FamilyLint", "lint"},
		{"FamilyManifest", "skip"},
		{"FamilyScaffold", "skip"},
		{"FamilyUnknown", "skip"},
		{"", "skip"},
		{"NotAFamily", "skip"},
	}
	for _, c := range cases {
		if got := HarvestPhaseFor(c.family); got != c.want {
			t.Errorf("HarvestPhaseFor(%q) = %q, want %q", c.family, got, c.want)
		}
	}
}

func TestOstyEscape(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"hello", "hello"},
		{"", ""},
		{`a\b`, `a\\b`},
		{`a"b`, `a\"b`},
		{"a\nb", `a\nb`},
		{"a\tb", `a\tb`},
		{"a\rb", `a\rb`},
		{"a{b}c", `a\{b\}c`},
		{"한글", "한글"},
		// Locks parity with the Osty side: non-ASCII runes are
		// passed through as their original UTF-8 bytes (Go's
		// b.WriteRune ↔ Osty's Char.toString()), while ASCII
		// metachars still take the escape path.
		{`한\글"日{本}`, `한\\글\"日\{本\}`},
	}
	for _, c := range cases {
		if got := OstyEscape(c.in); got != c.want {
			t.Errorf("OstyEscape(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
