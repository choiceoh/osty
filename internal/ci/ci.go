// Package ci implements the project-quality check bundle that backs
// the `osty ci` subcommand.
//
// CI is not a single analysis: it is a fan-out over the existing
// pipelines in internal/format, internal/lint, internal/check,
// internal/manifest, internal/lockfile, and this package's own
// policy + API-snapshot code. The job of ci.Runner is to load the
// project once, run each enabled check against the shared state,
// and aggregate per-check results into a single Report.
//
// The checks are intentionally conservative: every diagnostic they
// produce is either Error (a hard fail that breaks CI) or Warning
// (surfaced, but only breaks under --strict). None of the checks
// write to the project tree; `osty ci snapshot` is a separate entry
// point that writes an api-snapshot.json under the project root.
package ci

import (
	"time"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/resolve"
)

// Options controls which checks Runner.Run executes.
//
// The zero value of Options enables the "quick bundle" (format,
// lint, policy, lockfile) — the set most useful on every PR. The
// release / semver checks are opt-in because they make assumptions
// about publishing state and baselines.
type Options struct {
	// Format enables the `fmt --check` bundle.
	Format bool
	// Lint enables the lint pass over the whole workspace/package.
	Lint bool
	// Policy enables package-policy checks (manifest fields,
	// workspace well-formed, file-size limits).
	Policy bool
	// Lockfile enables the osty.lock presence + up-to-date check.
	Lockfile bool
	// Release enables release-validation (no path deps, license
	// present, strict-semver version, lockfile present).
	Release bool
	// Semver enables the semver-break detector. Requires Baseline
	// to be non-empty.
	Semver bool
	// SemverWarnOnly downgrades every breaking change reported by
	// the semver check to Warning severity, regardless of the
	// version bump. Use this for the "API compatibility report"
	// mode that surfaces drift without failing CI.
	SemverWarnOnly bool
	// Strict promotes any Warning-severity diagnostic produced by
	// the run into a failed check. Errors always fail.
	Strict bool
	// Baseline is a filesystem path to an api-snapshot.json
	// captured from a previous version of this package. Required
	// when Semver is true; ignored otherwise.
	Baseline string
	// MaxFileBytes caps individual source file size during the
	// policy check. Zero disables the limit. A small sentinel
	// default is applied by Runner when Policy is on and this
	// value is zero.
	MaxFileBytes int64
}

// DefaultOptions returns the "quick bundle" every `osty ci`
// invocation runs when no flags are set.
func DefaultOptions() Options {
	return Options{
		Format:   true,
		Lint:     true,
		Policy:   true,
		Lockfile: true,
	}
}

// CheckName identifies one check inside a Report. Stable string
// constants so tooling can parse the Report JSON.
type CheckName string

const (
	CheckFormat   CheckName = "format"
	CheckLint     CheckName = "lint"
	CheckPolicy   CheckName = "policy"
	CheckLockfile CheckName = "lockfile"
	CheckRelease  CheckName = "release"
	CheckSemver   CheckName = "semver"
)

// Check is the outcome of one individual CI check.
//
// Skipped is set when the check was not enabled in Options (so the
// Report still shows it — with a note — for transparency). Passed
// is true iff the check ran and emitted no error-severity
// diagnostic (and no warning-severity diagnostic when Strict is
// set).
type Check struct {
	Name    CheckName          `json:"name"`
	Passed  bool               `json:"passed"`
	Skipped bool               `json:"skipped"`
	Note    string             `json:"note,omitempty"`
	Diags   []*diag.Diagnostic `json:"diagnostics,omitempty"`
}

// Report is the aggregated outcome of one `osty ci` run. It's the
// authoritative object the CLI consumes to decide the exit code.
type Report struct {
	ProjectRoot string    `json:"projectRoot"`
	StartedAt   time.Time `json:"startedAt"`
	FinishedAt  time.Time `json:"finishedAt"`
	Checks      []*Check  `json:"checks"`
}

// AllPassed reports whether every non-skipped check passed.
func (r *Report) AllPassed() bool {
	for _, c := range r.Checks {
		if c.Skipped {
			continue
		}
		if !c.Passed {
			return false
		}
	}
	return true
}

// Runner carries the pre-loaded shared state every check consumes
// so the manifest is parsed and the workspace is resolved exactly
// once per run.
//
// Fields may be nil when a check's prerequisites aren't satisfied
// (e.g. Manifest is nil when there's no osty.toml in the target
// tree) — each check is responsible for no-op'ing in that case and
// recording a Skipped entry.
type Runner struct {
	Root      string
	Opts      Options
	Manifest  *manifest.Manifest
	Workspace *resolve.Workspace
	// Packages is the list of resolve.Packages loaded for the
	// project: single-package tree → one entry; workspace → one
	// entry per member. Populated by Load.
	Packages []*resolve.Package
	// Results is one PackageResult per Packages entry, in the
	// same order. Holds the resolver's diagnostics + the per-file
	// FileScope/Refs/TypeRefs maps so downstream checks (lint)
	// can reuse the resolved state instead of re-running resolve.
	// nil when Load failed before resolution; checks must guard.
	Results []*resolve.PackageResult
}

// NewRunner returns a Runner whose state is uninitialized. Call
// Load before Run to populate Manifest / Workspace / Packages.
func NewRunner(root string, opts Options) *Runner {
	if opts.Policy && opts.MaxFileBytes == 0 {
		// 1 MiB is a generous default — large enough for any
		// hand-written source, small enough to flag an accidentally
		// committed blob.
		opts.MaxFileBytes = 1 << 20
	}
	return &Runner{Root: root, Opts: opts}
}
