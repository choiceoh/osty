package main

import (
	"flag"
	"fmt"
	"os"
)

// runFetch implements `osty fetch`: resolve dependencies and warm the
// caches without compiling anything. Useful in CI where you want to
// download dependencies once (and cache the directory) before running
// build / test / lint in parallel.
//
// Implementation is just `resolveAndVendorEnvOpts` — the same shared
// helper every other manifest-driven command uses — with no further
// pipeline work. `--locked` and `--frozen` make fetch a strict
// integrity check: it succeeds iff the existing lockfile already
// describes a complete, downloadable graph.
//
// Exit codes: 0 success, 1 I/O failure, 2 manifest error,
// 3 resolve / lockfile-drift failure.
func runFetch(args []string, cliF cliFlags) {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty fetch [--offline | --locked | --frozen]")
	}
	var (
		offline bool
		locked  bool
		frozen  bool
	)
	fs.BoolVar(&offline, "offline", false, "do not contact the network; use the existing cache")
	fs.BoolVar(&locked, "locked", false, "fail if osty.lock would change")
	fs.BoolVar(&frozen, "frozen", false, "imply --locked --offline; require an existing osty.lock")
	_ = fs.Parse(args)

	m, root, abort := loadManifestWithDiag(".", cliF)
	if abort {
		os.Exit(2)
	}
	graph, _, err := resolveAndVendorEnvOpts(m, root, resolveOpts{
		Offline: offline, Locked: locked, Frozen: frozen,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty fetch: %v\n", err)
		os.Exit(3)
	}
	count := 0
	if graph != nil {
		count = len(graph.Nodes)
	}
	if count == 0 {
		fmt.Println("No dependencies declared")
		return
	}
	fmt.Printf("Fetched %d dependencies\n", count)
	for _, name := range graph.Order {
		n := graph.Nodes[name]
		if n == nil || n.Fetched == nil {
			continue
		}
		fmt.Printf("  %s %s (%s)\n", name, n.Fetched.Version, n.Source.URI())
	}
	_ = cliF
}
