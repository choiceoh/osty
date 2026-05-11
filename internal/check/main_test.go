package check

import (
	"fmt"
	"os"
	"testing"
)

// TestMain installs the managed-subprocess checker for the whole test
// binary so internal/check tests exercise the same factory path that
// runs in production.
//
// After gate (b-hard) removed the embedded default from
// productionNativeCheckerFactory, every test that calls Package / Workspace
// / PackageGraph / SelfhostFile / NativePackageCheck needs an explicit
// checker installation; doing it once here covers all current and future
// tests in this package.
//
// The specialised selector tests in native_checker_selector_test.go and
// testsupport_test.go install their own factories with t.Cleanup, which
// transparently overrides this default for those subtests.
func TestMain(m *testing.M) {
	binPath, err := BuildSharedNativeCheckerForTests()
	if err != nil {
		fmt.Fprintf(os.Stderr, "internal/check TestMain: %v\n", err)
		os.Exit(1)
	}
	InstallSubprocessCheckerForPackageTests(binPath)
	os.Exit(m.Run())
}
