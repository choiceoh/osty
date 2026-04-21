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
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

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
	// Force the embedded selfhost checker for regen: the exec+JSON
	// boundary around osty-native-checker is pure overhead here — we
	// already link the same selfhost package into this binary, so
	// calling it in-process avoids a subprocess build, a fork, and
	// marshal/unmarshal of a ~1 MB payload.
	//
	// Additionally persist checker results under .osty/cache: a regen
	// iteration that edits internal/bootstrap/gen/*.go rebuilds this
	// binary but leaves both the merged toolchain source and the
	// compiled-in selfhost checker identical, so the ~tens-of-seconds
	// CheckPackageStructured call collapses to a JSON read on the
	// second and subsequent runs.
	enableCheckerCache()
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

// enableCheckerCache pins the in-process checker and, when a repo-local
// cache directory is available, memoises CheckPackageStructured results
// across regen invocations. Falls back to the plain embedded path if the
// repo root or checker fingerprint cannot be resolved — caching is a
// best-effort optimisation, not a correctness dependency.
func enableCheckerCache() {
	root, err := findRepoRootFromCwd()
	if err != nil {
		check.UseEmbeddedNativeChecker()
		return
	}
	validity := checkerFingerprint(root)
	if validity == "" {
		check.UseEmbeddedNativeChecker()
		return
	}
	cacheDir := filepath.Join(root, ".osty", "cache", "bootstrap-gen-checker")
	check.UseCachedEmbeddedNativeChecker(cacheDir, validity)
}

// checkerFingerprint produces a digest of the Go sources that compile
// into the embedded selfhost checker. A cache entry keyed on this
// fingerprint is only reused by a bootstrap-gen binary built from the
// same checker sources, so edits to internal/selfhost/generated.go
// (or the astbridge bundle) invalidate prior entries transparently.
//
// Go version and GOOS/GOARCH are folded in so a cache written on one
// toolchain is not replayed into a different one.
func checkerFingerprint(root string) string {
	h := sha256.New()
	fmt.Fprintf(h, "go=%s\nos=%s\narch=%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	files := []string{
		"internal/selfhost/generated.go",
		"internal/selfhost/astbridge/generated.go",
	}
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return ""
		}
		fmt.Fprintf(h, "%s=%d\n", rel, len(data))
		h.Write(data)
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:12])
}

func findRepoRootFromCwd() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod ancestor of %s", dir)
		}
		dir = parent
	}
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
