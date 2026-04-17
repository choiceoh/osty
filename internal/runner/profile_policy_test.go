package runner

import (
	"reflect"
	"testing"
)

func TestSelectProfileName(t *testing.T) {
	cases := []struct {
		name     string
		profile  string
		release  bool
		fallback string
		want     ProfileSelection
	}{
		{"release-wins-with-empty-profile", "", true, "debug", ProfileSelection{Name: "release"}},
		{"release-agrees-with-explicit-release", "release", true, "", ProfileSelection{Name: "release"}},
		{"release-conflicts-with-explicit-other", "debug", true, "", ProfileSelection{Name: "", Conflict: "--release conflicts with --profile debug"}},
		{"explicit-overrides-fallback", "staging", false, "test", ProfileSelection{Name: "staging"}},
		{"fallback-used-when-profile-empty", "", false, "test", ProfileSelection{Name: "test"}},
		{"ultimate-default-debug", "", false, "", ProfileSelection{Name: "debug"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SelectProfileName(c.profile, c.release, c.fallback)
			if got != c.want {
				t.Errorf("SelectProfileName(%q, %v, %q) = %+v, want %+v",
					c.profile, c.release, c.fallback, got, c.want)
			}
		})
	}
}

func TestParseFeatureList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a, b ,c", []string{"a", "b", "c"}},
		{"a,,b, ,c", []string{"a", "b", "c"}},
		{"  ", nil},
	}
	for _, c := range cases {
		got := ParseFeatureList(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseFeatureList(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
