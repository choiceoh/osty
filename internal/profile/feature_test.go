package profile

import (
	"reflect"
	"sort"
	"testing"
)

// TestReadFeaturePragma covers the recognised forms (with/without
// space after //) and the early-stop rule that pragmas must live at
// the top of the file.
func TestReadFeaturePragma(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"none", "fn main() {}\n", nil},
		{"single", "// @feature: net\nfn main() {}\n", []string{"net"}},
		{"no space", "//@feature: net\nfn main() {}\n", []string{"net"}},
		{"multi", "// @feature: net, tls, async\nfn main() {}\n", []string{"net", "tls", "async"}},
		{"after blank", "\n// @feature: alpha\nfn main() {}\n", []string{"alpha"}},
		{"after code is ignored", "fn x() {}\n// @feature: late\n", nil},
		{"interleaved comments ok", "// banner\n// @feature: net\nfn main() {}\n", []string{"net"}},
	}
	for _, tc := range cases {
		got := ReadFeaturePragma([]byte(tc.src))
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestFileNeedsFeatures verifies the inclusion decision: every
// required feature must be active.
func TestFileNeedsFeatures(t *testing.T) {
	src := []byte("// @feature: net, tls\nfn main() {}\n")
	if ok, _ := FileNeedsFeatures(src, map[string]bool{"net": true, "tls": true}); !ok {
		t.Errorf("expected file to be included when all features active")
	}
	if ok, missing := FileNeedsFeatures(src, map[string]bool{"net": true}); ok {
		t.Errorf("expected file to be excluded; missing should be tls")
	} else if missing != "tls" {
		t.Errorf("missing = %q, want tls", missing)
	}
	// File without pragma is always included.
	if ok, _ := FileNeedsFeatures([]byte("fn main() {}"), nil); !ok {
		t.Errorf("file without pragma should be included")
	}
}

// TestOptLevelFlags covers the defined mapping points.
func TestOptLevelFlags(t *testing.T) {
	cases := map[int][]string{
		0: {"-gcflags=all=-N -l"},
		1: {"-gcflags=all=-l"},
		2: nil,
		3: nil,
	}
	for lvl, want := range cases {
		p := &Profile{OptLevel: lvl}
		got := p.OptLevelFlags()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("OptLevel=%d: got %v, want %v", lvl, got, want)
		}
	}
}

// TestGoFlagsDedupesOptAndStrip asserts the consolidated GoFlags()
// doesn't repeat the gcflags entry that's both in Profile.GoFlags and
// derived from OptLevel, and that Strip=true contributes the
// `-ldflags=-s -w` exactly once.
func TestGoFlagsDedupesOptAndStrip(t *testing.T) {
	c := Defaults()
	r, err := c.Resolve(NameDebug, "", nil, true)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	flags := r.GoFlags()
	count := 0
	for _, f := range flags {
		if f == "-gcflags=all=-N -l" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected -gcflags=all=-N -l exactly once, got %d in %v", count, flags)
	}

	r, _ = c.Resolve(NameRelease, "", nil, true)
	flags = r.GoFlags()
	stripCount := 0
	for _, f := range flags {
		if f == "-ldflags=-s -w" {
			stripCount++
		}
	}
	if stripCount != 1 {
		t.Errorf("expected -ldflags=-s -w exactly once, got %d in %v", stripCount, flags)
	}
}

// TestParseFeatureListWhitespaceAndCommas exercises the small parser
// for the feature list inline with the pragma.
func TestParseFeatureListWhitespaceAndCommas(t *testing.T) {
	got := parseFeatureList("  net , tls,async ")
	sort.Strings(got)
	want := []string{"async", "net", "tls"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
