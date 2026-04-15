package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/osty/osty/internal/ci"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
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

	// Defer to the Runner's default Baseline lookup only when
	// --semver is on and no explicit path was given — otherwise
	// the runner would emit CI401 for an empty Baseline.
	if semverOn && baseline == "" {
		baseline = defaultSnapshotPath
	}

	opts := ci.Options{
		Format:       fmtOn,
		Lint:         lintOn,
		Policy:       policyOn,
		Lockfile:     lockOn,
		Release:      releaseOn,
		Semver:       semverOn,
		Strict:       strict,
		Baseline:     baseline,
		MaxFileBytes: maxFileBytes,
	}

	// Manifest load + diag rendering first — same pattern every
	// other manifest-aware command uses, so parse errors are
	// rendered with the proper caret underlines before CI starts.
	if _, _, err := manifestLookupNear(start); err == nil {
		if _, _, abort := loadManifestWithDiag(start, cliF); abort {
			os.Exit(2)
		}
	}

	runner := ci.NewRunner(start, opts)
	if err := runner.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "osty ci: %v\n", err)
		os.Exit(2)
	}
	report := runner.Run()
	printCiReport(report, runner, cliF)

	if !report.AllPassed() {
		os.Exit(1)
	}
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

	runner := ci.NewRunner(start, ci.Options{})
	if err := runner.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "osty ci snapshot: %v\n", err)
		os.Exit(1)
	}
	if len(runner.Packages) == 0 {
		fmt.Fprintf(os.Stderr, "osty ci snapshot: no package found at %s\n", start)
		os.Exit(1)
	}
	// Resolve to populate PkgScope. Load only fills pkg.Files;
	// without a subsequent resolve pass the PkgScope is empty and
	// every symbol looks private.
	pkg := runner.Packages[0]
	_ = resolve.ResolvePackage(pkg, resolve.NewPrelude())

	var pkgVersion, edition string
	if runner.Manifest != nil && runner.Manifest.HasPackage {
		pkgVersion = runner.Manifest.Package.Version
		edition = runner.Manifest.Package.Edition
	}
	snap := ci.CapturePackage(pkg, pkgVersion, edition)

	target := out
	if target == "" {
		target = filepath.Join(runner.Root, defaultSnapshotPath)
	}
	if err := ci.WriteSnapshot(target, snap); err != nil {
		fmt.Fprintf(os.Stderr, "osty ci snapshot: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %s (%d symbols)\n", target, len(snap.Symbols))
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
