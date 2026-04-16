//go:build !selfhostgen

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/osty/osty/internal/backend"
)

func runTest(args []string, flags cliFlags) {
	fs := flag.NewFlagSet("test", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty test [--offline | --locked | --frozen] [--backend NAME] [--emit MODE] [PATH|FILTER...]")
	}
	var offline, locked, frozen bool
	fs.BoolVar(&offline, "offline", false, "do not fetch dependencies; fail if caches are missing")
	fs.BoolVar(&locked, "locked", false, "fail if osty.lock would change")
	fs.BoolVar(&frozen, "frozen", false, "imply --locked --offline; require an existing osty.lock")
	var backendName string
	var emitName string
	fs.StringVar(&backendName, "backend", defaultBackendName(), "code generation backend (llvm)")
	fs.StringVar(&emitName, "emit", "", "artifact mode to execute (binary)")
	var pf profileFlags
	pf.register(fs)
	_ = fs.Parse(args)
	_ = flags
	_ = offline
	_ = locked
	_ = frozen
	_ = pf

	backendID, emitMode := resolveBackendAndEmitFlags("test", backendName, emitName)
	if backendID != backend.NameLLVM || emitMode != backend.EmitBinary {
		fmt.Fprintf(os.Stderr, "osty test: backend %q cannot execute tests with emit mode %q\n", backendID, emitMode)
		os.Exit(2)
	}
	fmt.Fprintln(os.Stderr, "osty test: native backend test harness is not implemented yet")
	os.Exit(2)
}
