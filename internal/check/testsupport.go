package check

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

// Test helpers for installing the LLVM-built `osty-native-checker` subprocess
// in tests that route through `check.NativePackageCheck` / the package-check
// factory (same boundary as production after `UseManagedSubprocessChecker`).
//
// Migration recipe:
//
//  1. Add a TestMain to the test package (or reuse an existing one) that
//     calls BuildSharedNativeCheckerForTests once and installs it via
//     InstallSubprocessCheckerForPackageTests.
//  2. For tests that need per-test isolation (e.g. multiple checker
//     configurations in one package), call UseSubprocessCheckerForTest
//     inside the test body instead.
//  3. Tests using either helper must NOT call t.Parallel() — the factory
//     is a package var and concurrent swaps would race.
//
// Why this exists: the production CLI installs the managed subprocess at
// startup (`cmd/osty/main.go` calls `UseManagedSubprocessChecker`). Test
// binaries do not run `main`, so packages that exercise the factory need an
// explicit install — typically once per test binary from TestMain. See
// SUBPROCESS_SWITCHOVER.md gate (b-hard) migration history.

var (
	sharedTestCheckerOnce sync.Once
	sharedTestCheckerPath string
	sharedTestCheckerErr  error
)

// BuildSharedNativeCheckerForTests builds cmd/osty-native-checker into a
// temp directory exactly once per test binary invocation and returns its
// absolute path. Subsequent calls reuse the same binary — the typical
// pattern is one TestMain build shared by every test in the package.
//
// Returns a non-nil error only when `go build` itself fails (missing
// toolchain, broken sources). The temp dir is left behind on success so
// the OS can reclaim it after the test binary exits.
func BuildSharedNativeCheckerForTests() (string, error) {
	sharedTestCheckerOnce.Do(func() {
		tmp, err := os.MkdirTemp("", "osty-test-native-checker-*")
		if err != nil {
			sharedTestCheckerErr = fmt.Errorf("create temp dir: %w", err)
			return
		}
		binName := "osty-native-checker"
		if runtime.GOOS == "windows" {
			binName += ".exe"
		}
		path := filepath.Join(tmp, binName)
		cmd := exec.Command("go", "build", "-o", path, "github.com/osty/osty/cmd/osty-native-checker")
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			sharedTestCheckerErr = fmt.Errorf("go build osty-native-checker: %w", err)
			return
		}
		sharedTestCheckerPath = path
	})
	if sharedTestCheckerErr != nil {
		return "", sharedTestCheckerErr
	}
	return sharedTestCheckerPath, nil
}

// InstallSubprocessCheckerForPackageTests swaps the process-wide check
// factory to use the managed subprocess at binPath for the lifetime of the
// test binary. Designed for use from TestMain — no automatic restore.
//
// Pair with BuildSharedNativeCheckerForTests:
//
//	func TestMain(m *testing.M) {
//	    binPath, err := check.BuildSharedNativeCheckerForTests()
//	    if err != nil {
//	        fmt.Fprintln(os.Stderr, err)
//	        os.Exit(1)
//	    }
//	    check.InstallSubprocessCheckerForPackageTests(binPath)
//	    os.Exit(m.Run())
//	}
func InstallSubprocessCheckerForPackageTests(binPath string) {
	nativeCheckerFactory = func() (nativeChecker, string) {
		return nativeCheckerExec{path: binPath}, ""
	}
}

// UseSubprocessCheckerForTest swaps the factory to the managed subprocess
// at binPath for the duration of the calling test, restoring the previous
// factory on Cleanup. Use when a single test needs the subprocess path
// while sibling tests in the same package keep their existing behaviour.
//
// Tests calling this must NOT use t.Parallel() — the factory is a package
// var and concurrent swaps would race.
func UseSubprocessCheckerForTest(t *testing.T, binPath string) {
	t.Helper()
	original := nativeCheckerFactory
	t.Cleanup(func() { nativeCheckerFactory = original })
	nativeCheckerFactory = func() (nativeChecker, string) {
		return nativeCheckerExec{path: binPath}, ""
	}
}
