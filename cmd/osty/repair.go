package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"github.com/osty/osty/internal/repair"
)

// runRepair implements `osty repair`: a small pre-parser source fixer for
// common AI-authored Osty mistakes.
func runRepair(args []string) {
	fs := flag.NewFlagSet("repair", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty repair [--check] [--write] FILE")
	}
	var checkMode, writeMode bool
	fs.BoolVar(&checkMode, "check", false, "exit 1 if FILE would be repaired")
	fs.BoolVar(&checkMode, "c", false, "alias for --check")
	fs.BoolVar(&writeMode, "write", false, "overwrite FILE in place")
	fs.BoolVar(&writeMode, "w", false, "alias for --write")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	if checkMode && writeMode {
		fmt.Fprintln(os.Stderr, "osty repair: --check and --write are mutually exclusive")
		os.Exit(2)
	}

	path := fs.Arg(0)
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty repair: %v\n", err)
		os.Exit(1)
	}
	res := repair.Source(src)
	changed := !bytes.Equal(src, res.Source)

	if checkMode {
		if changed {
			reportRepairSummary(path, res)
			os.Exit(1)
		}
		return
	}
	if writeMode {
		if changed {
			if err := os.WriteFile(path, res.Source, 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "osty repair: %v\n", err)
				os.Exit(1)
			}
		}
		reportRepairSummary(path, res)
		return
	}

	if _, err := os.Stdout.Write(res.Source); err != nil {
		fmt.Fprintf(os.Stderr, "osty repair: %v\n", err)
		os.Exit(1)
	}
	reportRepairSummary(path, res)
}

func reportRepairSummary(path string, res repair.Result) {
	for _, c := range res.Changes {
		fmt.Fprintf(os.Stderr, "%s:%d:%d: repair %s: %s\n",
			path, c.Pos.Line, c.Pos.Column, c.Kind, c.Message)
	}
	if len(res.Changes) == 0 && res.Skipped == 0 {
		fmt.Fprintf(os.Stderr, "osty repair: %s already clean\n", path)
		return
	}
	if res.Skipped > 0 {
		fmt.Fprintf(os.Stderr, "osty repair: applied %d repair(s), skipped %d overlapping edit(s)\n",
			len(res.Changes), res.Skipped)
		return
	}
	fmt.Fprintf(os.Stderr, "osty repair: applied %d repair(s)\n", len(res.Changes))
}
