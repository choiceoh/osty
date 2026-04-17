// pkg_policy.go is the Go snapshot of toolchain/pkg_policy.osty.
// Osty is the source of truth; the drift test in this package
// enforces parity.
package runner

import "strings"

// ResolveOpts mirrors toolchain/pkg_policy.osty's ResolveOpts. The
// three flags are carried through every subcommand that touches
// the resolver (build/run/test/fetch/publish/add/update).
//
// Osty: toolchain/pkg_policy.osty:15
type ResolveOpts struct {
	Offline bool
	Locked  bool
	Frozen  bool
}

// ExpandFrozenFlags applies `--frozen`'s implication that it is a
// strict superset of locked+offline. See the Osty source for the
// semantic rationale.
//
// Osty: toolchain/pkg_policy.osty:28
func ExpandFrozenFlags(opts ResolveOpts) ResolveOpts {
	if opts.Frozen {
		return ResolveOpts{Offline: true, Locked: true, Frozen: true}
	}
	return opts
}

// FrozenMissingLockfileMessage builds the error string for the
// "--frozen but no lockfile present" rejection. Caller passes in
// the lockfile basename so lockfile.LockFile stays a single
// source of truth on the Go side.
//
// Osty: toolchain/pkg_policy.osty:42
func FrozenMissingLockfileMessage(lockfileName string) string {
	return "--frozen requires an existing " + lockfileName + "; run `osty update` first"
}

// LockedDiffMessage renders the "--locked: NAME would change:"
// report. Empty string means no changes (host proceeds as
// normal). `changes` is each change pre-stringified via
// pkgmgr.Change.String().
//
// Osty: toolchain/pkg_policy.osty:51
func LockedDiffMessage(lockfileName string, changes []string) string {
	if len(changes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("--locked: ")
	b.WriteString(lockfileName)
	b.WriteString(" would change:\n")
	for _, c := range changes {
		b.WriteString("  ")
		b.WriteString(c)
		b.WriteString("\n")
	}
	b.WriteString("rerun without --locked to update the lockfile.")
	return b.String()
}
