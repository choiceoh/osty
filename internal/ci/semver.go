package ci

import (
	"fmt"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/pkgmgr/semver"
)

// checkSemver is the semver-break detector: compare the current
// package's exported API against a committed baseline snapshot
// and emit diagnostics whose severity tracks the semver bump the
// user chose.
//
// Rules:
//
//   - Any removed exported symbol is a breaking change.
//     * If the current major >  baseline major: Warning (expected,
//       the user already signaled a breaking release).
//     * Otherwise: Error (silent break).
//   - Added symbols are Info-equivalent — surfaced under --json
//     but treated as passing.
//   - Signature changes on otherwise-same symbols are Error unless
//     the major bumped.
//
// Requires Options.Baseline to point at a readable snapshot. A
// missing baseline is a hard Error — CI should never silently pass
// the semver check just because the file moved.
func (r *Runner) checkSemver() *Check {
	c := &Check{Name: CheckSemver}
	if r.Opts.Baseline == "" {
		c.Diags = append(c.Diags, synthetic(diag.Error, "CI401",
			"semver check enabled but --baseline was not set"))
		return c
	}
	baseline, err := ReadSnapshot(r.Opts.Baseline)
	if err != nil {
		c.Diags = append(c.Diags, synthetic(diag.Error, "CI402",
			fmt.Sprintf("cannot read baseline snapshot: %v", err)))
		return c
	}
	if len(r.Packages) == 0 {
		c.Diags = append(c.Diags, synthetic(diag.Error, "CI403",
			"no package loaded; cannot capture current API"))
		return c
	}
	var curVersion, curEdition string
	if r.Manifest != nil && r.Manifest.HasPackage {
		curVersion = r.Manifest.Package.Version
		curEdition = r.Manifest.Package.Edition
	}
	current := NewWorkspaceSnapshot(r.Packages, curVersion, curEdition)
	// Compatibility mode promotes breaking changes to Warning
	// when the major version was already bumped — the user has
	// already signaled intent to break.
	breakingIsError := !majorBumped(baseline.Version, current.Version)
	if r.Opts.SemverWarnOnly {
		breakingIsError = false
	}

	diff := Compare(baseline, current)
	for _, s := range diff.Removed {
		sev := diag.Error
		if !breakingIsError {
			sev = diag.Warning
		}
		c.Diags = append(c.Diags, synthetic(sev, "CI410",
			fmt.Sprintf("breaking: exported %s %q was removed", s.Symbol.Kind, s.Qualified())))
	}
	for _, s := range diff.Changed {
		sev := diag.Error
		if !breakingIsError {
			sev = diag.Warning
		}
		c.Diags = append(c.Diags, synthetic(sev, "CI411",
			fmt.Sprintf("breaking: exported %s %q signature changed (now: %s)",
				s.Symbol.Kind, s.Qualified(), s.Symbol.Sig)))
	}
	// Additive changes never fail CI but we still report them so
	// `osty ci --json` output is useful for automated release-note
	// generation.
	for _, s := range diff.Added {
		c.Diags = append(c.Diags, synthetic(diag.Warning, "CI420",
			fmt.Sprintf("additive: exported %s %q is new", s.Symbol.Kind, s.Qualified())))
	}
	// Warn if we flagged "additive" symbols when Strict is on;
	// downgrade silently otherwise. The Runner's post-pass
	// inspects Strict against warnings, so no extra work here.
	return c
}

// majorBumped returns true when `cur` has a strictly greater
// major version than `base`. A malformed or empty version on
// either side is treated as "not bumped" (conservative default
// that flags breakages as errors).
func majorBumped(base, cur string) bool {
	if base == "" || cur == "" {
		return false
	}
	b, err := semver.ParseVersion(base)
	if err != nil {
		return false
	}
	c, err := semver.ParseVersion(cur)
	if err != nil {
		return false
	}
	return c.Major > b.Major
}
