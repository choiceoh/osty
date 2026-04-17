package runner

import "testing"

func TestResolveTestWorkers(t *testing.T) {
	cases := []struct {
		name                              string
		serial                            bool
		jobs, cpuCount, testCount, want int
	}{
		{"serial-wins", true, 8, 16, 100, 1},
		{"serial-with-zero-jobs", true, 0, 16, 100, 1},
		{"default-to-cpu-count", false, 0, 8, 100, 8},
		{"explicit-jobs", false, 4, 16, 100, 4},
		{"clamp-to-test-count", false, 32, 16, 4, 4},
		{"clamp-to-test-count-default", false, 0, 16, 2, 2},
		{"negative-jobs-behaves-as-default", false, -2, 8, 100, 8},
		{"minimum-one-when-cpu-zero", false, 0, 0, 100, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ResolveTestWorkers(c.serial, c.jobs, c.cpuCount, c.testCount)
			if got != c.want {
				t.Errorf("ResolveTestWorkers(%v, %d, %d, %d) = %d, want %d",
					c.serial, c.jobs, c.cpuCount, c.testCount, got, c.want)
			}
		})
	}
}

func TestMatchesTestFilters(t *testing.T) {
	cases := []struct {
		name    string
		tname   string
		filters []string
		want    bool
	}{
		{"empty-filters-match-all", "testFoo", nil, true},
		{"substring-match", "testParseConfigHappyPath", []string{"Config"}, true},
		{"one-of-many-filters", "testLexer", []string{"Lex", "Paren"}, true},
		{"no-match", "testLexer", []string{"Parser"}, false},
		{"ignores-empty-entries", "testLexer", []string{"", "Lex"}, true},
		{"all-empty-entries-is-no-match", "testLexer", []string{"", ""}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := MatchesTestFilters(c.tname, c.filters)
			if got != c.want {
				t.Errorf("MatchesTestFilters(%q, %v) = %v, want %v",
					c.tname, c.filters, got, c.want)
			}
		})
	}
}

func TestSanitizeNativeTestName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"testFoo123", "testFoo123"},
		{"test.foo-bar baz", "test_foo_bar_baz"},
		{"", "osty_test"},
		{"abc", "abc"},
		{"  ", "__"},
	}
	for _, c := range cases {
		if got := SanitizeNativeTestName(c.in); got != c.want {
			t.Errorf("SanitizeNativeTestName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
