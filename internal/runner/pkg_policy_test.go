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
