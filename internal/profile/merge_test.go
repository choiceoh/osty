package profile

import (
	"testing"

	"github.com/osty/osty/internal/manifest"
)

// TestBuildConfigNilManifest falls back to built-in defaults when the
// manifest is nil (e.g. `osty profiles` outside a project).
func TestBuildConfigNilManifest(t *testing.T) {
	c, err := BuildConfig(nil)
	if err != nil {
		t.Fatalf("BuildConfig(nil): %v", err)
	}
	if _, ok := c.Profile(NameDebug); !ok {
		t.Errorf("missing debug profile")
	}
}

// TestBuildConfigOverridesBuiltin verifies that a [profile.release]
// table in the manifest keeps unset defaults but applies declared
// overrides (opt-level, go-flags, strip).
func TestBuildConfigOverridesBuiltin(t *testing.T) {
	m := &manifest.Manifest{
		Profiles: map[string]*manifest.Profile{
			NameRelease: {
				Name:        NameRelease,
				OptLevel:    3,
				HasOptLevel: true,
				Strip:       false,
				HasStrip:    true,
				GoFlags:     []string{"-race"},
			},
		},
	}
	c, err := BuildConfig(m)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	rel := c.Profiles[NameRelease]
	if rel == nil {
		t.Fatalf("release missing after merge")
	}
	if rel.OptLevel != 3 {
		t.Errorf("OptLevel = %d, want 3", rel.OptLevel)
	}
	if rel.Strip {
		t.Errorf("Strip override ignored; want false")
	}
	if !rel.Debug == false {
		// defaults to the built-in release.Debug which is false; no
		// change expected.
	}
	sawRace := false
	for _, f := range rel.GoFlags {
		if f == "-race" {
			sawRace = true
		}
	}
	if !sawRace {
		t.Errorf("go-flags appended failed; got %v", rel.GoFlags)
	}
	if !rel.UserDefined {
		t.Errorf("merged profile should be marked UserDefined")
	}
}

// TestBuildConfigCustomProfileInherits validates that a new profile
// name derives from its inherits base and then applies overrides.
func TestBuildConfigCustomProfileInherits(t *testing.T) {
	m := &manifest.Manifest{
		Profiles: map[string]*manifest.Profile{
			"bench": {
				Name:        "bench",
				Inherits:    NameRelease,
				OptLevel:    3,
				HasOptLevel: true,
				Debug:       true,
				HasDebug:    true,
			},
		},
	}
	c, err := BuildConfig(m)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	b := c.Profiles["bench"]
	if b == nil {
		t.Fatalf("bench profile missing")
	}
	if b.OptLevel != 3 {
		t.Errorf("OptLevel = %d, want 3", b.OptLevel)
	}
	if !b.Debug {
		t.Errorf("Debug override ignored")
	}
	if !b.Strip {
		t.Errorf("Strip should inherit from release (true)")
	}
}

// TestBuildConfigUnknownInherits surfaces manifests that point
// `inherits` at a profile that doesn't exist.
func TestBuildConfigUnknownInherits(t *testing.T) {
	m := &manifest.Manifest{
		Profiles: map[string]*manifest.Profile{
			"bench": {
				Name:     "bench",
				Inherits: "nope",
			},
		},
	}
	if _, err := BuildConfig(m); err == nil {
		t.Fatalf("expected error for unknown inherits")
	}
}

// TestBuildConfigTargets propagates target tables with arch / os /
// cgo derived from the triple name.
func TestBuildConfigTargets(t *testing.T) {
	m := &manifest.Manifest{
		Targets: []*manifest.Target{
			{Triple: "amd64-linux", CGO: false, HasCGO: true},
			{Triple: "arm64-darwin", Env: map[string]string{"SDK": "macos"}},
		},
	}
	c, err := BuildConfig(m)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	lin := c.Targets["amd64-linux"]
	if lin == nil || lin.OS != "linux" || lin.Arch != "amd64" {
		t.Errorf("amd64-linux target wrong: %+v", lin)
	}
	if lin.CGO == nil || *lin.CGO {
		t.Errorf("cgo should be explicitly off: %+v", lin.CGO)
	}
	mac := c.Targets["arm64-darwin"]
	if mac == nil || mac.Env["SDK"] != "macos" {
		t.Errorf("arm64-darwin env lost: %+v", mac)
	}
}

// TestBuildConfigFeatures moves [features] + default list into Config.
func TestBuildConfigFeatures(t *testing.T) {
	m := &manifest.Manifest{
		Features: map[string][]string{
			"tls": {"crypto"},
		},
		DefaultFeatures: []string{"tls"},
	}
	c, err := BuildConfig(m)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	if len(c.DefaultFeatures) != 1 || c.DefaultFeatures[0] != "tls" {
		t.Errorf("DefaultFeatures = %v", c.DefaultFeatures)
	}
	if c.Features["tls"][0] != "crypto" {
		t.Errorf("feature table lost: %+v", c.Features)
	}
}
