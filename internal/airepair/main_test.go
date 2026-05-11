package airepair

import (
	"fmt"
	"os"
	"testing"

	"github.com/osty/osty/internal/check"
)

// TestMain routes every test in this package through the production
// managed-subprocess checker. airepair/semantic.go calls check.SelfhostFile
// while running its semantic-fixup loop, so this aligns those test
// invocations with what `osty airepair` ships in production.
func TestMain(m *testing.M) {
	binPath, err := check.BuildSharedNativeCheckerForTests()
	if err != nil {
		fmt.Fprintf(os.Stderr, "internal/airepair TestMain: %v\n", err)
		os.Exit(1)
	}
	check.InstallSubprocessCheckerForPackageTests(binPath)
	os.Exit(m.Run())
}
