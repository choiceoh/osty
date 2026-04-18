package backend

import (
	"os"
	"testing"
)

func TestStdlibBodyLoweringEnabledOff(t *testing.T) {
	cases := []string{"", "0", "false", "off"}
	for _, v := range cases {
		t.Run("OSTY_STDLIB_BODY_LOWER="+v, func(t *testing.T) {
			t.Setenv("OSTY_STDLIB_BODY_LOWER", v)
			if stdlibBodyLoweringEnabled() {
				t.Fatalf("enabled for %q, want disabled", v)
			}
		})
	}
}

func TestStdlibBodyLoweringEnabledOn(t *testing.T) {
	cases := []string{"1", "true", "yes", "on"}
	for _, v := range cases {
		t.Run("OSTY_STDLIB_BODY_LOWER="+v, func(t *testing.T) {
			t.Setenv("OSTY_STDLIB_BODY_LOWER", v)
			if !stdlibBodyLoweringEnabled() {
				t.Fatalf("disabled for %q, want enabled", v)
			}
		})
	}
}

func TestStdlibBodyLoweringEnabledDefaultOff(t *testing.T) {
	// Sanity guard: unset env yields off. Running this test in a shell
	// that exports the flag would make it fail, which is the correct
	// behavior — "default" means unset.
	os.Unsetenv("OSTY_STDLIB_BODY_LOWER")
	if stdlibBodyLoweringEnabled() {
		t.Fatalf("unset env yields enabled, want disabled default")
	}
}
