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
// strict superset of locked+offline, so downstream code only has
// to check Offline/Locked and the frozen semantics are already
// baked in.
//
// Osty: toolchain/pkg_policy.osty:22
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

// PkgSkipDirEntry reports whether a filesystem entry should be
// excluded when hashing a path-source directory or packing a
// publish tarball. Pure basename match; `rel == "."` (the walk
// root itself) is never skipped.
//
// Osty: toolchain/pkg_policy.osty:64
func PkgSkipDirEntry(rel string) bool {
	if rel == "." {
		return false
	}
	base := pkgPathBase(rel)
	switch base {
	case ".osty", ".git", ".hg", ".svn", ".DS_Store":
		return true
	}
	return false
}

// pkgPathBase mirrors filepath.Base on forward-slash and backslash
// paths, including trimming trailing separators (`sub/.git/` →
// `.git`).
func pkgPathBase(p string) string {
	end := len(p)
	for end > 0 && (p[end-1] == '/' || p[end-1] == '\\') {
		end--
	}
	if end == 0 {
		return p
	}
	sep := -1
	for i := 0; i < end; i++ {
		b := p[i]
		if b == '/' || b == '\\' {
			sep = i
		}
	}
	if sep < 0 {
		return p[:end]
	}
	return p[sep+1 : end]
}

// PkgSourceKind enum constants. Order matches
// toolchain/pkg_policy.osty + internal/pkgmgr.SourceKind iota.
const (
	PkgSourceKindPath     int = 0
	PkgSourceKindGit      int = 1
	PkgSourceKindRegistry int = 2
)

// PkgSourceKindString maps a SourceKind int back to its
// human-readable label. Unknown values collapse to "unknown".
//
// Osty: toolchain/pkg_policy.osty:118
func PkgSourceKindString(kind int) string {
	switch kind {
	case PkgSourceKindPath:
		return "path"
	case PkgSourceKindGit:
		return "git"
	case PkgSourceKindRegistry:
		return "registry"
	}
	return "unknown"
}
