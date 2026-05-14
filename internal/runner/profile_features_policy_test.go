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

func TestGoFlags(t *testing.T) {
	cases := []struct {
		name           string
		optLevel       int
		profileGoFlags []string
		strip          bool
		features       []string
		want           []string
	}{
		{
			name: "opt-level-only", optLevel: 0,
			want: []string{"-gcflags=all=-N -l"},
		},
		{
			name: "dedupes-opt-against-profile", optLevel: 0,
			profileGoFlags: []string{"-gcflags=all=-N -l", "-trimpath"},
			want:           []string{"-gcflags=all=-N -l", "-trimpath"},
		},
		{
			name: "appends-strip", optLevel: 2,
			profileGoFlags: []string{"-trimpath"}, strip: true,
			want: []string{"-trimpath", "-ldflags=-s -w"},
		},
		{
			name: "strip-dedupes", optLevel: 2,
			profileGoFlags: []string{"-ldflags=-s -w"}, strip: true,
			want: []string{"-ldflags=-s -w"},
		},
		{
			name: "features-emit-sorted-tags", optLevel: 2,
			features: []string{"x", "a", "m"},
			want:     []string{"-tags=feat_a,feat_m,feat_x"},
		},
		{
			name: "features-empty-omits-tags", optLevel: 2,
			want: nil,
		},
		{
			name: "full-stack", optLevel: 0,
			profileGoFlags: []string{"-trimpath"}, strip: true,
			features: []string{"foo", "bar"},
			want: []string{
				"-gcflags=all=-N -l", "-trimpath",
				"-ldflags=-s -w", "-tags=feat_bar,feat_foo",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := GoFlags(c.optLevel, c.profileGoFlags, c.strip, c.features)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("GoFlags(...) = %v, want %v", got, c.want)
			}
		})
	}
}

func TestFileNeedsFeatures(t *testing.T) {
	cases := []struct {
		name       string
		src        string
		active     map[string]bool
		wantOk     bool
		wantMissing string
	}{
		{"no-pragma", "let x = 1\n", map[string]bool{}, true, ""},
		{"all-present", "// @feature: a, b\nlet x = 1\n", map[string]bool{"a": true, "b": true}, true, ""},
		{"missing-returns-first", "// @feature: a, b\nlet x = 1\n", map[string]bool{"a": true}, false, "b"},
		{"inactive-counts-as-missing", "// @feature: a\nlet x = 1\n", map[string]bool{"a": false}, false, "a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FileNeedsFeatures([]byte(c.src), c.active)
			if got.Ok != c.wantOk || got.Missing != c.wantMissing {
				t.Errorf("FileNeedsFeatures = %+v, want {ok:%v missing:%q}", got, c.wantOk, c.wantMissing)
			}
		})
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
			// Transitive cross-package tokens MUST be filtered —
			// downstream `feat_<name>` Go build tags can't carry `/`.
			name: "transitive-cross-package-dropped",
			features: map[string][]string{
				"a": {"dep/feat", "b"},
			},
			requested: []string{"a"}, useDefaults: false,
			want: []string{"a", "b"},
		},
		{
			// Top-level cross-package tokens DO land in the output —
			// resolver layer interprets them separately.
			name:     "top-level-cross-package-kept",
			features: map[string][]string{},
			requested: []string{"dep/feat"}, useDefaults: false,
			want: []string{"dep/feat"},
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
