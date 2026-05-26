package backend

import (
	"errors"
	"os"
	"testing"

	"github.com/osty/osty/internal/toolchain/selfhostcache"
)

// installNativeMIRPayloadStub installs a `tryNativeOwnedMIRPayloadLLVMIRText`
// override that always reports coverage with a trivial-but-valid IR shell.
// Backend tests that only verify dispatch/build flow (succeed → tc records
// compile/link calls; warnings carry front-end diagnostics) opt in via this
// helper instead of needing a real `osty-self` artifact wired into the LIR
// Proto subprocess chain. Cleanup restores the original emitter.
func installNativeMIRPayloadStub(t *testing.T) {
	t.Helper()
	withNativeMIRPayloadEmitter(t, func(entry Entry, target string) ([]byte, bool, []error, error) {
		return []byte("; stub native MIR payload IR\nsource_filename = \"stub.osty\"\n"), true, nil, nil
	})
}

// installNativeMIRPayloadDeclineStub installs an override that always
// declines, so dispatcher fall-through paths can be exercised
// deterministically regardless of whether osty-self is built.
func installNativeMIRPayloadDeclineStub(t *testing.T) {
	t.Helper()
	withNativeMIRPayloadEmitter(t, func(Entry, string) ([]byte, bool, []error, error) {
		return nil, false, nil, nil
	})
}

// requireRealLLVMEmissionStrictEnv is the env var that flips
// `requireRealLLVMEmission` from skip-on-missing to fail-on-missing.
// CI / preflight bootstrap scripts should set this AFTER ensuring
// osty-self is built (e.g. after `just bootstrap` succeeds) so any
// regression in the MIR-direct emit path surfaces as a test failure
// instead of an invisible skip.
//
// Without this env var, local dev runs continue to see clean skips
// when osty-self isn't cached — preserves the fresh-clone-friendly
// behavior `t.Skip` was added for in PR #1975.
const requireRealLLVMEmissionStrictEnv = "OSTY_REQUIRE_REAL_LLVM_EMISSION"

// requireRealLLVMEmission skips the test unless an `osty-self` binary is
// reachable through `selfhostcache.ResolveBinary` — the same lookup the
// LIR Proto subprocess uses at runtime (env override → in-tree build →
// content-addressed cache). The MIR-direct path bottoms out at
// `osty-native-lirproto`, which forks `osty-self lir-proto-lower`; without
// that artifact the subprocess declines and tests that assert specific
// runtime symbols or optimisations cannot succeed.
//
// When `OSTY_REQUIRE_REAL_LLVM_EMISSION=1` is set (CI mode), the same
// missing-artifact condition FAILS the test instead of skipping. This
// closes the gate hole reviewer-flagged after PR #1998's silent
// regression: tests with no osty-self skipped silently in CI, hiding
// the regression that broke the bootstrap until manual users hit it.
// With the strict env var set, CI catches MIR-direct breakage on the
// introducing PR.
func requireRealLLVMEmission(t *testing.T) {
	t.Helper()
	strict := requireRealLLVMEmissionStrict()
	failOrSkip := func(format string, args ...any) {
		if strict {
			t.Fatalf(format, args...)
		} else {
			t.Skipf(format, args...)
		}
	}
	root, err := selfhostcache.LocateProjectRoot(".")
	if err != nil {
		failOrSkip("requires osty-self artifact (locate project root: %v)", err)
		return
	}
	if _, _, err := selfhostcache.ResolveBinary(root); err != nil {
		if errors.Is(err, selfhostcache.ErrNotCached) {
			if strict {
				t.Fatal("requires osty-self artifact (run `osty build toolchain/`); LIR Proto subprocess otherwise declines")
			} else {
				t.Skip("requires osty-self artifact (run `osty build toolchain/`); LIR Proto subprocess otherwise declines")
			}
			return
		}
		failOrSkip("requires osty-self artifact (resolve: %v)", err)
	}
}

func requireRealLLVMEmissionStrict() bool {
	switch os.Getenv(requireRealLLVMEmissionStrictEnv) {
	case "1", "true", "TRUE", "yes", "YES", "on", "ON":
		return true
	}
	return false
}
