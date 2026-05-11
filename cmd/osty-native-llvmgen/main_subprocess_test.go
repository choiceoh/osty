package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/osty/osty/internal/check"
)

// TestMain installs the managed-subprocess checker for the test binary.
// main_test.go uses check.SelfhostFile via the same factory path the
// production binary exercises after main() calls
// UseManagedSubprocessChecker.
//
// Part of the gate (b-hard) test migration tracked in SUBPROCESS_SWITCHOVER.md.
func TestMain(m *testing.M) {
	binPath, err := check.BuildSharedNativeCheckerForTests()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cmd/osty-native-llvmgen TestMain: %v\n", err)
		os.Exit(1)
	}
	check.InstallSubprocessCheckerForPackageTests(binPath)
	os.Exit(m.Run())
}
