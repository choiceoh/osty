package llvmgen

// LIRProtoRunner is the contract any Go-side bridge into the
// Osty-owned LIR Proto lowerer must satisfy. The dispatcher in
// internal/backend/llvm.go routes through this interface when
// LIRProtoSelected() is true and a non-nil runner has been set via
// SetLIRProtoRunner.
//
// Today the only registered runner is `defaultLIRProtoRunner`, which
// always returns ErrLIRProtoNotWired so production output is
// unchanged. The runner stays decoupled from the dispatch site so a
// future commit can register a real implementation (for example: a
// JSON-RPC client that talks to a self-host osty-native-lirproto
// binary built from toolchain/lir_proto.osty) without touching the
// dispatcher at all.
//
// The contract is intentionally narrow: input is a request the
// existing dispatcher already builds (package name, source path,
// source bytes, target triple); output is either a complete LLVM
// IR text artifact OR a structured error that the dispatcher
// converts into a fall-back warning. No partial / streaming /
// retry shape — the dispatcher's fall-back policy treats any error
// as "use the legacy MIR-direct emitter and continue."
type LIRProtoRunner interface {
	// Run lowers the given source through the LIR Proto path and
	// returns the rendered LLVM IR. Implementations MUST return
	// either (nil, ErrLIRProtoNotWired) when no real runner is
	// available, or a non-nil byte slice with no error on success.
	// Any other (err != nil) result is treated as a structured
	// fall-back signal — the dispatcher logs it and continues with
	// the legacy MIR-direct emitter.
	Run(req LIRProtoRequest) ([]byte, error)
}

// LIRProtoRequest is the single argument the dispatcher hands the
// runner. Mirrors the Phase-7 plan doc's "shadow parity" shape:
// everything the production MIR-direct emitter sees, packaged for
// transport to a self-host process.
type LIRProtoRequest struct {
	PackageName string
	SourcePath  string
	Source      []byte
	Target      string
}

var registeredLIRProtoRunner LIRProtoRunner = defaultLIRProtoRunner{}

// SetLIRProtoRunner installs the runner the Phase-7 dispatcher will
// invoke when the gate is enabled. Pass nil to revert to the default
// "not-wired" behavior. Intended for the eventual self-host bridge
// (registered from cmd-side init) and tests that want to swap in a
// fake runner.
func SetLIRProtoRunner(runner LIRProtoRunner) {
	if runner == nil {
		registeredLIRProtoRunner = defaultLIRProtoRunner{}
		return
	}
	registeredLIRProtoRunner = runner
}

// CurrentLIRProtoRunner returns the currently-registered runner,
// always non-nil. Test helpers use this to peek at the active
// implementation; production code goes through InvokeLIRProtoRunner.
func CurrentLIRProtoRunner() LIRProtoRunner {
	return registeredLIRProtoRunner
}

// InvokeLIRProtoRunner is the single entry point dispatcher code
// uses to attempt a LIR Proto lowering. It is a thin wrapper around
// the registered runner that exists so the dispatcher does not have
// to know whether a runner is registered or not — the default
// "not-wired" runner returns ErrLIRProtoNotWired which the
// dispatcher already treats as a fall-back signal.
func InvokeLIRProtoRunner(req LIRProtoRequest) ([]byte, error) {
	return registeredLIRProtoRunner.Run(req)
}

// defaultLIRProtoRunner is the always-installed fallback. Returns
// (nil, ErrLIRProtoNotWired) for every call. Replaced via
// SetLIRProtoRunner once the self-host bridge lands.
type defaultLIRProtoRunner struct{}

func (defaultLIRProtoRunner) Run(_ LIRProtoRequest) ([]byte, error) {
	return nil, ErrLIRProtoNotWired
}
