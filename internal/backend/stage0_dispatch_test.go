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

func withStage0MissingOstySelfProbe(t *testing.T, fn func() ([]error, bool)) {
	t.Helper()
	old := probeStage0BootstrapMissingOstySelf
	probeStage0BootstrapMissingOstySelf = fn
	t.Cleanup(func() { probeStage0BootstrapMissingOstySelf = old })
}

func emitLLVMIRTextWithStage0Bootstrap(entry Entry, target string, features []string) ([]byte, []error, error) {
	return generateLLVMIR(entry, target, features, EmitLLVMIR, true)
}

func TestGenerateLLVMIRShortCircuitsNativeMIRMarshalWhenStage0FreshClone(t *testing.T) {
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    println(1)
}
`)

	withStage0MissingOstySelfProbe(t, func() ([]error, bool) {
		return []error{errors.New(stage0OstySelfMissingWarning)}, true
	})
	withNativeMIRPayloadEmitter(t, func(Entry, string) ([]byte, bool, []error, error) {
		t.Fatal("native MIR payload emitter must not run when stage0 already knows osty-self is missing")
		return nil, false, nil, nil
	})
	withStage0FallbackOverride(t, func(entry Entry, opts llvmabi.Options) ([]byte, error) {
		if entry.MIR == nil {
			t.Fatal("stage0 fallback received nil MIR")
		}
		return []byte("; stage0 shortcut IR for test\n"), nil
	})

	got, warnings, err := emitLLVMIRTextWithStage0Bootstrap(req.Entry, "", nil)
	if err != nil {
		t.Fatalf("EmitLLVMIRText returned error: %v", err)
	}
	if !strings.Contains(string(got), "stage0 shortcut IR for test") {
		t.Fatalf("expected stage0 shortcut IR in output:\n%s", got)
	}
	if !findWarning(warnings, stage0OstySelfMissingWarning) {
		t.Fatalf("expected osty-self missing warning in warnings: %v", warnings)
	}
}

func TestEmitLLVMFallbackUsesStage0WhenOstySelfMissing(t *testing.T) {
	withStage0MissingOstySelfProbe(t, func() ([]error, bool) { return nil, false })
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

	got, warnings, err := emitLLVMIRTextWithStage0Bootstrap(req.Entry, "", nil)
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

func TestEmitLLVMFallbackUsesStage0WhenPartialOstySelfDeclines(t *testing.T) {
	withStage0MissingOstySelfProbe(t, func() ([]error, bool) { return nil, false })
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    println(1)
}
`)

	withNativeMIRPayloadEmitter(t, func(Entry, string) ([]byte, bool, []error, error) {
		return nil, false, []error{errors.New("osty-self: stage0 declined function: mirJsonParseValue")}, nil
	})
	stage0Called := false
	withStage0FallbackOverride(t, func(entry Entry, opts llvmabi.Options) ([]byte, error) {
		stage0Called = true
		if entry.MIR == nil {
			t.Fatal("stage0 fallback received nil MIR")
		}
		return []byte("; stage0 fallback after partial osty-self\n"), nil
	})

	got, warnings, err := emitLLVMIRTextWithStage0Bootstrap(req.Entry, "", nil)
	if err != nil {
		t.Fatalf("EmitLLVMIRText returned error: %v", err)
	}
	if !stage0Called {
		t.Fatal("expected stage0 fallback to be invoked")
	}
	if !strings.Contains(string(got), "partial osty-self") {
		t.Fatalf("expected stage0 IR in output:\n%s", got)
	}
	if !findWarning(warnings, "stage0 fallback: emitted MIR through bootstrap-only path") {
		t.Fatalf("expected fallback breadcrumb in warnings: %v", warnings)
	}
}

func TestEmitLLVMFallbackSkipsStage0ByDefault(t *testing.T) {
	// Strict default mode must not reach stage0 even when the
	// native subprocess declined with the osty-self signal.
	req := newBackendRequest(t, EmitLLVMIR, `fn main() {
    println(1)
}
`)
	withNativeMIRPayloadEmitter(t, func(Entry, string) ([]byte, bool, []error, error) {
		return nil, false, []error{errors.New("osty-self not found")}, nil
	})
	withStage0FallbackOverride(t, func(Entry, llvmabi.Options) ([]byte, error) {
		t.Fatal("stage0 fallback must not run without bootstrap mode")
		return nil, nil
	})

	_, _, err := EmitLLVMIRText(req.Entry, "", nil)
	if err == nil {
		t.Fatal("expected ErrLLVMNotImplemented when stage0 is gated off")
	}
}

func TestEmitLLVMFallbackSkipsStage0WhenDeclineNotOstySelf(t *testing.T) {
	// Bootstrap mode is set, but the decline reason is unrelated — stage0
	// should NOT run; the dispatcher should pass the decline up.
	withStage0MissingOstySelfProbe(t, func() ([]error, bool) { return nil, false })
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

	_, _, err := emitLLVMIRTextWithStage0Bootstrap(req.Entry, "", nil)
	if err == nil {
		t.Fatal("expected ErrLLVMNotImplemented when stage0 is not eligible")
	}
}

func TestEmitLLVMFallbackBubblesUpStage0Decline(t *testing.T) {
	// Bootstrap mode on, native declined with osty-self-missing, stage0 declines too.
	withStage0MissingOstySelfProbe(t, func() ([]error, bool) { return nil, false })
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

	_, warnings, err := emitLLVMIRTextWithStage0Bootstrap(req.Entry, "", nil)
	if err == nil {
		t.Fatal("expected ErrLLVMNotImplemented when stage0 also declines")
	}
	if !findWarning(warnings, "stage0 fallback declined") {
		t.Fatalf("expected stage0 decline breadcrumb in warnings: %v", warnings)
	}
}

func TestEmitLLVMFallbackPrefersNativeWhenItCovers(t *testing.T) {
	// Bootstrap mode on but native subprocess succeeds — stage0 must not run.
	withStage0MissingOstySelfProbe(t, func() ([]error, bool) { return nil, false })
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

	got, _, err := emitLLVMIRTextWithStage0Bootstrap(req.Entry, "", nil)
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
	withStage0MissingOstySelfProbe(t, func() ([]error, bool) { return nil, false })
	tc := &fakeLLVMToolchain{}
	backend := LLVMBackend{toolchain: tc}
	req := newBackendRequest(t, EmitBinary, `fn main() {}`)
	req.BootstrapStage0 = true

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
