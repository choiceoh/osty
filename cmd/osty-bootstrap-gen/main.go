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
	"path/filepath"

	"github.com/osty/osty/internal/bootstrap/gen"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
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
	if err := run(fs.Arg(0), pkgName, outPath); err != nil {
		fmt.Fprintf(os.Stderr, "osty-bootstrap-gen: %v\n", err)
		os.Exit(1)
	}
}

func run(sourcePath, pkgName, outPath string) error {
	absPath, err := filepath.Abs(sourcePath)
	if err != nil {
		return err
	}

	reg := stdlib.LoadCached()
	opts := check.Opts{
		UseGolegacy:   true,
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
	}

	ws, err := resolve.NewWorkspace(filepath.Dir(absPath))
	if err != nil {
		return err
	}
	ws.Stdlib = reg
	if _, err := ws.LoadPackage(""); err != nil {
		return err
	}
	results := ws.ResolveAll()

	pkg := ws.Packages[""]
	if pkg == nil {
		return fmt.Errorf("no package sources loaded from %s", filepath.Dir(absPath))
	}
	rootRes := results[""]
	if rootRes != nil && hasError(rootRes.Diags) {
		return reportDiags("resolve", rootRes.Diags)
	}

	checks := check.Workspace(ws, results, opts)
	var entryFile *resolve.PackageFile
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		fp, err := filepath.Abs(pf.Path)
		if err != nil {
			continue
		}
		if fp == absPath {
			entryFile = pf
			break
		}
	}
	if entryFile == nil {
		return fmt.Errorf("%s is not part of the package rooted at %s", absPath, pkg.Dir)
	}
	res := rootRes
	if res == nil {
		res = &resolve.PackageResult{}
	}
	chk := checks[""]
	if chk == nil {
		chk = &check.Result{}
	}

	fileRes := &resolve.Result{
		Refs:      entryFile.Refs,
		TypeRefs:  entryFile.TypeRefs,
		FileScope: entryFile.FileScope,
		Diags:     res.Diags,
	}

	out, emitErr := gen.GenerateMapped(pkgName, entryFile.File, fileRes, chk, absPath)
	if out == nil && emitErr != nil {
		return emitErr
	}

	if outPath != "" {
		if err := os.WriteFile(outPath, out, 0o644); err != nil {
			return err
		}
	} else if _, err := os.Stdout.Write(out); err != nil {
		return err
	}
	return emitErr
}

func hasError(diags []*diag.Diagnostic) bool {
	for _, d := range diags {
		if d != nil && d.Severity == diag.Error {
			return true
		}
	}
	return false
}

func reportDiags(stage string, diags []*diag.Diagnostic) error {
	for _, d := range diags {
		if d == nil || d.Severity != diag.Error {
			continue
		}
		fmt.Fprintf(os.Stderr, "osty-bootstrap-gen: %s: %s\n", stage, d.Message)
	}
	return fmt.Errorf("%s produced errors", stage)
}
