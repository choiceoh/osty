package backend

import (
	"strings"

	"github.com/osty/osty/internal/backend/stage0"
	"github.com/osty/osty/internal/llvmabi"
)

// tryStage0Fallback invokes the stage0 emitter with the entry's MIR.
// Indirection so tests can stub it without touching the live emitter.
var tryStage0Fallback = func(entry Entry, opts llvmabi.Options) ([]byte, error) {
	return stage0.EmitMIR(entry.MIR, opts)
}

// ostySelfMissingSignal matches the canonical "osty-self not found" message
// produced by `cmd/osty-native-lirproto/main.go:resolveOstySelfBin`. The
// substring is intentionally short so it survives the warning chain
// (`osty-native-lirproto` → `osty-native-llvmgen` → `nativellvmgen.TryMIR`
// → `tryNativeOwnedMIRPayloadLLVMIRText`) where each hop can prepend its
// own context.
const ostySelfMissingSignal = "osty-self not found"
const ostySelfStage0DeclinedSignal = "stage0 declined function:"

// IsOstySelfMissing reports whether any of the supplied subprocess warnings
// carry the "osty-self not found" signal originating from
// `cmd/osty-native-lirproto`.
//
// The native LIR Proto subprocess forks `osty-self lir-proto-lower`; if
// that binary has not been built yet (`osty build toolchain/` not run),
// the subprocess declines and propagates the message up through the
// warning chain. This helper fixes the call-site shape for future stage0
// fallback dispatch — callers can ask "would emission have succeeded with
// a built osty-self?" without parsing strings themselves.
//
// Today the signal is matched by substring against `error.Error()`. A
// future wire-format revision will replace this with a structured
// `Reason` field on the subprocess response so the detection mechanism
// can drop the substring scan; consumers of `IsOstySelfMissing` will not
// need to update.
//
// See `docs/osty_self_bootstrap_design.md` for the bootstrap design and
// the planned stage0 fallback that will consume this helper.
func IsOstySelfMissing(warnings []error) bool {
	for _, w := range warnings {
		if w == nil {
			continue
		}
		if strings.Contains(w.Error(), ostySelfMissingSignal) {
			return true
		}
	}
	return false
}

// ShouldUseStage0BootstrapFallback reports whether an explicitly enabled
// bootstrap build should bypass the native subprocess result and emit through
// stage0. A stale partial `osty-self` can exist in the normal lookup path and
// decline before a fresh stage1 has been produced; treating that as bootstrap
// state lets `osty build toolchain/` recover instead of getting stuck behind
// its previous partial binary.
func ShouldUseStage0BootstrapFallback(warnings []error) bool {
	for _, w := range warnings {
		if w == nil {
			continue
		}
		msg := w.Error()
		if strings.Contains(msg, ostySelfMissingSignal) || strings.Contains(msg, ostySelfStage0DeclinedSignal) {
			return true
		}
	}
	return false
}
