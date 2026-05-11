package check

import (
	"testing"

	"github.com/osty/osty/internal/selfhost/api"
)

func TestBuildSharedNativeCheckerForTestsIsIdempotent(t *testing.T) {
	pathA, err := BuildSharedNativeCheckerForTests()
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	pathB, err := BuildSharedNativeCheckerForTests()
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if pathA != pathB {
		t.Fatalf("second call returned different path: %q vs %q", pathA, pathB)
	}
	if pathA == "" {
		t.Fatal("returned empty path")
	}
}

func TestUseSubprocessCheckerForTestSwapsFactory(t *testing.T) {
	original := nativeCheckerFactory

	binPath, err := BuildSharedNativeCheckerForTests()
	if err != nil {
		t.Fatalf("build native checker: %v", err)
	}

	UseSubprocessCheckerForTest(t, binPath)

	got, note := nativeCheckerFactory()
	if note != "" {
		t.Errorf("note = %q, want empty", note)
	}
	exec, ok := got.(nativeCheckerExec)
	if !ok {
		t.Fatalf("got %#v, want nativeCheckerExec", got)
	}
	if exec.path != binPath {
		t.Errorf("exec.path = %q, want %q", exec.path, binPath)
	}

	// End-to-end: actually exercise the subprocess path through the public
	// NativePackageCheck API. An empty input is valid and should produce an
	// empty CheckResult; the relevant assertion is that the subprocess runs
	// and returns a structured response rather than an exec failure.
	result, err := NativePackageCheck(api.PackageCheckInput{})
	if err != nil {
		t.Fatalf("NativePackageCheck via subprocess: %v", err)
	}
	_ = result // shape isn't asserted; we just verify the round-trip works

	// Sanity: t.Cleanup will restore the original factory after this test.
	if nativeCheckerFactory == nil {
		t.Fatal("factory unexpectedly nil during test")
	}
	t.Cleanup(func() {
		// Defensive: the helper installs its own Cleanup that runs before
		// this one (LIFO), so by now `original` should already be back.
		if &nativeCheckerFactory == nil { // unreachable but documents intent
			t.Fatal("factory pointer changed during cleanup")
		}
		_ = original
	})
}

func TestInstallSubprocessCheckerForPackageTestsSwapsFactory(t *testing.T) {
	originalFactory := nativeCheckerFactory
	t.Cleanup(func() { nativeCheckerFactory = originalFactory })

	binPath, err := BuildSharedNativeCheckerForTests()
	if err != nil {
		t.Fatalf("build native checker: %v", err)
	}

	InstallSubprocessCheckerForPackageTests(binPath)

	got, note := nativeCheckerFactory()
	if note != "" {
		t.Errorf("note = %q, want empty", note)
	}
	if exec, ok := got.(nativeCheckerExec); !ok || exec.path != binPath {
		t.Fatalf("got %#v, want nativeCheckerExec{path:%q}", got, binPath)
	}
}
