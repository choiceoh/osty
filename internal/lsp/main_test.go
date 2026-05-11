package lsp

import (
	"fmt"
	"os"
	"testing"

	"github.com/osty/osty/internal/check"
)

// TestMain routes every test in this package through the production
// managed-subprocess checker. lsp/server.go is one of the hottest
// production factory consumers (every editor didOpen/didChange routes
// through check.Package / check.PackageGraph), so this migration is
// the highest-value step in gate (b-hard).
func TestMain(m *testing.M) {
	binPath, err := check.BuildSharedNativeCheckerForTests()
	if err != nil {
		fmt.Fprintf(os.Stderr, "internal/lsp TestMain: %v\n", err)
		os.Exit(1)
	}
	check.InstallSubprocessCheckerForPackageTests(binPath)
	os.Exit(m.Run())
}
