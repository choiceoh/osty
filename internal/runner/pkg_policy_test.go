package runner

import "testing"

func TestExpandFrozenFlags(t *testing.T) {
	cases := []struct {
		name string
		in   ResolveOpts
		want ResolveOpts
	}{
		{
			name: "frozen-promotes-to-all-three",
			in:   ResolveOpts{Frozen: true},
			want: ResolveOpts{Offline: true, Locked: true, Frozen: true},
		},
		{
			name: "frozen-already-with-offline-locked",
			in:   ResolveOpts{Offline: true, Locked: true, Frozen: true},
			want: ResolveOpts{Offline: true, Locked: true, Frozen: true},
		},
		{
			name: "non-frozen-preserved",
			in:   ResolveOpts{Offline: false, Locked: true, Frozen: false},
			want: ResolveOpts{Offline: false, Locked: true, Frozen: false},
		},
		{
			name: "all-off-preserved",
			in:   ResolveOpts{},
			want: ResolveOpts{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExpandFrozenFlags(c.in)
			if got != c.want {
				t.Errorf("ExpandFrozenFlags(%+v) = %+v, want %+v", c.in, got, c.want)
			}
		})
	}
}

func TestFrozenMissingLockfileMessage(t *testing.T) {
	got := FrozenMissingLockfileMessage("osty.lock")
	want := "--frozen requires an existing osty.lock; run `osty update` first"
	if got != want {
		t.Errorf("FrozenMissingLockfileMessage = %q, want %q", got, want)
	}
}

func TestLockedDiffMessageEmptyChanges(t *testing.T) {
	if got := LockedDiffMessage("osty.lock", nil); got != "" {
		t.Errorf("LockedDiffMessage(nil) = %q, want empty", got)
	}
	if got := LockedDiffMessage("osty.lock", []string{}); got != "" {
		t.Errorf("LockedDiffMessage([]) = %q, want empty", got)
	}
}

func TestLockedDiffMessageFormatsChanges(t *testing.T) {
	changes := []string{"+ foo 1.0.0", "- bar 2.1.3", "~ baz 0.9 -> 1.0"}
	got := LockedDiffMessage("osty.lock", changes)
	want := "--locked: osty.lock would change:\n  + foo 1.0.0\n  - bar 2.1.3\n  ~ baz 0.9 -> 1.0\nrerun without --locked to update the lockfile."
	if got != want {
		t.Errorf("LockedDiffMessage =\n%q\nwant\n%q", got, want)
	}
}

func TestPkgSkipDirEntry(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"dot-kept", ".", false},
		{"osty-cache-root", ".osty", true},
		{"osty-cache-nested", "sub/.osty", true},
		{"path-inside-osty-not-skipped", ".osty/cache/x", false},
		{"git", ".git", true},
		{"hg", ".hg", true},
		{"svn", ".svn", true},
		{"ds-store", ".DS_Store", true},
		{"ds-store-nested", "sub/.DS_Store", true},
		{"normal-source", "src/main.osty", false},
		{"normal-readme", "README.md", false},
		{"backslash-git", "sub\\.git", true},
		{"backslash-normal", "sub\\src\\main.osty", false},
		{"trailing-slash-git", "sub/.git/", true},
		{"trailing-backslash-git", "sub\\.git\\", true},
		{"trailing-slash-osty", ".osty/", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PkgSkipDirEntry(c.in); got != c.want {
				t.Errorf("PkgSkipDirEntry(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestPkgSourceKindString(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want string
	}{
		{"path", PkgSourceKindPath, "path"},
		{"git", PkgSourceKindGit, "git"},
		{"registry", PkgSourceKindRegistry, "registry"},
		{"unknown-positive", 99, "unknown"},
		{"unknown-negative", -1, "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PkgSourceKindString(c.in); got != c.want {
				t.Errorf("PkgSourceKindString(%d) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestPkgSourceKindConstantsLiteralValues(t *testing.T) {
	// Mirror the literal-value assertion that runs on the Osty side
	// (toolchain/pkg_policy_test.osty). The compile-time guards in
	// internal/pkgmgr/source.go would block a build that drifts, but
	// these run pre-build during `go test` so CI can name the regression.
	if PkgSourceKindPath != 0 {
		t.Errorf("PkgSourceKindPath = %d, want 0", PkgSourceKindPath)
	}
	if PkgSourceKindGit != 1 {
		t.Errorf("PkgSourceKindGit = %d, want 1", PkgSourceKindGit)
	}
	if PkgSourceKindRegistry != 2 {
		t.Errorf("PkgSourceKindRegistry = %d, want 2", PkgSourceKindRegistry)
	}
}

func TestPkgSourceKindStringByLiteralValue(t *testing.T) {
	// Hit the mapping with literal ints — independent of the named
	// constants — so a `PkgSourceKindPath = 1` typo can't hide.
	if got := PkgSourceKindString(0); got != "path" {
		t.Errorf("PkgSourceKindString(0) = %q, want %q", got, "path")
	}
	if got := PkgSourceKindString(1); got != "git" {
		t.Errorf("PkgSourceKindString(1) = %q, want %q", got, "git")
	}
	if got := PkgSourceKindString(2); got != "registry" {
		t.Errorf("PkgSourceKindString(2) = %q, want %q", got, "registry")
	}
}
