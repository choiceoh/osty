package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/osty/osty/internal/ci"
	"github.com/osty/osty/internal/diag"
)

// defaultSnapshotPath is where `osty ci snapshot` writes by default
// and where `osty ci --semver` reads its baseline from when
// --baseline is not set. Picking a committed path inside the
// project keeps the semver baseline reviewable in PRs.
const defaultSnapshotPath = "api-snapshot.json"

// runCi implements `osty ci [flags] [PATH]` and
// `osty ci snapshot [-o OUT] [PATH]`.
//
// The default invocation runs the quick bundle (format + lint +
// policy + lockfile) and exits 0 on a clean tree, 1 on any
// error-severity diagnostic, 2 on usage errors. Individual check
// groups can be toggled with flags, or the full bundle requested
// via --all.
//
// Exit codes:
//
//	0  every non-skipped check passed
//	1  one or more checks failed (errors, or warnings under --strict)
//	2  usage error (unknown flag, bad baseline path, etc.)
func runCi(args []string, cliF cliFlags) {
	if len(args) >= 1 && args[0] == "snapshot" {
		runCiSnapshot(args[1:])
		return
	}

	fs := flag.NewFlagSet("ci", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty ci [flags] [PATH]")
		fmt.Fprintln(os.Stderr, "flags:")
		fmt.Fprintln(os.Stderr, "  --all             enable every check (equivalent to -format -lint -policy -lockfile -release -semver)")
		fmt.Fprintln(os.Stderr, "  --format          enable format check       [default: on]")
		fmt.Fprintln(os.Stderr, "  --lint            enable lint pass          [default: on]")
		fmt.Fprintln(os.Stderr, "  --policy          enable package policy     [default: on]")
		fmt.Fprintln(os.Stderr, "  --lockfile        enable lockfile check     [default: on]")
		fmt.Fprintln(os.Stderr, "  --release         enable release validation [default: off]")
		fmt.Fprintln(os.Stderr, "  --semver          enable semver-break check [default: off]")
		fmt.Fprintln(os.Stderr, "  --strict          fail on warnings as well as errors")
		fmt.Fprintln(os.Stderr, "  --baseline PATH   baseline api snapshot for --semver (default: api-snapshot.json)")
		fmt.Fprintln(os.Stderr, "  --max-file-bytes N  per-file size cap enforced by --policy (default 1MiB)")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  osty ci snapshot [-o OUT] [PATH]   # capture current api-snapshot.json")
	}

	var (
		all            bool
		fmtOn          = true
		lintOn         = true
		policyOn       = true
		lockOn         = true
		releaseOn      bool
		semverOn       bool
		semverWarnOnly bool
		strict         bool
		baseline       string
		maxFileBytes   int64
	)
	fs.BoolVar(&all, "all", false, "enable every check")
	fs.BoolVar(&fmtOn, "format", fmtOn, "enable format check")
	fs.BoolVar(&lintOn, "lint", lintOn, "enable lint pass")
	fs.BoolVar(&policyOn, "policy", policyOn, "enable package policy")
	fs.BoolVar(&lockOn, "lockfile", lockOn, "enable lockfile check")
	fs.BoolVar(&releaseOn, "release", false, "enable release validation")
	fs.BoolVar(&semverOn, "semver", false, "enable semver-break check")
	fs.BoolVar(&semverWarnOnly, "api-compat", false, "warn-only API compatibility check (implies --semver)")
	fs.BoolVar(&strict, "strict", false, "fail on warnings as well as errors")
	fs.StringVar(&baseline, "baseline", "", "baseline api snapshot path for --semver")
	fs.Int64Var(&maxFileBytes, "max-file-bytes", 0, "per-file size cap enforced by --policy")
	_ = fs.Parse(args)

	start := "."
	if fs.NArg() == 1 {
		start = fs.Arg(0)
	} else if fs.NArg() > 1 {
		fs.Usage()
		os.Exit(2)
	}

	if all {
		fmtOn, lintOn, policyOn, lockOn, releaseOn, semverOn = true, true, true, true, true, true
	}
	if semverWarnOnly {
		semverOn = true
	}

	opts := ci.Options{
		Format:         fmtOn,
		Lint:           lintOn,
		Policy:         policyOn,
		Lockfile:       lockOn,
		Release:        releaseOn,
		Semver:         semverOn,
		SemverWarnOnly: semverWarnOnly,
		Strict:         strict,
		Baseline:       baseline,
		MaxFileBytes:   int(maxFileBytes),
	}

	// Manifest parse-error rendering: surface E2xxx diagnostics
	// with caret underlines before CI starts. We pass the parsed
	// manifest into Runner so it doesn't re-read the file from
	// disk — keeping the "manifest loaded once per command"
	// invariant the rest of cmd/osty maintains.
	runner := ci.NewRunner(start, &opts)
	if _, _, err := manifestLookupNear(start); err == nil {
		m, root, abort := loadManifestWithDiag(start, cliF)
		if abort {
			os.Exit(2)
		}
		runner.Manifest = m
		runner.Root = root
	}
	if msg := runner.Load(); msg != "" {
		fmt.Fprintf(os.Stderr, "osty ci: %s\n", msg)
		os.Exit(2)
	}
	// Default semver baseline is resolved relative to the
	// project root (not cwd) so `osty ci --semver` works from
	// any subdirectory of the project — matching how `osty
	// build` and `osty test` find osty.toml + osty.lock.
	if runner.Opts.Semver && runner.Opts.Baseline == "" {
		runner.Opts.Baseline = filepath.Join(runner.Root, defaultSnapshotPath)
	}
	report := runner.Run()
	if cliF.jsonOutput {
		printCiReportJSON(report)
	} else {
		printCiReport(report, runner, cliF)
	}

	if !report.AllPassed() {
		os.Exit(1)
	}
}

// printCiReportJSON serializes the entire Report — checks +
// nested diagnostics — as a single JSON object on stdout. NDJSON
// would interleave with the per-diag --json mode used elsewhere;
// a Report is a tree, so a single object is the cleaner shape
// for downstream tooling (CI dashboards, release-note bots) to
// consume.
func printCiReportJSON(rep *ci.Report) {
	out := struct {
		ProjectRoot string        `json:"projectRoot"`
		StartedAt   any           `json:"startedAt"`
		FinishedAt  any           `json:"finishedAt"`
		Checks      []ciCheckJSON `json:"checks"`
	}{
		ProjectRoot: rep.ProjectRoot,
		StartedAt:   rep.StartedAt,
		FinishedAt:  rep.FinishedAt,
		Checks:      make([]ciCheckJSON, 0, len(rep.Checks)),
	}
	for _, c := range rep.Checks {
		out.Checks = append(out.Checks, ciCheckJSON{
			Name:    c.Name,
			Passed:  c.Passed,
			Skipped: c.Skipped,
			Note:    c.Note,
			Diags:   c.Diags,
		})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "osty ci: encode: %v\n", err)
		os.Exit(1)
	}
}

type ciCheckJSON struct {
	Name    ci.CheckName       `json:"name"`
	Passed  bool               `json:"passed"`
	Skipped bool               `json:"skipped"`
	Note    string             `json:"note,omitempty"`
	Diags   []*diag.Diagnostic `json:"diagnostics,omitempty"`
}

// runCiSnapshot implements the `osty ci snapshot` sub-subcommand.
// It loads the project at PATH (default: cwd), resolves the root
// package, captures its exported API, and writes the result to
// --out (default: <project>/api-snapshot.json).
//
// Exit codes:
//
//	0  snapshot written
//	1  resolve / write failure
//	2  usage error
func runCiSnapshot(args []string) {
	fs := flag.NewFlagSet("ci snapshot", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty ci snapshot [-o OUT] [PATH]")
	}
	var out string
	fs.StringVar(&out, "o", "", "output path (default: <root>/api-snapshot.json)")
	fs.StringVar(&out, "out", "", "alias for -o")
	_ = fs.Parse(args)
	start := "."
	if fs.NArg() == 1 {
		start = fs.Arg(0)
	} else if fs.NArg() > 1 {
		fs.Usage()
		os.Exit(2)
	}

	runner := ci.NewRunner(start, &ci.Options{})
	if msg := runner.Load(); msg != "" {
		fmt.Fprintf(os.Stderr, "osty ci snapshot: %s\n", msg)
		os.Exit(1)
	}
	if len(runner.Packages) == 0 {
		fmt.Fprintf(os.Stderr, "osty ci snapshot: no package found at %s\n", start)
		os.Exit(1)
	}

	var pkgVersion, edition string
	if runner.Manifest != nil && runner.Manifest.HasPackage {
		pkgVersion = runner.Manifest.Package.Version
		edition = runner.Manifest.Package.Edition
	}
	// Workspace-aware snapshot: every loaded package contributes
	// its exported API. Single-package projects flow through the
	// same constructor — NewWorkspaceSnapshot fills both the v2
	// Packages map and the v1 single-package convenience fields.
	snap := ci.NewWorkspaceSnapshot(runner.Packages, pkgVersion, edition)

	target := out
	if target == "" {
		target = filepath.Join(runner.Root, defaultSnapshotPath)
	}
	if msg := ci.WriteSnapshot(target, snap); msg != "" {
		fmt.Fprintf(os.Stderr, "osty ci snapshot: %s\n", msg)
		os.Exit(1)
	}
	total := 0
	for _, syms := range snap.Packages {
		total += len(syms)
	}
	fmt.Printf("Wrote %s (%d packages, %d symbols)\n", target, len(snap.Packages), total)
}

// printCiReport streams one human-readable line per check plus an
// aggregated diagnostic block. Uses the existing diag.Formatter so
// positioned diagnostics keep their source snippets.
//
// When --json is set at the global flag level, each check is
// emitted as NDJSON, matching every other subcommand's --json
// output contract.
func printCiReport(rep *ci.Report, runner *ci.Runner, cliF cliFlags) {
	// Sort diagnostics per check by position for stable output.
	for _, c := range rep.Checks {
		sort.SliceStable(c.Diags, func(i, j int) bool {
			a, b := c.Diags[i].PrimaryPos(), c.Diags[j].PrimaryPos()
			if a.Line != b.Line {
				return a.Line < b.Line
			}
			return a.Column < b.Column
		})
	}

	f := newFormatter("", nil, cliF)
	for _, c := range rep.Checks {
		switch {
		case c.Skipped:
			fmt.Fprintf(os.Stderr, "SKIP  %-10s  %s\n", c.Name, c.Note)
			continue
		case c.Passed:
			note := c.Note
			if note == "" {
				note = "ok"
			}
			fmt.Fprintf(os.Stderr, "PASS  %-10s  %s\n", c.Name, note)
		default:
			fmt.Fprintf(os.Stderr, "FAIL  %-10s\n", c.Name)
		}
		if len(c.Diags) > 0 {
			printDiags(f, c.Diags, cliF)
		}
	}

	// Final summary line so scripts can grep for it.
	errs, warns := 0, 0
	for _, c := range rep.Checks {
		for _, d := range c.Diags {
			switch d.Severity {
			case diag.Error:
				errs++
			case diag.Warning:
				warns++
			}
		}
	}
	if rep.AllPassed() {
		fmt.Fprintf(os.Stderr, "osty ci: OK (%d warnings)\n", warns)
	} else {
		fmt.Fprintf(os.Stderr, "osty ci: FAILED (%d errors, %d warnings)\n", errs, warns)
	}
	_ = runner
}
