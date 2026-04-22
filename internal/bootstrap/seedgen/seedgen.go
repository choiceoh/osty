package seedgen

import (
	"crypto/sha256"
	"encoding/hex"
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

// Config drives developer-only bootstrap Osty->Go generation.
type Config struct {
	SourcePath  string
	PackageName string
	RepoRoot    string
}

// Generate transpiles a merged Osty source file into source-mapped Go.
//
// The caller owns output installation and may still receive non-nil output
// alongside a non-nil error when the underlying emitter produced partial Go.
func Generate(cfg Config) ([]byte, error) {
	// Force the embedded selfhost checker for regen: the exec+JSON
	// boundary around osty-native-checker is pure overhead here — we
	// already link the same selfhost package into this binary, so
	// calling it in-process avoids a subprocess build, a fork, and
	// marshal/unmarshal of a ~1 MB payload.
	//
	// Additionally persist checker results under .osty/cache: a regen
	// iteration that edits internal/bootstrap/gen/*.go or this wrapper
	// package rebuilds the caller but leaves both the merged toolchain
	// source and the compiled-in selfhost checker identical, so the
	// ~tens-of-seconds CheckPackageStructured call collapses to a JSON
	// read on the second and subsequent runs.
	enableCheckerCache(cfg.RepoRoot)
	absPath, err := filepath.Abs(cfg.SourcePath)
	if err != nil {
		return nil, err
	}

	pkgName := cfg.PackageName
	if pkgName == "" {
		pkgName = "main"
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
		return nil, err
	}
	ws.Stdlib = reg
	if _, err := ws.LoadPackage(""); err != nil {
		return nil, err
	}
	results := ws.ResolveAll()

	pkg := ws.Packages[""]
	if pkg == nil {
		return nil, fmt.Errorf("no package sources loaded from %s", filepath.Dir(absPath))
	}
	rootRes := results[""]
	if rootRes != nil && hasError(rootRes.Diags) {
		return nil, reportDiags("resolve", rootRes.Diags)
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
		return nil, fmt.Errorf("%s is not part of the package rooted at %s", absPath, pkg.Dir)
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
		FileScope: entryFile.FileScope,
		Diags:     res.Diags,
	}

	out, emitErr := gen.GenerateMapped(pkgName, entryFile.File, fileRes, chk, absPath)
	if out == nil && emitErr != nil {
		return nil, emitErr
	}
	return out, emitErr
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
func enableCheckerCache(repoRoot string) {
	root := repoRoot
	if root == "" {
		var err error
		root, err = findRepoRootFromCwd()
		if err != nil {
			check.UseEmbeddedNativeChecker()
			return
		}
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		check.UseEmbeddedNativeChecker()
		return
	}
	validity := checkerFingerprint(absRoot)
	if validity == "" {
		check.UseEmbeddedNativeChecker()
		return
	}
	cacheDir := filepath.Join(absRoot, ".osty", "cache", "bootstrap-gen-checker")
	check.UseCachedEmbeddedNativeChecker(cacheDir, validity)
}

// checkerFingerprint produces a digest of the Go sources that compile
// into the embedded selfhost checker. A cache entry keyed on this
// fingerprint is only reused by a bootstrap-gen caller built from the
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
