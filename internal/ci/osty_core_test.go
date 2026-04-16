package ci

import "testing"

func TestToolchainCoreDefaultOptions(t *testing.T) {
	opts := DefaultOptions()
	if !opts.Format || !opts.Lint || !opts.Policy || !opts.Lockfile {
		t.Fatalf("default quick bundle disabled: %+v", opts)
	}
	if opts.Release || opts.Semver || opts.Strict {
		t.Fatalf("default opt-in checks unexpectedly enabled: %+v", opts)
	}
	r := NewRunner(".", opts)
	if r.Opts.MaxFileBytes != 1<<20 {
		t.Fatalf("default max file bytes = %d, want %d", r.Opts.MaxFileBytes, 1<<20)
	}
}

func TestToolchainCorePolicyManifestFields(t *testing.T) {
	ds := ciPolicyManifestFieldsCore(&CiManifestCore{
		hasPackage: true,
		name:       "Osty",
	})
	codes := diagCodes(ds)
	want := []string{"CI102", "CI103", "CI104", "CI105", "CI106"}
	if len(codes) != len(want) {
		t.Fatalf("codes = %v, want %v", codes, want)
	}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("codes = %v, want %v", codes, want)
		}
	}
}

func TestToolchainCoreReleaseAndSemver(t *testing.T) {
	ds := ciReleaseManifestCore(&CiReleaseCore{
		hasManifest: true,
		hasPackage:  true,
		version:     "1.02.0",
		dependencies: []*CiDependencyCore{
			{name: "local", path: "../local"},
			{name: "branchy", hasGit: true},
		},
	})
	codes := diagCodes(ds)
	want := []string{"CI304", "CI305", "CI306", "CI307"}
	if len(codes) != len(want) {
		t.Fatalf("codes = %v, want %v", codes, want)
	}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("codes = %v, want %v", codes, want)
		}
	}

	if !ciMajorBumped("1.2.3", "2.0.0") {
		t.Fatal("expected major bump")
	}
	if ciMajorBumped("1.2.3", "1.9.0") {
		t.Fatal("minor bump counted as major bump")
	}
}

func diagCodes(ds []*CiDiagCore) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.code)
	}
	return out
}
