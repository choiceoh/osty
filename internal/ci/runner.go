package ci

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

const ciSkipDirective = "osty:ci-skip"

// Load reads the project metadata for r.Root. It tolerates a
// missing manifest (standalone source directory) but refuses a
// manifest whose Parse failed at the TOML level — that's a hard
// error because later checks can't reason about a tree whose
// shape is unknowable.
//
// Load is separated from Run so the CLI layer can pre-populate r
// with its own manifest-diagnostic rendering (matching what
// `osty build` / `osty publish` do) before handing control to Run.
func (r *Runner) Load() error {
	// Try to locate osty.toml upward from root. Not finding one is
	// not an error — `osty ci .` on a directory of hand-rolled
	// samples still runs the format + lint checks.
	//
	// The CLI layer typically pre-renders manifest diagnostics
	// before constructing the Runner, then sets r.Manifest +
	// r.Root directly via the constructor's pipeline; in that
	// case we trust the caller and skip the redundant LoadDir.
	if r.Manifest == nil {
		if mRoot, err := manifest.FindRoot(r.Root); err == nil {
			m, _, loadErr := manifest.LoadDir(mRoot)
			if loadErr != nil {
				return loadErr
			}
			r.Manifest = m
			r.Root = mRoot
		}
	}

	// Workspace mode when [workspace] is declared OR when the tree
	// has no root sources but sibling packages — the exact policy
	// `cmd/osty` uses for `build` / `check`.
	if r.Manifest != nil && r.Manifest.Workspace != nil {
		return r.loadWorkspace(true)
	}
	if !hasDirectPackageSources(r.Root) && resolve.IsWorkspaceRoot(r.Root, "") {
		return r.loadWorkspace(false)
	}
	pkg, err := resolve.LoadPackage(r.Root)
	if err != nil {
		// Single-file directories / empty trees still let the
		// format check run over whatever .osty files are present.
		// Record nothing and let each check decide to skip.
		return nil
	}
	filterPackageCIFiles(pkg)
	r.Packages = []*resolve.Package{pkg}
	pr := resolve.ResolvePackage(pkg, resolve.NewPrelude())
	r.Results = []*resolve.PackageResult{pr}
	return nil
}

func hasDirectPackageSources(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".osty") && !strings.HasSuffix(name, "_test.osty") {
			return true
		}
	}
	return false
}

// loadWorkspace loads every member described by the manifest (or
// discovered by directory layout when the manifest is absent). A
// single failed member becomes a skip — we still want the other
// checks to run on the healthy parts of the tree.
func (r *Runner) loadWorkspace(byManifest bool) error {
	ws, err := resolve.NewWorkspace(r.Root)
	if err != nil {
		return err
	}
	ws.Stdlib = stdlib.LoadCached()
	r.Workspace = ws

	seen := map[string]bool{}
	pkgPaths := []string{}
	add := func(member string) {
		if seen[member] {
			return
		}
		seen[member] = true
		if pkg, err := ws.LoadPackage(member); err == nil && pkg != nil {
			filterPackageCIFiles(pkg)
			r.Packages = append(r.Packages, pkg)
			pkgPaths = append(pkgPaths, member)
		}
	}
	if byManifest && r.Manifest != nil {
		if r.Manifest.HasPackage {
			add("")
		}
		if r.Manifest.Workspace != nil {
			for _, mem := range r.Manifest.Workspace.Members {
				add(mem)
			}
		}
	} else {
		for _, p := range resolve.WorkspacePackagePaths(r.Root) {
			add(p)
		}
	}
	// Run resolution once for the whole workspace so cross-package
	// `use` edges resolve and lint sees the same state cmd/osty's
	// `build` / `check` paths see.
	allResults := ws.ResolveAll()
	for _, path := range pkgPaths {
		r.Results = append(r.Results, allResults[path])
	}
	return nil
}

func filterPackageCIFiles(pkg *resolve.Package) {
	if pkg == nil || len(pkg.Files) == 0 {
		return
	}
	files := pkg.Files[:0]
	for _, pf := range pkg.Files {
		if pf == nil || bytes.Contains(pf.Source, []byte(ciSkipDirective)) {
			continue
		}
		files = append(files, pf)
	}
	pkg.Files = files
}

// Run executes every enabled check and returns the aggregated
// Report. Errors returned from individual checks are recorded as
// diagnostics on the corresponding Check; Run itself only returns
// an error for setup failures (manifest parse errors) that were
// not already surfaced by Load.
//
// The order of checks is stable and deterministic — callers get
// the same Report layout every run so CI tooling can diff output.
func (r *Runner) Run() *Report {
	rep := &Report{
		ProjectRoot: r.Root,
		StartedAt:   time.Now().UTC(),
	}
	// Order matches the docstring on Runner: cheap checks first
	// (format / policy / lockfile) then the deeper ones
	// (lint / release / semver). Reordering changes the visible
	// output, so keep it stable.
	if r.Opts.Format {
		rep.Checks = append(rep.Checks, r.checkFormat())
	} else {
		rep.Checks = append(rep.Checks, skipped(CheckFormat))
	}
	if r.Opts.Policy {
		rep.Checks = append(rep.Checks, r.checkPolicy())
	} else {
		rep.Checks = append(rep.Checks, skipped(CheckPolicy))
	}
	if r.Opts.Lockfile {
		rep.Checks = append(rep.Checks, r.checkLockfile())
	} else {
		rep.Checks = append(rep.Checks, skipped(CheckLockfile))
	}
	if r.Opts.Lint {
		rep.Checks = append(rep.Checks, r.checkLint())
	} else {
		rep.Checks = append(rep.Checks, skipped(CheckLint))
	}
	if r.Opts.Release {
		rep.Checks = append(rep.Checks, r.checkRelease())
	} else {
		rep.Checks = append(rep.Checks, skipped(CheckRelease))
	}
	if r.Opts.Semver {
		rep.Checks = append(rep.Checks, r.checkSemver())
	} else {
		rep.Checks = append(rep.Checks, skipped(CheckSemver))
	}
	// Finalize Passed per check based on severity + Strict mode.
	for _, c := range rep.Checks {
		if c.Skipped {
			continue
		}
		c.Passed = !hasError(c.Diags) &&
			!(r.Opts.Strict && hasWarning(c.Diags))
	}
	rep.FinishedAt = time.Now().UTC()
	return rep
}

func skipped(name CheckName) *Check {
	return &Check{Name: name, Skipped: true, Note: "not enabled"}
}

// ostyFiles returns every .osty path under root (recursively),
// excluding vendored .osty/deps/ contents and anything matching
// the manifest's [lint.exclude] globs. Sorted for determinism.
//
// Helper shared by the format and policy checks; lint uses the
// resolver-side package walk so cross-file refs resolve.
func (r *Runner) ostyFiles() []string {
	files := map[string]bool{}
	for _, pkg := range r.Packages {
		for _, pf := range pkg.Files {
			if pf == nil || pf.Path == "" {
				continue
			}
			files[pf.Path] = true
		}
	}
	// When Load found no package at all, fall back to a filesystem
	// walk so loose source trees still get a format pass. If packages
	// were loaded but their files were explicitly CI-skipped, do not
	// walk back into fixtures and samples.
	if len(files) == 0 && len(r.Packages) == 0 {
		_ = filepath.WalkDir(r.Root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d == nil {
				return nil
			}
			if d.IsDir() {
				base := d.Name()
				// Skip vendored deps + .git + .osty metadata dir
				if base == ".osty" || base == ".git" || base == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) == ".osty" {
				src, err := os.ReadFile(path)
				if err != nil || !bytes.Contains(src, []byte(ciSkipDirective)) {
					files[path] = true
				}
			}
			return nil
		})
	}
	out := make([]string, 0, len(files))
	for p := range files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
