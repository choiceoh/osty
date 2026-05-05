package backend

import (
	"errors"
	"strings"
	"testing"

	"github.com/osty/osty/internal/llvmabi"
)

// lirProtoGateSrc is a small main that compiles cleanly through
// either the native-owned fast path or the MIR-direct emitter — the
// Phase-7 gate fires unconditionally at the dispatcher entry, so the
// fixture only needs to exercise generateLLVMIR end-to-end without
// preflight failures. We pick println-of-string so the source itself
// is the smallest thing the dispatcher will accept.
const lirProtoGateSrc = `fn main() {
    println("hi")
}
`

// TestLLVMDispatchAppendsLIRProtoFallbackWarning pins the Phase-7 gate
// hook in generateLLVMIR: when OSTY_LLVM_LIR_PROTO is set, the
// dispatcher records ErrLIRProtoNotWired in the warnings slice and
// then continues through the regular MIR-direct path so the build
// stays green even if the gate is flipped on before the runner is
// wired.
func TestLLVMDispatchAppendsLIRProtoFallbackWarning(t *testing.T) {
	req := newBackendRequest(t, EmitLLVMIR, lirProtoGateSrc)
	t.Setenv(llvmabi.LIRProtoEnvVar, "1")
	_, warnings, _ := generateLLVMIR(req.Entry, "arm64-apple-macosx", req.Features, req.Emit)
	found := false
	for _, w := range warnings {
		if errors.Is(w, llvmabi.ErrLIRProtoNotWired) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("warnings missing ErrLIRProtoNotWired (have: %s)", joinLirProtoWarnings(warnings))
	}
}

// TestLLVMDispatchSkipsLIRProtoWarningWhenGateOff is the negative
// pin: with the gate off, no LIR Proto warning ever surfaces.
// Without this, a regression that always-on'd the warning would
// silently make every backend run noisy.
func TestLLVMDispatchSkipsLIRProtoWarningWhenGateOff(t *testing.T) {
	req := newBackendRequest(t, EmitLLVMIR, lirProtoGateSrc)
	t.Setenv(llvmabi.LIRProtoEnvVar, "")
	_, warnings, _ := generateLLVMIR(req.Entry, req.Layout.Target, req.Features, req.Emit)
	for _, w := range warnings {
		if errors.Is(w, llvmabi.ErrLIRProtoNotWired) {
			t.Fatalf("warnings unexpectedly include ErrLIRProtoNotWired with gate off: %s", joinLirProtoWarnings(warnings))
		}
	}
}

func joinLirProtoWarnings(warnings []error) string {
	parts := make([]string, 0, len(warnings))
	for _, w := range warnings {
		parts = append(parts, w.Error())
	}
	return strings.Join(parts, " | ")
}
