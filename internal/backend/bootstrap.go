package backend

import "strings"

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
