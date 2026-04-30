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
		if module == "keychain" && len(chk.Types) == 0 {
			t.Fatalf("stdlibCheckResult(%s) recorded no expression types", module)
		}
	}
}
