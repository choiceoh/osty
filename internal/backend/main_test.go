package backend

import (
	"fmt"
	"os"
	"testing"

	"github.com/osty/osty/internal/check"
)

// TestMain routes every test in this package through the production
// managed-subprocess checker instead of the embedded default. The two
// factory consumers in this package — llvm_test.go and
// llvm_multi_file_e2e_test.go — exercise check.Package /
// check.SelfhostFile, so this migration makes them parity-equivalent to
// the production `osty build` path.
//
// Incremental step in gate (b-hard) per SUBPROCESS_SWITCHOVER.md.
func TestMain(m *testing.M) {
	binPath, err := check.BuildSharedNativeCheckerForTests()
	if err != nil {
		fmt.Fprintf(os.Stderr, "internal/backend TestMain: %v\n", err)
		os.Exit(1)
	}
	check.InstallSubprocessCheckerForPackageTests(binPath)
	os.Exit(m.Run())
}
