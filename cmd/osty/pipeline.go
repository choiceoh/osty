package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/osty/osty/internal/pipeline"
)

// runPipeline implements `osty pipeline FILE [--json] [--trace]`.
//
// The default rendering is a fixed-width table summarising each
// front-end phase: lex → parse → resolve → check → lint, with timing,
// a one-line output summary, and per-phase error/warning counts. A
// closing histogram breaks down diagnostics by code so the dominant
// rule is obvious at a glance.
//
// `--json` emits a stable machine-readable schema instead of the
// table — handy for benchmarking dashboards or CI gates.
//
// `--trace` switches from "render after every phase finished" to
// "stream each phase line to stderr as it completes", so you can see
// where a slow front-end is spending its time even on a long file.
// `--trace` and `--json` can be combined: the trace lines go to
// stderr, the JSON document lands on stdout.
func runPipeline(args []string) {
	fs := flag.NewFlagSet("pipeline", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty pipeline [--json] [--trace] FILE")
	}
	var jsonOut, trace bool
	fs.BoolVar(&jsonOut, "json", false, "emit machine-readable JSON instead of the text table")
	fs.BoolVar(&trace, "trace", false, "stream a trace line to stderr as each phase completes")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}

	path := fs.Arg(0)
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty pipeline: %v\n", err)
		os.Exit(1)
	}

	var stream *os.File
	if trace {
		stream = os.Stderr
	}
	r := pipeline.Run(src, stream)

	if jsonOut {
		if err := r.RenderJSON(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "osty pipeline: %v\n", err)
			os.Exit(1)
		}
	} else {
		r.RenderText(os.Stdout)
	}

	// Mirror the exit-code conventions of the other front-end commands:
	// any error-severity diag aborts with exit 1.
	for _, s := range r.Stages {
		if s.Errors > 0 {
			os.Exit(1)
		}
	}
}
