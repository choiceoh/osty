package backend

import (
	"os"
	"strings"

	"github.com/osty/osty/internal/backend/stage0"
	"github.com/osty/osty/internal/llvmabi"
)

// Stage0FallbackEnv is the env-var name that opts a build into the
// stage0 bootstrap fallback emitter. The dispatcher consults it inside
// `emitLLVMFallback` only when the native LIR Proto subprocess
// declined because `osty-self` is missing — the canonical first-build
// symptom on a fresh clone. Production runs (osty-self built) never
// observe stage0 even when the variable is set.
const Stage0FallbackEnv = "OSTY_STAGE0_FALLBACK"

// Stage0FallbackEnabled reports whether the stage0 fallback emitter
// should be considered when the LIR Proto subprocess declines due to
// `osty-self` not being built yet.
func Stage0FallbackEnabled() bool {
	switch strings.TrimSpace(os.Getenv(Stage0FallbackEnv)) {
	case "1", "true", "TRUE", "True", "on", "ON", "On", "yes", "YES", "Yes":
		return true
	}
	return false
}

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
