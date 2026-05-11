package check

import (
	"os"
	"testing"

	"github.com/osty/osty/internal/selfhost/api"
)

type stubNativeChecker struct{}

func (stubNativeChecker) CheckSourceStructured([]byte) (api.CheckResult, error) {
	return api.CheckResult{}, nil
}

func (stubNativeChecker) CheckPackageStructured(api.PackageCheckInput) (api.CheckResult, error) {
	return api.CheckResult{}, nil
}

func TestDefaultNativeCheckerUsesProductionSelector(t *testing.T) {
	original := productionNativeCheckerFactory
	t.Cleanup(func() {
		productionNativeCheckerFactory = original
	})

	want := stubNativeChecker{}
	productionNativeCheckerFactory = func() (nativeChecker, string) {
		return want, "production"
	}
	t.Setenv(nativeCheckerEnv, "")

	got, note := defaultNativeChecker()
	if note != "production" {
		t.Fatalf("defaultNativeChecker note = %q, want %q", note, "production")
	}
	if got != want {
		t.Fatalf("defaultNativeChecker checker = %#v, want %#v", got, want)
	}
}

func TestUseManagedSubprocessCheckerInstallsFactory(t *testing.T) {
	original := productionNativeCheckerFactory
	t.Cleanup(func() {
		productionNativeCheckerFactory = original
	})
	t.Setenv(nativeCheckerEnv, "")

	UseManagedSubprocessChecker(".")

	got, note := productionNativeCheckerFactory()
	// EnsureNativeChecker may succeed (returns nativeCheckerExec, empty note)
	// or fail (returns embedded with a note) depending on the test cwd's
	// project-root resolution. Both outcomes are valid — the contract is
	// only that the factory is swapped to one that consults the managed
	// path and falls back rather than panicking.
	switch got.(type) {
	case nativeCheckerExec:
		if note != "" {
			t.Errorf("note = %q on success path, want empty", note)
		}
	case embeddedNativeChecker:
		if note == "" {
			t.Errorf("embedded fallback must surface a note explaining why managed checker is unavailable")
		}
	default:
		t.Fatalf("UseManagedSubprocessChecker produced %#v, want nativeCheckerExec or embeddedNativeChecker", got)
	}
}

func TestDefaultNativeCheckerUsesEnvOverride(t *testing.T) {
	original := productionNativeCheckerFactory
	t.Cleanup(func() {
		productionNativeCheckerFactory = original
	})

	called := false
	productionNativeCheckerFactory = func() (nativeChecker, string) {
		called = true
		return stubNativeChecker{}, "production"
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv(nativeCheckerEnv, exe)

	got, note := defaultNativeChecker()
	if called {
		t.Fatal("defaultNativeChecker consulted production selector despite explicit override")
	}
	if note != "" {
		t.Fatalf("defaultNativeChecker note = %q, want empty", note)
	}
	if _, ok := got.(nativeCheckerExec); !ok {
		t.Fatalf("defaultNativeChecker checker = %#v, want nativeCheckerExec", got)
	}
}
