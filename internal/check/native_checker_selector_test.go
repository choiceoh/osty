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
