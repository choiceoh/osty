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
//
// Also exports `OSTY_NATIVE_CHECKER_BIN` to point at the same Go-built
// checker so subprocess tests that exec `osty` directly (`runOstyCLI`
// in airepair_test.go, the TestCheckCLI* family in check_cmd_test.go, …)
// resolve the checker without re-entering the LLVM build path. Without
// this every subprocess `osty check` in this package fails with
// "managed native checker unavailable: LLVM-built osty-native-checker
// requires a resolvable osty-self" on fresh worktrees post-PR #1954 —
// the env-var precedence in `internal/check.defaultNativeChecker`
// wins over the managed factory, so a single Setenv here unblocks
// every subprocess test at once.
func TestMain(m *testing.M) {
	binPath, err := check.BuildSharedNativeCheckerForTests()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cmd/osty TestMain: %v\n", err)
		os.Exit(1)
	}
	check.InstallSubprocessCheckerForPackageTests(binPath)
	if err := os.Setenv("OSTY_NATIVE_CHECKER_BIN", binPath); err != nil {
		fmt.Fprintf(os.Stderr, "cmd/osty TestMain: setenv OSTY_NATIVE_CHECKER_BIN: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
