package backend

import (
	"testing"

	"github.com/osty/osty/internal/stdlib"
)

func TestStdlibCheckResultKeychainAndSecrets(t *testing.T) {
	reg := stdlib.LoadCached()
	for _, module := range []string{"keychain", "secrets"} {
		chk := stdlibCheckResult(reg, module)
		if chk == nil {
			t.Fatalf("stdlibCheckResult(%s) = nil, want non-nil *check.Result", module)
		}
		// Read from `NativeCheckResult.TypedNodes` post-#1645 — see
		// TestStdlibCheckResultStringsModule for the rationale.
		if module == "keychain" {
			native := chk.NativeCheckResult
			if native == nil || len(native.TypedNodes) == 0 {
				t.Fatalf("stdlibCheckResult(%s) recorded no native typed nodes", module)
			}
		}
	}
}
