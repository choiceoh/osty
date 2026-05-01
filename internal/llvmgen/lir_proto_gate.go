package llvmgen

import (
	"errors"
	"os"
)

// LIRProtoEnvVar is the explicit Phase-7 gate documented in
// docs/lir_proto_plan.md. When the environment variable is set to a
// non-empty, non-zero value, the LLVM backend dispatcher routes
// through the LIR Proto path instead of the existing MIR-direct
// emitter.
//
// The LIR Proto runner itself lives in `toolchain/lir_proto.osty` and
// is not yet callable from the Go side, so the production hookpoint
// today returns ErrLIRProtoNotWired when the gate is set. A future
// commit will replace the not-wired sentinel with an actual
// MIR -> LIR Proto -> LLVM text invocation; the gate is added now so
// the dispatch site, the env-var contract, and the fallback policy
// stay one PR rather than three.
const LIRProtoEnvVar = "OSTY_LLVM_LIR_PROTO"

// ErrLIRProtoNotWired is returned by Phase-7 dispatch sites when the
// LIR Proto gate is enabled but no Go-side runner exists yet. Treat
// this as a structured "fall back to the legacy MIR-direct emitter"
// signal rather than a user-visible error — the dispatcher converts
// it into a backend warning and continues with the existing path so
// the gate never breaks production by being flipped on early.
var ErrLIRProtoNotWired = errors.New("llvmgen: LIR Proto path selected by " + LIRProtoEnvVar + " but Go-side runner is not wired yet — falling back to MIR-direct")

// LIRProtoSelected reports whether the Phase-7 gate is on for the
// current process. The accepted positive values match the rest of the
// project's env-var policy: anything other than the empty string and
// the literal "0" / "false" / "off" turns the gate on.
func LIRProtoSelected() bool {
	return lirProtoEnvOn(os.Getenv(LIRProtoEnvVar))
}

func lirProtoEnvOn(value string) bool {
	switch value {
	case "", "0", "false", "FALSE", "False", "off", "OFF", "Off", "no", "NO", "No":
		return false
	}
	return true
}
