package backend

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsOstySelfMissingDetectsSubprocessSignal(t *testing.T) {
	t.Parallel()
	canonical := errors.New("osty-self not found; run `osty build toolchain/` or set OSTY_SELF_BIN")

	cases := []struct {
		name string
		ws   []error
		want bool
	}{
		{"empty", nil, false},
		{"empty-slice", []error{}, false},
		{"unrelated", []error{errors.New("MIR payload requires the Osty-owned LIR Proto backend")}, false},
		{"nil-entry", []error{nil, errors.New("noise")}, false},
		{"canonical", []error{canonical}, true},
		{"with-prefix", []error{fmt.Errorf("declined: %s", canonical.Error())}, true},
		{"wrapped", []error{fmt.Errorf("native llvmgen: %w", canonical)}, true},
		{"mixed", []error{errors.New("noise"), nil, canonical, errors.New("more noise")}, true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := IsOstySelfMissing(c.ws); got != c.want {
				t.Fatalf("IsOstySelfMissing(%v) = %v, want %v", c.ws, got, c.want)
			}
		})
	}
}

func TestShouldUseStage0BootstrapFallbackDetectsPartialOstySelf(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ws   []error
		want bool
	}{
		{"empty", nil, false},
		{"missing", []error{errors.New("osty-self not found; run `osty build toolchain/`")}, true},
		{"partial-stage0", []error{errors.New("native LIR Proto subprocess declined: osty-self: stage0 declined function: mirJsonParseValue")}, true},
		{"unrelated", []error{errors.New("MIR payload requires the Osty-owned LIR Proto backend")}, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := ShouldUseStage0BootstrapFallback(c.ws); got != c.want {
				t.Fatalf("ShouldUseStage0BootstrapFallback(%v) = %v, want %v", c.ws, got, c.want)
			}
		})
	}
}
