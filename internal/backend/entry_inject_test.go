package backend

import (
	"os"
	"testing"
)

func TestStdlibBodyLoweringEnabledOff(t *testing.T) {
	cases := []string{"0", "false", "off"}
	for _, v := range cases {
		t.Run("OSTY_STDLIB_BODY_LOWER="+v, func(t *testing.T) {
			t.Setenv("OSTY_STDLIB_BODY_LOWER", v)
			if stdlibBodyLoweringEnabled() {
				t.Fatalf("enabled for %q, want disabled (escape hatch)", v)
			}
		})
	}
}

func TestStdlibBodyLoweringEnabledOn(t *testing.T) {
	// Legacy `=1` invocation remains enabled after the default-on flip.
	t.Setenv("OSTY_STDLIB_BODY_LOWER", "1")
	if !stdlibBodyLoweringEnabled() {
		t.Fatalf("disabled for legacy =1, want enabled")
	}
}

func TestStdlibBodyLoweringEnabledDefaultOn(t *testing.T) {
	// Sanity guard: unset env yields ON (PR3-C default flip). Running
	// this test in a shell that exports `OSTY_STDLIB_BODY_LOWER=0`
	// would make it fail, which is the correct behavior — "default"
	// means unset.
	//
	// Save/restore the env so any caller that exports the flag (e.g.
	// a developer running `go test` from a shell with the escape
	// hatch set) sees their environment intact after this test; the
	// pre-PR #1999 form dropped the value for the rest of the run.
	prev, hadPrev := os.LookupEnv("OSTY_STDLIB_BODY_LOWER")
	os.Unsetenv("OSTY_STDLIB_BODY_LOWER")
	t.Cleanup(func() {
		if hadPrev {
			os.Setenv("OSTY_STDLIB_BODY_LOWER", prev)
		} else {
			os.Unsetenv("OSTY_STDLIB_BODY_LOWER")
		}
	})
	if !stdlibBodyLoweringEnabled() {
		t.Fatalf("unset env yields disabled, want enabled default (post-flip)")
	}
}
