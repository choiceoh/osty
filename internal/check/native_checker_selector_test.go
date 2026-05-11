package check

import (
	"os"
	"strings"
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

type stubPackageChecker struct {
	onCheck func()
}

func (s stubPackageChecker) CheckSourceStructured([]byte) (api.CheckResult, error) {
	return api.CheckResult{}, nil
}

func (s stubPackageChecker) CheckPackageStructured(api.PackageCheckInput) (api.CheckResult, error) {
	if s.onCheck != nil {
		s.onCheck()
	}
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

func TestNativePackageCheckRoutesThroughFactory(t *testing.T) {
	original := nativeCheckerFactory
	t.Cleanup(func() { nativeCheckerFactory = original })

	called := false
	nativeCheckerFactory = func() (nativeChecker, string) {
		return stubPackageChecker{onCheck: func() { called = true }}, ""
	}
	_, err := NativePackageCheck(api.PackageCheckInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("NativePackageCheck did not consult the factory-supplied runner")
	}
}

func TestNativePackageCheckPropagatesUnavailability(t *testing.T) {
	original := nativeCheckerFactory
	t.Cleanup(func() { nativeCheckerFactory = original })

	nativeCheckerFactory = func() (nativeChecker, string) {
		return nil, "OSTY_NATIVE_CHECKER_BIN=\"/missing\" was not found"
	}
	_, err := NativePackageCheck(api.PackageCheckInput{})
	if err == nil {
		t.Fatal("expected error when factory yields nil runner")
	}
	if !strings.Contains(err.Error(), "OSTY_NATIVE_CHECKER_BIN") {
		t.Errorf("error should propagate the factory note; got: %v", err)
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
	// Gate (b) contract: the production factory either resolves the managed
	// subprocess (returns nativeCheckerExec, empty note) or surfaces the
	// build failure as nil + note. It must NEVER silently fall back to the
	// embedded path — that path can only lag behind the live toolchain
	// sources, so silent degradation hides real configuration problems.
	switch got.(type) {
	case nativeCheckerExec:
		if note != "" {
			t.Errorf("note = %q on success path, want empty", note)
		}
	case nil:
		if note == "" {
			t.Errorf("nil runner must come with a diagnostic note explaining why managed checker is unavailable")
		}
	default:
		t.Fatalf("UseManagedSubprocessChecker produced %#v, want nativeCheckerExec or nil (no embedded fallback after gate (b))", got)
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
