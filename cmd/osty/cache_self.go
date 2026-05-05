package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/osty/osty/internal/toolchain/selfhostcache"
)

// runCacheSelf prints the canonical content-addressed cache entry
// for the current `(toolchain SHA, host triple)` tuple. Used by
// shell tooling (`scripts/verify-self-rebuild`, `just bootstrap`)
// to consult the cache without spawning a Go process per query
// or duplicating the SHA logic in bash.
//
// Modes:
//
//	osty cache-self                     # print absolute path; exit 0
//	                                      regardless of whether the
//	                                      file exists (path-only).
//	osty cache-self --check             # print path; exit 0 only when
//	                                      the file exists. Exit 1 +
//	                                      diagnostic to stderr on miss.
//	osty cache-self --key               # print the cache key
//	                                      `<sha>-<triple>` instead of
//	                                      the full path. Useful for
//	                                      manifest filename derivation.
//	osty cache-self --triple            # print only the host triple.
//
// Flag combinations are mutually exclusive — passing more than one
// is a usage error.
func runCacheSelf(args []string, _ cliFlags) {
	fs := flag.NewFlagSet("cache-self", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, cacheSelfUsage())
		fs.PrintDefaults()
	}
	var (
		check  bool
		keyOut bool
		triple bool
	)
	fs.BoolVar(&check, "check", false, "exit non-zero when the cached binary is absent")
	fs.BoolVar(&keyOut, "key", false, "print the cache key (<sha>-<triple>) instead of the path")
	fs.BoolVar(&triple, "triple", false, "print only the host triple stamp")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if countTrue(keyOut, triple) > 1 {
		fmt.Fprintln(os.Stderr, "osty cache-self: --key and --triple are mutually exclusive")
		os.Exit(2)
	}

	if triple {
		fmt.Println(selfhostcache.HostTriple())
		return
	}

	root, err := selfhostcache.LocateProjectRoot(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty cache-self: locate project root: %v\n", err)
		os.Exit(1)
	}
	key, err := selfhostcache.ComputeKey(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty cache-self: compute key: %v\n", err)
		os.Exit(1)
	}
	if keyOut {
		fmt.Println(key.String())
		return
	}

	path := selfhostcache.CachePath(root, key)
	if check {
		info, err := os.Stat(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			fmt.Fprintf(os.Stderr, "osty cache-self: no cached binary at %s\n", path)
			os.Exit(1)
		case err != nil:
			fmt.Fprintf(os.Stderr, "osty cache-self: stat %s: %v\n", path, err)
			os.Exit(1)
		case info.IsDir():
			fmt.Fprintf(os.Stderr, "osty cache-self: %s is a directory, not a binary\n", path)
			os.Exit(1)
		}
	}
	fmt.Println(path)
}

func countTrue(bs ...bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

func cacheSelfUsage() string {
	return strings.TrimSpace(`
osty cache-self [--check] [--key | --triple]
    Print the canonical .osty/cache/self-host/<sha>-<triple>/osty-self
    path for the current toolchain source state and host triple.
    --check exits non-zero when the cached binary is missing so
    shell scripts can branch on cache hit/miss without parsing the
    output. --key prints just the <sha>-<triple> stem; --triple
    prints just the host triple stamp.
`)
}
