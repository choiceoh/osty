// scaffold_cmd.go — `osty scaffold` subcommand dispatcher.
//
// This is the CLI side of the small code-generation tools that live
// in internal/scaffold (fixture / schema / ffi). They share a flag
// shape — every generator takes `--out DIR` (default ".") and
// produces a single .osty file derived from the positional NAME — so
// the dispatcher centralises argument parsing and exit-code mapping.

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/osty/osty/internal/scaffold"
)

// runScaffold dispatches `osty scaffold <kind> ...` to the per-kind
// runner. An unknown kind exits 2 with a usage message so the user
// sees the full set of generators at a glance.
func runScaffold(args []string) {
	if len(args) < 1 {
		scaffoldUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "fixture":
		runScaffoldFixture(args[1:])
	case "schema":
		runScaffoldSchema(args[1:])
	case "ffi":
		runScaffoldFFI(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "osty scaffold: unknown generator %q\n", args[0])
		scaffoldUsage()
		os.Exit(2)
	}
}

func scaffoldUsage() {
	fmt.Fprintln(os.Stderr, "usage: osty scaffold <fixture|schema|ffi> [flags] NAME")
	fmt.Fprintln(os.Stderr, "  fixture NAME [--cases N] [--out DIR]")
	fmt.Fprintln(os.Stderr, "      generate a table-driven `*_test.osty` skeleton")
	fmt.Fprintln(os.Stderr, "  schema NAME --from FILE.json [--out DIR]")
	fmt.Fprintln(os.Stderr, "      infer a `pub struct` from a JSON sample payload")
	fmt.Fprintln(os.Stderr, "  ffi NAME --header FILE.h [--out DIR]")
	fmt.Fprintln(os.Stderr, "      emit Osty wrapper stubs for top-level C function decls")
}

func runScaffoldFixture(args []string) {
	fs := flag.NewFlagSet("scaffold fixture", flag.ExitOnError)
	var cases int
	var out string
	fs.IntVar(&cases, "cases", 3, "number of placeholder rows in the generated table")
	fs.StringVar(&out, "out", ".", "directory to write the fixture into")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty scaffold fixture NAME [--cases N] [--out DIR]")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	path, d := scaffold.WriteFixture(out, scaffold.FixtureOptions{
		Name:  fs.Arg(0),
		Cases: cases,
	})
	if d != nil {
		printScaffoldDiag(d)
		os.Exit(scaffoldExitCode(d))
	}
	fmt.Printf("Wrote fixture %s\n", path)
}

func runScaffoldSchema(args []string) {
	fs := flag.NewFlagSet("scaffold schema", flag.ExitOnError)
	var from, out string
	fs.StringVar(&from, "from", "", "path to a JSON sample (object) to infer fields from")
	fs.StringVar(&out, "out", ".", "directory to write the generated struct into")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty scaffold schema NAME --from FILE.json [--out DIR]")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	if fs.NArg() != 1 || from == "" {
		fs.Usage()
		os.Exit(2)
	}
	sample, err := os.ReadFile(from)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty scaffold schema: %v\n", err)
		os.Exit(1)
	}
	path, d := scaffold.WriteSchema(out, scaffold.SchemaOptions{
		Name:   fs.Arg(0),
		Sample: sample,
	})
	if d != nil {
		printScaffoldDiag(d)
		os.Exit(scaffoldExitCode(d))
	}
	fmt.Printf("Wrote schema %s\n", path)
}

func runScaffoldFFI(args []string) {
	fs := flag.NewFlagSet("scaffold ffi", flag.ExitOnError)
	var header, out string
	fs.StringVar(&header, "header", "", "path to a C header to scan for `<retType> <name>(<args>);` decls")
	fs.StringVar(&out, "out", ".", "directory to write the generated bindings into")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty scaffold ffi NAME --header FILE.h [--out DIR]")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)
	if fs.NArg() != 1 || header == "" {
		fs.Usage()
		os.Exit(2)
	}
	src, err := os.ReadFile(header)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty scaffold ffi: %v\n", err)
		os.Exit(1)
	}
	path, d := scaffold.WriteFFI(out, scaffold.FFIOptions{
		Module: fs.Arg(0),
		Header: src,
	})
	if d != nil {
		printScaffoldDiag(d)
		os.Exit(scaffoldExitCode(d))
	}
	fmt.Printf("Wrote FFI bindings %s\n", path)
}
