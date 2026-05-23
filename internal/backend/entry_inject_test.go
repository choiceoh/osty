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
	// Post-flip: any value other than the explicit off-set leaves the
	// gate ON. We still keep an "any truthy spelling" case so users who
	// migrate from the old `=1` invocation see no behavior change.
	cases := []string{"", "1", "true", "yes", "on"}
	for _, v := range cases {
		t.Run("OSTY_STDLIB_BODY_LOWER="+v, func(t *testing.T) {
			t.Setenv("OSTY_STDLIB_BODY_LOWER", v)
			if !stdlibBodyLoweringEnabled() {
				t.Fatalf("disabled for %q, want enabled", v)
			}
		})
	}
}

func TestStdlibBodyLoweringEnabledDefaultOn(t *testing.T) {
	// Sanity guard: unset env yields ON (PR3-C default flip). Running
	// this test in a shell that exports `OSTY_STDLIB_BODY_LOWER=0`
	// would make it fail, which is the correct behavior — "default"
	// means unset.
	os.Unsetenv("OSTY_STDLIB_BODY_LOWER")
	if !stdlibBodyLoweringEnabled() {
		t.Fatalf("unset env yields disabled, want enabled default (post-flip)")
	}
}
