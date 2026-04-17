// Package runner ships both the Osty source (toolchain/runner.osty)
// and a hand-maintained Go snapshot (policy.go). The long-term goal
// is for `osty gen` to emit policy.go directly; the rest of this
// file documents why that isn't wired up yet and what the next
// maintainer needs to fix.
//
// Experiment (2026-04-18):
//
//	# Isolated (toolchain/ siblings have unrelated pre-existing
//	# front-end errors; run from a scratch dir):
//	mkdir -p /tmp/runner-gen-src
//	cp toolchain/runner.osty /tmp/runner-gen-src/
//	osty gen --backend go --emit go --package runner \
//	    -o internal/runner/generated.go \
//	    /tmp/runner-gen-src/runner.osty
//
// What works:
//
//   - The gen command accepts a single-file package that `use
//     std.strings`es and produces a syntactically valid Go file with
//     the correct package declaration and line markers.
//
// What still breaks:
//
//  1. std.strings doesn't lower. Generated calls read
//     `strings.concat`, `strings.hasSuffix`, `strings.indexOf`, etc.
//     — the Osty surface, not Go's strings.HasSuffix / strings.Index
//     / `+` concatenation. The gen backend needs a prelude that maps
//     each stdlib function to its Go equivalent (or ships a runtime
//     helper).
//
//  2. `pub` doesn't propagate to Go export. Field `pub severity`
//     emits as lowercase `severity` and `pub fn binaryBaseName`
//     emits as lowercase `binaryBaseName`. Callers outside the
//     package can't see them. Fix: capitalise identifiers declared
//     `pub` when the emit target is `go`.
//
//  3. Option types round-trip through `**T`. `RunnerDiag?` in the
//     Osty CrossCompileOutcome lowers to `**RunnerDiag`. For a
//     hand-written Go API `*RunnerDiag` is the idiomatic shape.
//
//  4. `if` expressions wrap in `func() any { ... }()`. Harmless for
//     bootstrap bundles that already depend on `any`-typed helpers,
//     but the standalone-package emit path should produce concrete
//     Go types so nothing boxes through `any`.
//
// Once (1)–(4) are addressed, replace this file with:
//
//	//go:generate go run gen_runner.go
//
// and add a `gen_runner.go` mirroring internal/selfhost/gen_selfhost.go.
// For now, policy.go is the source of truth at `go build` time and
// toolchain/runner.osty is the source of truth at review time; the
// drift_test.go in this package enforces that the two stay in sync
// (every `pub fn` / `pub struct` in the Osty file must have a
// matching exported Go symbol here).
package runner
