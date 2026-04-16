package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/osty/osty/internal/diag"
	ostyquery "github.com/osty/osty/internal/query/osty"
)

// runQueryCheck implements
//
//	osty query-check [--metrics] [--twice] FILE
//
// Runs Parse / Resolve / Check / Lint / FileDiagnostics through the
// incremental query engine and prints any diagnostics. The flag
// --metrics dumps the engine's hit/miss/rerun/cutoff counters after
// the analysis, useful for observing cache behavior. --twice runs
// the analysis twice over the same input to exhibit cache hits on
// the second pass.
//
// Unlike `osty check`, which runs each invocation in a fresh process
// and thus gets no carry-over caching, this subcommand is primarily
// a demonstration / measurement tool for the engine. The engine
// itself is intended to be embedded in long-lived processes (LSP,
// build daemons) where cross-request caching pays off.
func runQueryCheck(args []string) {
	fs := flag.NewFlagSet("query-check", flag.ExitOnError)
	var (
		showMetrics bool
		runTwice    bool
	)
	fs.BoolVar(&showMetrics, "metrics", false, "print engine metrics after analysis")
	fs.BoolVar(&runTwice, "twice", false, "re-run the same analysis to exercise the cache path")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr,
			"usage: osty query-check [--metrics] [--twice] FILE")
	}
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	path := fs.Arg(0)

	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty query-check: %v\n", err)
		os.Exit(1)
	}

	eng := ostyquery.NewEngine()
	key := ostyquery.NormalizePath(path)
	dir := ostyquery.PackageDirOf(key)

	eng.Inputs.SourceText.Set(eng.DB, key, src)
	eng.Inputs.PackageFiles.Set(eng.DB, dir, []string{key})

	before := eng.DB.Metrics()
	diags := eng.Queries.FileDiagnostics.Get(eng.DB, key)
	firstDelta := eng.DB.Metrics().Sub(before)

	if showMetrics {
		fmt.Fprintf(os.Stderr, "first pass: %+v\n", firstDelta)
	}

	if runTwice {
		before = eng.DB.Metrics()
		diags = eng.Queries.FileDiagnostics.Get(eng.DB, key)
		secondDelta := eng.DB.Metrics().Sub(before)
		if showMetrics {
			fmt.Fprintf(os.Stderr, "second pass (cache): %+v\n", secondDelta)
		}
	}

	if len(diags) == 0 {
		return
	}
	f := &diag.Formatter{Filename: path, Source: src}
	fmt.Fprintln(os.Stderr, f.FormatAll(diags))

	anyErr := false
	for _, d := range diags {
		if d.Severity == diag.Error {
			anyErr = true
			break
		}
	}
	if anyErr {
		os.Exit(1)
	}
}
