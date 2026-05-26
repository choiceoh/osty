package backend

import (
	"strings"
	"testing"
)

// TestRequireRealLLVMEmissionStrictEnvOff confirms the default
// behavior: when OSTY_REQUIRE_REAL_LLVM_EMISSION is unset (the
// local-dev case), requireRealLLVMEmission still SKIPs when
// osty-self is unreachable. Without this guarantee, every fresh
// clone would fail the gated tests before the user has a chance
// to run `osty build toolchain/`.
func TestRequireRealLLVMEmissionStrictEnvOff(t *testing.T) {
	t.Setenv(requireRealLLVMEmissionStrictEnv, "")
	if requireRealLLVMEmissionStrict() {
		t.Fatal("strict() returned true with env unset")
	}
}

// TestRequireRealLLVMEmissionStrictEnvOn flips the env var and
// confirms strict mode activates. CI workflows that bootstrap
// osty-self before running tests should set this to FAIL on
// missing artifact instead of silently skipping — the gate hole
// reviewer-flagged after PR #1998's regression hid for 14 PRs.
func TestRequireRealLLVMEmissionStrictEnvOn(t *testing.T) {
	for _, val := range []string{"1", "true", "TRUE", "yes", "YES", "on", "ON"} {
		t.Run(val, func(t *testing.T) {
			t.Setenv(requireRealLLVMEmissionStrictEnv, val)
			if !requireRealLLVMEmissionStrict() {
				t.Fatalf("strict() returned false for env %q", val)
			}
		})
	}
}

// TestRequireRealLLVMEmissionStrictEnvIgnoresOtherValues confirms
// arbitrary non-truthy values DON'T activate strict mode, so a
// stray export of `OSTY_REQUIRE_REAL_LLVM_EMISSION=0` or
// `=false` won't accidentally make tests skip when they should fail.
// Mirrors the asymmetry in Go's `strconv.ParseBool` but limited to
// the explicit accept-list above for stability.
func TestRequireRealLLVMEmissionStrictEnvIgnoresOtherValues(t *testing.T) {
	for _, val := range []string{"0", "false", "no", "off", "anything", " 1 "} {
		t.Run(strings.TrimSpace(val), func(t *testing.T) {
			t.Setenv(requireRealLLVMEmissionStrictEnv, val)
			if requireRealLLVMEmissionStrict() {
				t.Fatalf("strict() returned true for non-truthy env %q", val)
			}
		})
	}
}
