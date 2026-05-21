package backend

import (
	"errors"
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

// requireRealLLVMEmission skips the test unless an `osty-self` binary is
// reachable through `selfhostcache.ResolveBinary` — the same lookup the
// LIR Proto subprocess uses at runtime (env override → in-tree build →
// content-addressed cache). The MIR-direct path bottoms out at
// `osty-native-lirproto`, which forks `osty-self lir-proto-lower`; without
// that artifact the subprocess declines and tests that assert specific
// runtime symbols or optimisations cannot succeed.
func requireRealLLVMEmission(t *testing.T) {
	t.Helper()
	root, err := selfhostcache.LocateProjectRoot(".")
	if err != nil {
		t.Skipf("requires osty-self artifact (locate project root: %v)", err)
	}
	if _, _, err := selfhostcache.ResolveBinary(root); err != nil {
		if errors.Is(err, selfhostcache.ErrNotCached) {
			t.Skip("requires osty-self artifact (run `osty build toolchain/`); LIR Proto subprocess otherwise declines")
		}
		t.Skipf("requires osty-self artifact (resolve: %v)", err)
	}
}
