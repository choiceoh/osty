package backend

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/osty/osty/internal/llvmabi"
)

// withStage0FallbackOverride installs a hook for `tryStage0Fallback`
// scoped to one test. Tests can return a successful IR or simulate
// stage0 declining.
func withStage0FallbackOverride(t *testing.T, fn func(Entry, llvmabi.Options) ([]byte, error)) {
	t.Helper()
	old := tryStage0Fallback
	tryStage0Fallback = fn
	t.Cleanup(func() { tryStage0Fallback = old })
}

func TestStage0FallbackDisabledByDefault(t *testing.T) {
	if Stage0FallbackEnabled() {
		t.Fatalf("OSTY_STAGE0_FALLBACK must default to off; saw enabled")
	}
}

func TestStage0FallbackEnabledRespectsEnvVar(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", false},
		{"0", false},
		{"false", false},
		{"off", false},
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"On", true},
		{"yes", true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.value, func(t *testing.T) {
			t.Setenv(Stage0FallbackEnv, c.value)
			if got := Stage0FallbackEnabled(); got != c.want {
				t.Fatalf("Stage0FallbackEnabled with %q = %v, want %v", c.value, got, c.want)
			}
		})
	}
}

func TestEmitLLVMFallbackUsesStage0WhenOstySelfMissing(t *testing.T) {
	t.Setenv(Stage0FallbackEnv, "1")
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    println(1)
}
`)

	withNativeMIRPayloadEmitter(t, func(Entry, string) ([]byte, bool, []error, error) {
		return nil, false, []error{errors.New("osty-self not found; run `osty build toolchain/`")}, nil
	})
	stage0Called := false
	withStage0FallbackOverride(t, func(entry Entry, opts llvmabi.Options) ([]byte, error) {
		stage0Called = true
		if entry.MIR == nil {
			t.Fatal("stage0 fallback received nil MIR")
		}
		return []byte("; stage0 fallback IR for test\n"), nil
	})

	got, warnings, err := EmitLLVMIRText(req.Entry, "", nil)
	if err != nil {
		t.Fatalf("EmitLLVMIRText returned error: %v", err)
	}
	if !stage0Called {
		t.Fatal("expected stage0 fallback to be invoked")
	}
	if !strings.Contains(string(got), "stage0 fallback IR for test") {
		t.Fatalf("expected stage0 IR in output:\n%s", got)
	}
	if !findWarning(warnings, "stage0 fallback: emitted MIR through bootstrap-only path") {
		t.Fatalf("expected fallback breadcrumb in warnings: %v", warnings)
	}
}

func TestEmitLLVMFallbackSkipsStage0WhenEnvNotSet(t *testing.T) {
	// No env var set — stage0 must not be reached even when the
	// native subprocess declined with the osty-self signal.
	if v := Stage0FallbackEnabled(); v {
		t.Fatalf("env should be unset before this test")
	}
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    println(1)
}
`)
	withNativeMIRPayloadEmitter(t, func(Entry, string) ([]byte, bool, []error, error) {
		return nil, false, []error{errors.New("osty-self not found")}, nil
	})
	withStage0FallbackOverride(t, func(Entry, llvmabi.Options) ([]byte, error) {
		t.Fatal("stage0 fallback must not run without OSTY_STAGE0_FALLBACK")
		return nil, nil
	})

	_, _, err := EmitLLVMIRText(req.Entry, "", nil)
	if err == nil {
		t.Fatal("expected ErrLLVMNotImplemented when stage0 is gated off")
	}
}

func TestEmitLLVMFallbackSkipsStage0WhenDeclineNotOstySelf(t *testing.T) {
	// Env var set, but the decline reason is unrelated — stage0
	// should NOT run; the dispatcher should pass the decline up.
	t.Setenv(Stage0FallbackEnv, "1")
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    println(1)
}
`)
	withNativeMIRPayloadEmitter(t, func(Entry, string) ([]byte, bool, []error, error) {
		return nil, false, []error{errors.New("MIR payload requires the Osty-owned LIR Proto backend")}, nil
	})
	withStage0FallbackOverride(t, func(Entry, llvmabi.Options) ([]byte, error) {
		t.Fatal("stage0 fallback must not run when decline is not osty-self-missing")
		return nil, nil
	})

	_, _, err := EmitLLVMIRText(req.Entry, "", nil)
	if err == nil {
		t.Fatal("expected ErrLLVMNotImplemented when stage0 is not eligible")
	}
}

func TestEmitLLVMFallbackBubblesUpStage0Decline(t *testing.T) {
	// Env on, native declined with osty-self-missing, stage0 declines too.
	t.Setenv(Stage0FallbackEnv, "1")
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    println(1)
}
`)
	withNativeMIRPayloadEmitter(t, func(Entry, string) ([]byte, bool, []error, error) {
		return nil, false, []error{errors.New("osty-self not found")}, nil
	})
	stage0Reason := errors.New("stage0: MIR shape outside bootstrap subset: synthetic test reason")
	withStage0FallbackOverride(t, func(Entry, llvmabi.Options) ([]byte, error) {
		return nil, stage0Reason
	})

	_, warnings, err := EmitLLVMIRText(req.Entry, "", nil)
	if err == nil {
		t.Fatal("expected ErrLLVMNotImplemented when stage0 also declines")
	}
	if !findWarning(warnings, "stage0 fallback declined") {
		t.Fatalf("expected stage0 decline breadcrumb in warnings: %v", warnings)
	}
}

func TestEmitLLVMFallbackPrefersNativeWhenItCovers(t *testing.T) {
	// Env on but native subprocess succeeds — stage0 must not run.
	t.Setenv(Stage0FallbackEnv, "1")
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    println(1)
}
`)
	withNativeMIRPayloadEmitter(t, func(Entry, string) ([]byte, bool, []error, error) {
		return []byte("; native IR\n"), true, nil, nil
	})
	withStage0FallbackOverride(t, func(Entry, llvmabi.Options) ([]byte, error) {
		t.Fatal("stage0 must not run when native subprocess succeeded")
		return nil, nil
	})

	got, _, err := EmitLLVMIRText(req.Entry, "", nil)
	if err != nil {
		t.Fatalf("EmitLLVMIRText returned error: %v", err)
	}
	if !strings.Contains(string(got), "; native IR") {
		t.Fatalf("expected native IR in output:\n%s", got)
	}
}

// findWarning is a tiny helper that checks whether any warning
// contains the given substring. Used by the wiring tests above.
func findWarning(warnings []error, sub string) bool {
	for _, w := range warnings {
		if w == nil {
			continue
		}
		if strings.Contains(w.Error(), sub) {
			return true
		}
	}
	return false
}

// newBackendRequest is exercised through the existing llvm_test.go
// helpers. Adding a per-test wrapper here keeps the wiring tests
// self-contained.

// Ensure the dispatcher path is exercised end-to-end via Emit (not
// just EmitLLVMIRText) to catch regressions in the binary / object
// branches.
func TestStage0FallbackSurvivesBinaryEmitPath(t *testing.T) {
	t.Setenv(Stage0FallbackEnv, "1")
	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitBinary, `fn main() {}`)

	withNativeMIRPayloadEmitter(t, func(Entry, string) ([]byte, bool, []error, error) {
		return nil, false, []error{errors.New("osty-self not found")}, nil
	})
	withStage0FallbackOverride(t, func(entry Entry, opts llvmabi.Options) ([]byte, error) {
		return []byte("; stage0 IR; package: " + entry.PackageName + "\n"), nil
	})

	res, err := backend.Emit(context.Background(), req)
	if err != nil {
		t.Fatalf("Emit returned error: %v", err)
	}
	if res == nil {
		t.Fatal("Emit returned nil result")
	}
	if len(tc.irCompiles) != 1 {
		t.Fatalf("expected toolchain compile call from stage0 IR; got %d", len(tc.irCompiles))
	}
}
