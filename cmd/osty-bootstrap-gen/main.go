// osty-bootstrap-gen is the private developer binary that regenerates
// internal/selfhost/generated.go from the toolchain Osty sources.
//
// It is not part of the public Osty compiler surface: the public `osty`
// CLI exposes only the LLVM backend. The Osty→Go transpiler at
// internal/bootstrap/gen lives on solely to rebuild the bootstrap seed
// until the LLVM backend can self-host the toolchain directly.
//
// The binary intentionally keeps a minimal pipeline: parse → resolve →
// check → emit. It runs against a single merged .osty source produced
// by internal/selfhost/bundle.MergeToolchainChecker and writes gofmt'd
// Go source to the path given via -o.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/osty/osty/internal/bootstrap/seedgen"
)

func main() {
	fs := flag.NewFlagSet("osty-bootstrap-gen", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty-bootstrap-gen [--package NAME] [-o OUT] FILE.osty")
	}
	var outPath, pkgName string
	fs.StringVar(&outPath, "o", "", "write generated Go source to this file instead of stdout")
	fs.StringVar(&outPath, "out", "", "alias for -o")
	fs.StringVar(&pkgName, "package", "main", "Go package clause to emit (default: main)")
	_ = fs.Parse(os.Args[1:])
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	out, err := seedgen.Generate(seedgen.Config{
		SourcePath:  fs.Arg(0),
		PackageName: pkgName,
	})
	if outPath != "" && out != nil {
		if writeErr := os.WriteFile(outPath, out, 0o644); writeErr != nil {
			fmt.Fprintf(os.Stderr, "osty-bootstrap-gen: %v\n", writeErr)
			os.Exit(1)
		}
	} else if out != nil {
		if _, writeErr := os.Stdout.Write(out); writeErr != nil {
			fmt.Fprintf(os.Stderr, "osty-bootstrap-gen: %v\n", writeErr)
			os.Exit(1)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty-bootstrap-gen: %v\n", err)
		os.Exit(1)
	}
}
