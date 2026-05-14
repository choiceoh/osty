package runner

import (
	"reflect"
	"testing"
)

func TestOptLevelFlags(t *testing.T) {
	cases := []struct {
		level int
		want  []string
	}{
		{0, []string{"-gcflags=all=-N -l"}},
		{1, []string{"-gcflags=all=-l"}},
		{2, nil},
		{3, nil},
		{99, nil},
	}
	for _, c := range cases {
		got := OptLevelFlags(c.level)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("OptLevelFlags(%d) = %v, want %v", c.level, got, c.want)
		}
	}
}

func TestExpandFeatures(t *testing.T) {
	cases := []struct {
		name        string
		features    map[string][]string
		defaults    []string
		requested   []string
		useDefaults bool
		want        []string
	}{
		{
			name: "empty", features: map[string][]string{}, defaults: nil, requested: nil,
			useDefaults: false, want: nil,
		},
		{
			name: "use-defaults-only", features: map[string][]string{},
			defaults: []string{"a", "b"}, requested: nil, useDefaults: true,
			want: []string{"a", "b"},
		},
		{
			name: "ignores-defaults-when-false", features: map[string][]string{},
			defaults: []string{"a", "b"}, requested: nil, useDefaults: false,
			want: nil,
		},
		{
			name: "union-requested-with-defaults", features: map[string][]string{},
			defaults: []string{"b"}, requested: []string{"a"}, useDefaults: true,
			want: []string{"a", "b"},
		},
		{
			name: "recurses-into-children",
			features: map[string][]string{
				"a": {"b", "c"},
				"b": {"d"},
			},
			requested: []string{"a"}, useDefaults: false,
			want: []string{"a", "b", "c", "d"},
		},
		{
			name: "dedupes",
			features: map[string][]string{
				"a": {"b"},
				"c": {"b"},
			},
			requested: []string{"a", "c"}, useDefaults: false,
			want: []string{"a", "b", "c"},
		},
		{
			name: "cross-package-not-recursed",
			features: map[string][]string{
				"a": {"dep/feat", "b"},
			},
			requested: []string{"a"}, useDefaults: false,
			want: []string{"a", "b", "dep/feat"},
		},
		{
			name: "unknown-passes-through", features: map[string][]string{},
			requested: []string{"unknown"}, useDefaults: false,
			want: []string{"unknown"},
		},
		{
			name: "cycle-safe",
			features: map[string][]string{
				"a": {"b"},
				"b": {"a"},
			},
			requested: []string{"a"}, useDefaults: false,
			want: []string{"a", "b"},
		},
		{
			name: "deterministic-sort",
			features: map[string][]string{
				"z": {"a"},
				"m": {"x"},
			},
			requested: []string{"z", "m"}, useDefaults: false,
			want: []string{"a", "m", "x", "z"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExpandFeatures(c.features, c.defaults, c.requested, c.useDefaults)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("ExpandFeatures(...) = %v, want %v", got, c.want)
			}
		})
	}
}
