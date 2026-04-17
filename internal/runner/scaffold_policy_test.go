package runner

import "testing"

func TestIsValidScaffoldName(t *testing.T) {
	valid := []string{
		"myproj", "my-tool", "my_tool", "_private",
		"A", "abc123", "a_b-c_0-9",
	}
	for _, n := range valid {
		if !IsValidScaffoldName(n) {
			t.Errorf("IsValidScaffoldName(%q) = false, want true", n)
		}
	}
	invalid := []string{
		"", "1abc", "9", "-abc",
		"my.tool", "my tool", "my/tool", "my@tool",
		"한글",
	}
	for _, n := range invalid {
		if IsValidScaffoldName(n) {
			t.Errorf("IsValidScaffoldName(%q) = true, want false", n)
		}
	}
}

func TestResolveFixtureCases(t *testing.T) {
	cases := []struct {
		requested int
		want      FixtureCaseCount
	}{
		{0, FixtureCaseCount{Count: 3, OverCap: false}},
		{-5, FixtureCaseCount{Count: 3, OverCap: false}},
		{1, FixtureCaseCount{Count: 1, OverCap: false}},
		{10, FixtureCaseCount{Count: 10, OverCap: false}},
		{64, FixtureCaseCount{Count: 64, OverCap: false}},
		{65, FixtureCaseCount{Count: 64, OverCap: true}},
		{1000, FixtureCaseCount{Count: 64, OverCap: true}},
	}
	for _, c := range cases {
		got := ResolveFixtureCases(c.requested)
		if got != c.want {
			t.Errorf("ResolveFixtureCases(%d) = %+v, want %+v", c.requested, got, c.want)
		}
	}
}

func TestScaffoldFixtureCapsMatchOstyConstants(t *testing.T) {
	if ScaffoldFixtureCasesDefault != 3 {
		t.Errorf("ScaffoldFixtureCasesDefault = %d, want 3", ScaffoldFixtureCasesDefault)
	}
	if ScaffoldFixtureCasesMax != 64 {
		t.Errorf("ScaffoldFixtureCasesMax = %d, want 64", ScaffoldFixtureCasesMax)
	}
}
