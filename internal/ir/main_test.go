package ir

import (
	"fmt"
	"os"
	"testing"

	"github.com/osty/osty/internal/check"
)

// TestMain routes every test in this package through the production
// managed-subprocess checker instead of the embedded default. This is
// the first incremental migration toward gate (b-hard) in
// SUBPROCESS_SWITCHOVER.md (deleting embeddedNativeChecker entirely).
//
// The osty-native-checker binary is built once per `go test` invocation
// via check.BuildSharedNativeCheckerForTests; every per-test factory
// lookup forks a subprocess thereafter. Net cost on this package is on
// the order of a few hundred milliseconds plus the one-time build.
func TestMain(m *testing.M) {
	binPath, err := check.BuildSharedNativeCheckerForTests()
	if err != nil {
		fmt.Fprintf(os.Stderr, "internal/ir TestMain: %v\n", err)
		os.Exit(1)
	}
	check.InstallSubprocessCheckerForPackageTests(binPath)
	os.Exit(m.Run())
}
