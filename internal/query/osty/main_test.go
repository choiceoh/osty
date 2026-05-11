package osty

import (
	"fmt"
	"os"
	"testing"

	"github.com/osty/osty/internal/check"
)

// TestMain routes every test in this package through the production
// managed-subprocess checker. queries.go calls check.Package and
// check.PackageGraph from inside the LSP-style query layer, so this
// aligns query tests with the production checker selection.
func TestMain(m *testing.M) {
	binPath, err := check.BuildSharedNativeCheckerForTests()
	if err != nil {
		fmt.Fprintf(os.Stderr, "internal/query/osty TestMain: %v\n", err)
		os.Exit(1)
	}
	check.InstallSubprocessCheckerForPackageTests(binPath)
	os.Exit(m.Run())
}
