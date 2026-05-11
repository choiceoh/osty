package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/osty/osty/internal/check"
)

// TestMain installs the managed-subprocess checker for the whole test
// binary so cmd/osty tests exercise the same factory path that runs in
// production. The production main() also calls UseManagedSubprocessChecker
// but main() never runs from inside `go test`, so we install it here.
//
// Part of the gate (b-hard) test migration tracked in SUBPROCESS_SWITCHOVER.md.
func TestMain(m *testing.M) {
	binPath, err := check.BuildSharedNativeCheckerForTests()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cmd/osty TestMain: %v\n", err)
		os.Exit(1)
	}
	check.InstallSubprocessCheckerForPackageTests(binPath)
	os.Exit(m.Run())
}
