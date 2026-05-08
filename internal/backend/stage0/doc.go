// Package stage0 is the bootstrap-only MIR → LLVM IR emitter.
//
// stage0 catches one specific failure mode: the production LLVM emit
// path forks `osty-self lir-proto-lower` (built by `osty build
// toolchain/`), and on a fresh clone that artifact does not exist yet.
// Without stage0 there is no way to bootstrap the toolchain compiler
// itself — `osty-self` cannot build the program that builds `osty-self`.
//
// The emitter is intentionally narrow. Each new MIR pattern requires an
// explicit unlock + regression test; surface drift is held back by
// `TestStage0CoversToolchainMIR`-style coverage gates added at later
// phases. Production builds — where `osty-self` is reachable — go
// through the LIR Proto subprocess unchanged; stage0 is only consulted
// when `IsOstySelfMissing` reports the canonical decline.
//
// See docs/osty_self_bootstrap_design.md for the bootstrap plan,
// retirement conditions, and the roadmap from P1 (this skeleton)
// through P5 (CI matrix). v0.6 spec surface is unchanged — stage0
// operates entirely below the front-end.
package stage0
