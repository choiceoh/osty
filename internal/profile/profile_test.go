package profile

import (
	"reflect"
	"sort"
	"testing"
)

// TestDefaultProfilesShape asserts the four built-ins are present and
// carry the expected high-level knobs. Acts as a guard against
// accidental edits to Defaults() that would silently change what
// `osty build` emits.
func TestDefaultProfilesShape(t *testing.T) {
	c := Defaults()
	for _, n := range []string{NameDebug, NameRelease, NameProfile, NameTest} {
		if _, ok := c.Profiles[n]; !ok {
			t.Errorf("Defaults missing profile %q", n)
		}
	}
	if c.Profiles[NameDebug].Strip {
		t.Errorf("debug profile should keep symbols (Strip=false)")
	}
	if !c.Profiles[NameRelease].Strip {
		t.Errorf("release profile should strip symbols (Strip=true)")
	}
	if c.Profiles[NameRelease].OptLevel <= c.Profiles[NameDebug].OptLevel {
		t.Errorf("release OptLevel (%d) should exceed debug (%d)",
			c.Profiles[NameRelease].OptLevel,
			c.Profiles[NameDebug].OptLevel)
	}
}

// TestParseTriple covers the common shapes plus one error path.
func TestParseTriple(t *testing.T) {
	cases := []struct {
		in           string
		arch, os     string
		expectError  bool
	}{
		{"amd64-linux", "amd64", "linux", false},
		{"arm64-darwin", "arm64", "darwin", false},
		{"riscv64-freebsd", "riscv64", "freebsd", false},
		{"amd64", "", "", true},
		{"", "", "", true},
		{"-linux", "", "", true},
	}
	for _, tc := range cases {
		arch, os, err := ParseTriple(tc.in)
		if tc.expectError {
			if err == nil {
				t.Errorf("ParseTriple(%q) expected error, got (%q,%q)", tc.in, arch, os)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseTriple(%q): unexpected error %v", tc.in, err)
			continue
		}
		if arch != tc.arch || os != tc.os {
			t.Errorf("ParseTriple(%q) = (%q,%q), want (%q,%q)",
				tc.in, arch, os, tc.arch, tc.os)
		}
	}
}

// TestResolveHostTarget verifies that an empty triple produces a
// Resolved with nil Target (the "host" signal for GoEnv).
func TestResolveHostTarget(t *testing.T) {
	c := Defaults()
	r, err := c.Resolve(NameDebug, "", nil, true)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if r.Target != nil {
		t.Errorf("expected nil target for host, got %+v", r.Target)
	}
	env := r.GoEnv()
	if _, present := env["GOOS"]; present {
		t.Errorf("host resolve should not set GOOS, got %v", env)
	}
}

// TestResolveUnknownProfile asserts the error path for a missing
// --profile NAME.
func TestResolveUnknownProfile(t *testing.T) {
	c := Defaults()
	if _, err := c.Resolve("nope", "", nil, true); err == nil {
		t.Fatalf("expected error for unknown profile")
	}
}

// TestFeatureExpansion covers default-feature inclusion, explicit
// request union, and transitive graph walking.
func TestFeatureExpansion(t *testing.T) {
	c := Defaults()
	c.Features = map[string][]string{
		"full":   {"net", "tls"},
		"net":    {"async"},
		"tls":    {"crypto", "dep/openssl"},
		"async":  nil,
		"crypto": nil,
	}
	c.DefaultFeatures = []string{"net"}

	r, err := c.Resolve(NameDebug, "", []string{"full"}, true)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got := append([]string(nil), r.Features...)
	sort.Strings(got)
	want := []string{"async", "crypto", "full", "net", "tls"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Features = %v, want %v", got, want)
	}

	r, err = c.Resolve(NameDebug, "", []string{"full"}, false)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Dropping the default doesn't matter here because "full" already
	// pulls "net"; sanity check the closure is still the same.
	if len(r.Features) != 5 {
		t.Errorf("--no-default-features lost features: %v", r.Features)
	}

	r, err = c.Resolve(NameDebug, "", nil, false)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(r.Features) != 0 {
		t.Errorf("no features requested + no-default, want empty, got %v", r.Features)
	}
}

// TestGoFlagsIncludesFeatureTags asserts feature names produce a
// `-tags=feat_<name>` invocation for the Go backend.
func TestGoFlagsIncludesFeatureTags(t *testing.T) {
	c := Defaults()
	r, err := c.Resolve(NameDebug, "", []string{"alpha", "beta"}, false)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	flags := r.GoFlags()
	seen := false
	for _, f := range flags {
		if f == "-tags=feat_alpha,feat_beta" {
			seen = true
			break
		}
	}
	if !seen {
		t.Errorf("expected -tags=feat_alpha,feat_beta in %v", flags)
	}
}

// TestGoEnvWithTarget covers GOOS/GOARCH/CGO_ENABLED propagation.
func TestGoEnvWithTarget(t *testing.T) {
	c := Defaults()
	cgoOff := false
	c.Targets["amd64-linux"] = &Target{
		Triple: "amd64-linux",
		Arch:   "amd64",
		OS:     "linux",
		CGO:    &cgoOff,
		Env:    map[string]string{"FOO": "bar"},
	}
	r, err := c.Resolve(NameRelease, "amd64-linux", nil, false)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	env := r.GoEnv()
	if env["GOOS"] != "linux" || env["GOARCH"] != "amd64" {
		t.Errorf("target env missing: %+v", env)
	}
	if env["CGO_ENABLED"] != "0" {
		t.Errorf("CGO_ENABLED = %q, want 0", env["CGO_ENABLED"])
	}
	if env["FOO"] != "bar" {
		t.Errorf("user env lost: %+v", env)
	}
}

// TestArtifactKey verifies the file-system naming helper.
func TestArtifactKey(t *testing.T) {
	if got := ArtifactKey("debug", ""); got != "debug" {
		t.Errorf("ArtifactKey(debug,'') = %q", got)
	}
	if got := ArtifactKey("release", "amd64-linux"); got != "release-amd64-linux" {
		t.Errorf("ArtifactKey(release,amd64-linux) = %q", got)
	}
}
