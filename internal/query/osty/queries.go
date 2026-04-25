package osty

import (
	"sort"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/canonical"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/lint"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/query"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/sourcemap"
)

// ---- Value types ----

// ParseResult is the output of the [Parse] query — the AST plus any
// lexer/parser diagnostics. The Source field holds the bytes the
// parser consumed, enabling diagnostic rendering without re-reading
// the file.
type ParseResult struct {
	Source          []byte
	CanonicalSource []byte
	CanonicalMap    *sourcemap.Map
	File            *ast.File
	Diags           []*diag.Diagnostic
	Provenance      *parser.Provenance
}

// ResolvedPackage is an immutable view over a resolved package. Once
// returned from [Queries.ResolvePackage] the pointer must not be
// mutated by the caller; the Database holds it verbatim.
type ResolvedPackage struct {
	pkg *resolve.Package
	res *resolve.PackageResult
}

// Package returns the underlying resolve.Package. Callers must treat
// the returned pointer as read-only.
func (rp *ResolvedPackage) Package() *resolve.Package { return rp.pkg }

// PackageResult returns the resolver's summary output (package scope
// and diagnostics). Read-only.
func (rp *ResolvedPackage) PackageResult() *resolve.PackageResult { return rp.res }

// FileResult produces a per-file resolve.Result slice for the given
// path. Returns nil if the path does not belong to this package.
func (rp *ResolvedPackage) FileResult(path string) *resolve.Result {
	if rp == nil || rp.pkg == nil {
		return nil
	}
	norm := NormalizePath(path)
	for _, pf := range rp.pkg.Files {
		if NormalizePath(pf.Path) != norm {
			continue
		}
		return &resolve.Result{
			RefsByID:      pf.RefsByID,
			TypeRefsByID:  pf.TypeRefsByID,
			RefIdents:     pf.RefIdents,
			TypeRefIdents: pf.TypeRefIdents,
			FileScope:     pf.FileScope,
			Diags:         rp.res.Diags,
		}
	}
	return nil
}

// ResolvedWorkspace is the output of the [Queries.ResolveWorkspace]
// query. It holds an immutable snapshot of the full cross-package
// resolution of a workspace.
//
// Callers (typically the LSP server's analyzeWorkspaceViaEngine)
// use the per-directory and per-file accessors to slice out the
// portions they need for diagnostic publishing and cross-file
// handlers.
type ResolvedWorkspace struct {
	root     string
	pkgs     []*resolve.Package
	pkgMap   map[string]*resolve.Package       // normalized dir → Package
	results  map[string]*resolve.PackageResult // normalized dir → PackageResult
	resolved map[string]*ResolvedPackage       // normalized dir → ResolvedPackage
}

// Root returns the workspace root directory (normalized).
func (rw *ResolvedWorkspace) Root() string { return rw.root }

// Packages returns every loaded package. Callers must treat the
// returned slice as read-only.
func (rw *ResolvedWorkspace) Packages() []*resolve.Package { return rw.pkgs }

// PackageByDir returns the resolve.Package for the given normalized
// directory, or nil if the directory is not part of this workspace.
func (rw *ResolvedWorkspace) PackageByDir(dir string) *resolve.Package {
	if rw == nil {
		return nil
	}
	return rw.pkgMap[dir]
}

// ResultByDir returns the resolve.PackageResult for the given
// normalized directory, or nil.
func (rw *ResolvedWorkspace) ResultByDir(dir string) *resolve.PackageResult {
	if rw == nil {
		return nil
	}
	return rw.results[dir]
}

// ResolvedByDir returns the ResolvedPackage for the given normalized
// directory, or nil.
func (rw *ResolvedWorkspace) ResolvedByDir(dir string) *ResolvedPackage {
	if rw == nil {
		return nil
	}
	return rw.resolved[dir]
}

// DirForPath returns the normalized directory of the package that
// contains the given file path, or "" if the file is not in any
// workspace package.
func (rw *ResolvedWorkspace) DirForPath(path string) string {
	if rw == nil {
		return ""
	}
	dir := PackageDirOf(path)
	if _, ok := rw.pkgMap[dir]; ok {
		return dir
	}
	return ""
}

// FileResult produces a per-file resolve.Result for the given path.
// Returns nil if the path does not belong to any package in this
// workspace.
func (rw *ResolvedWorkspace) FileResult(path string) *resolve.Result {
	if rw == nil {
		return nil
	}
	dir := PackageDirOf(path)
	pkg := rw.pkgMap[dir]
	if pkg == nil {
		return nil
	}
	pr := rw.results[dir]
	norm := NormalizePath(path)
	for _, pf := range pkg.Files {
		if NormalizePath(pf.Path) != norm {
			continue
		}
		result := &resolve.Result{
			RefsByID:      pf.RefsByID,
			TypeRefsByID:  pf.TypeRefsByID,
			RefIdents:     pf.RefIdents,
			TypeRefIdents: pf.TypeRefIdents,
			FileScope:     pf.FileScope,
		}
		if pr != nil {
			result.Diags = pr.Diags
		}
		return result
	}
	return nil
}

// WorkspaceCheckResult is the output of the [Queries.CheckWorkspace]
// query. It maps normalized package directories to their check
// results.
type WorkspaceCheckResult struct {
	byDir map[string]*check.Result
}

// ResultByDir returns the check.Result for the given normalized
// directory, or nil.
func (wcr *WorkspaceCheckResult) ResultByDir(dir string) *check.Result {
	if wcr == nil {
		return nil
	}
	return wcr.byDir[dir]
}

// Len returns the number of packages with check results.
func (wcr *WorkspaceCheckResult) Len() int {
	if wcr == nil {
		return 0
	}
	return len(wcr.byDir)
}

// ---- Queries struct ----

// Queries groups the derived query handles. Constructed once by
// [NewEngine] and reused for the Database's lifetime.
type Queries struct {
	// Parse: (path) -> parsed AST + diagnostics.
	// Depends on: SourceText(path).
	Parse *query.Query[string, ParseResult]

	// BuildPackage: (dir) -> fresh *resolve.Package with PackageFiles
	// assembled from Parse'd contents. Not directly useful to
	// callers; exists so ResolvePackage can rebuild cleanly on each
	// run without retaining a pointer that the resolver has mutated.
	// Depends on: PackageFiles(dir), Parse(f) for each f.
	BuildPackage *query.Query[string, *resolve.Package]

	// ResolvePackage: (dir) -> resolved view of the package. Runs the
	// resolver over BuildPackage's fresh Package. The returned
	// ResolvedPackage is immutable.
	// Depends on: BuildPackage(dir).
	ResolvePackage *query.Query[string, *ResolvedPackage]

	// ResolveFile: (path) -> per-file resolve.Result sliced out of
	// ResolvePackage. Convenience for file-centric callers.
	// Depends on: ResolvePackage(packageDirOf(path)).
	ResolveFile *query.Query[string, *resolve.Result]

	// CheckPackage: (dir) -> type-check result for the whole package.
	// Depends on: ResolvePackage(dir).
	CheckPackage *query.Query[string, *check.Result]

	// CheckFile: (path) -> type-check result covering the file. In
	// package mode this shares maps with CheckPackage for that dir.
	// Depends on: CheckPackage(packageDirOf(path)).
	//
	// NOTE: CheckFile uses the per-package ResolvePackage/CheckPackage
	// chain. For workspace mode, callers should use ResolveWorkspace
	// and CheckWorkspace directly and slice per-file results from
	// their output. The engine's mutex design prevents transparent
	// mode-switching inside a query body.
	CheckFile *query.Query[string, *check.Result]

	// ResolveWorkspace: (rootDir) -> full cross-package resolution.
	// Assembles a resolve.Workspace from BuildPackage outputs and
	// runs ws.ResolveAll(). Use this when WorkspaceMembers has been
	// seeded (workspace mode) instead of the per-package
	// ResolvePackage query.
	// Depends on: WorkspaceMembers, BuildPackage(dir) for each dir.
	ResolveWorkspace *query.Query[string, *ResolvedWorkspace]

	// CheckWorkspace: (rootDir) -> per-package check results for
	// the entire workspace. Calls check.Workspace with the output
	// of ResolveWorkspace.
	// Depends on: ResolveWorkspace(rootDir).
	CheckWorkspace *query.Query[string, *WorkspaceCheckResult]

	// LintFile: (path) -> lint diagnostics for one file.
	// Depends on: Parse(path), ResolveFile(path), CheckFile(path).
	LintFile *query.Query[string, *lint.Result]

	// IdentIndex: (path) -> offset → resolver Symbol. Used by LSP
	// hover / completion for O(1) position lookup.
	// Depends on: ResolveFile(path).
	IdentIndex *query.Query[string, map[int]*resolve.Symbol]

	// FileDiagnostics: (path) -> unified diagnostic list for the
	// file, aggregating parse, resolve (per-file), check (per-file),
	// and lint results. This is the one query LSP's publishDiagnostics
	// ultimately needs.
	// Depends on: Parse(path), ResolveFile(path), CheckFile(path),
	// LintFile(path).
	FileDiagnostics *query.Query[string, []*diag.Diagnostic]
}

func registerQueries(db *query.Database, inp Inputs) Queries {
	var qs Queries

	qs.Parse = query.Register(db, "Parse",
		func(ctx *query.Ctx, path string) ParseResult {
			src := inp.SourceText.Fetch(ctx, path)
			parsed := parser.ParseDetailed(src)
			canonicalSrc, canonicalMap := canonical.SourceWithMap(src, parsed.File)
			return ParseResult{
				Source:          src,
				CanonicalSource: canonicalSrc,
				CanonicalMap:    canonicalMap,
				File:            parsed.File,
				Diags:           parsed.Diagnostics,
				Provenance:      parsed.Provenance,
			}
		},
		hashParseResult,
	)

	// BuildPackage has no hashFn: its output carries fresh
	// *resolve.Package + *ast.File pointers every run and cannot be
	// content-compared cheaply. Downstream ResolvePackage supplies
	// the real cutoff via its semantic output hash, so BuildPackage
	// just bumps computedAt on every rerun.
	qs.BuildPackage = query.Register(db, "BuildPackage",
		func(ctx *query.Ctx, dir string) *resolve.Package {
			files := inp.PackageFiles.Fetch(ctx, dir)
			pkg := &resolve.Package{
				Dir:   dir,
				Name:  packageNameFromDir(dir),
				Files: make([]*resolve.PackageFile, 0, len(files)),
			}
			for _, f := range files {
				pr := qs.Parse.Fetch(ctx, f)
				pkg.Files = append(pkg.Files, &resolve.PackageFile{
					Path:            f,
					Source:          pr.Source,
					CanonicalSource: pr.CanonicalSource,
					CanonicalMap:    pr.CanonicalMap,
					File:            pr.File,
					ParseDiags:      pr.Diags,
					ParseProvenance: pr.Provenance,
				})
			}
			return pkg
		},
		nil,
	)

	qs.ResolvePackage = query.Register(db, "ResolvePackage",
		func(ctx *query.Ctx, dir string) *ResolvedPackage {
			built := qs.BuildPackage.Fetch(ctx, dir)
			// Allocate a brand-new Package with copied PackageFile
			// entries so resolve.ResolvePackage's in-place mutation
			// doesn't corrupt the BuildPackage cache. We reuse the
			// already-parsed *ast.File pointers — they are read-only.
			pkg := &resolve.Package{
				Dir:   built.Dir,
				Name:  built.Name,
				Files: make([]*resolve.PackageFile, len(built.Files)),
			}
			for i, pf := range built.Files {
				pkg.Files[i] = &resolve.PackageFile{
					Path:            pf.Path,
					Source:          pf.Source,
					CanonicalSource: pf.CanonicalSource,
					CanonicalMap:    pf.CanonicalMap,
					File:            pf.File,
					ParseDiags:      pf.ParseDiags,
					ParseProvenance: pf.ParseProvenance,
				}
			}
			// Stdlib attachment requires an unexported resolve.Workspace
			// field; until that's exposed, package-mode resolution
			// matches the LSP's pre-query behavior (no auto std import).
			res := resolve.ResolvePackage(pkg, ctx.Prelude())
			return &ResolvedPackage{pkg: pkg, res: res}
		},
		hashResolvedPackage,
	)

	qs.ResolveFile = query.Register(db, "ResolveFile",
		func(ctx *query.Ctx, path string) *resolve.Result {
			dir := PackageDirOf(path)
			rp := qs.ResolvePackage.Fetch(ctx, dir)
			if rp == nil {
				return &resolve.Result{}
			}
			r := rp.FileResult(path)
			if r == nil {
				return &resolve.Result{}
			}
			return r
		},
		hashResolveFileResult,
	)

	qs.CheckPackage = query.Register(db, "CheckPackage",
		func(ctx *query.Ctx, dir string) *check.Result {
			rp := qs.ResolvePackage.Fetch(ctx, dir)
			if rp == nil || rp.pkg == nil {
				return &check.Result{}
			}
			opts := check.Opts{Stdlib: resolveStdlibProvider(ctx)}
			return check.Package(rp.pkg, rp.res, opts)
		},
		hashCheckResult,
	)

	qs.CheckFile = query.Register(db, "CheckFile",
		func(ctx *query.Ctx, path string) *check.Result {
			dir := PackageDirOf(path)
			return qs.CheckPackage.Fetch(ctx, dir)
		},
		hashCheckResult,
	)

	// ResolveWorkspace assembles all packages listed in
	// WorkspaceMembers into a resolve.Workspace, deep-copies each
	// package (so ResolveAll's in-place mutation doesn't corrupt
	// the BuildPackage cache), and runs cross-package resolution.
	qs.ResolveWorkspace = query.Register(db, "ResolveWorkspace",
		func(ctx *query.Ctx, rootDir string) *ResolvedWorkspace {
			members := inp.WorkspaceMembers.Fetch(ctx, struct{}{})
			if len(members) == 0 {
				return nil
			}

			// Build all packages from the engine's cached Parse
			// results. Deep-copy each one so ResolveAll's in-place
			// mutation doesn't corrupt the BuildPackage cache.
			builtPkgs := make(map[string]*resolve.Package, len(members))
			for _, dir := range members {
				bp := qs.BuildPackage.Fetch(ctx, dir)
				if bp == nil || len(bp.Files) == 0 {
					continue
				}
				builtPkgs[dir] = copyPackageForWorkspace(bp)
			}
			if len(builtPkgs) == 0 {
				return nil
			}

			// Assemble a resolve.Workspace. Package keys are dotted
			// import paths (e.g. "", "app", "lib") derived from the
			// relative path to the workspace root.
			ws, _ := resolve.NewWorkspace(rootDir)
			for dir, pkg := range builtPkgs {
				dotPath := dotPathFromDir(rootDir, dir)
				ws.Packages[dotPath] = pkg
			}

			// Cross-package resolution (declare pass + body pass).
			resolved := ws.ResolveAll()

			// Build the result, converting dotted-path keys back to
			// normalized directory keys.
			pkgList := make([]*resolve.Package, 0, len(builtPkgs))
			resultsByDir := make(map[string]*resolve.PackageResult, len(builtPkgs))
			resolvedByDir := make(map[string]*ResolvedPackage, len(builtPkgs))
			for dir, pkg := range builtPkgs {
				pkgList = append(pkgList, pkg)
				dotPath := dotPathFromDir(rootDir, dir)
				if pr, ok := resolved[dotPath]; ok {
					resultsByDir[dir] = pr
					resolvedByDir[dir] = &ResolvedPackage{pkg: pkg, res: pr}
				}
			}
			return &ResolvedWorkspace{
				root:     rootDir,
				pkgs:     pkgList,
				pkgMap:   builtPkgs,
				results:  resultsByDir,
				resolved: resolvedByDir,
			}
		},
		hashResolvedWorkspaceFn,
	)

	// CheckWorkspace runs check.Workspace over the resolved packages.
	// It rebuilds a temporary resolve.Workspace from the
	// ResolveWorkspace output because check.Workspace expects one.
	qs.CheckWorkspace = query.Register(db, "CheckWorkspace",
		func(ctx *query.Ctx, rootDir string) *WorkspaceCheckResult {
			rw := qs.ResolveWorkspace.Fetch(ctx, rootDir)
			if rw == nil || len(rw.resolved) == 0 {
				return &WorkspaceCheckResult{}
			}

			// Rebuild a resolve.Workspace + resolved map for
			// check.Workspace, which expects dotted-path keys.
			ws, _ := resolve.NewWorkspace(rootDir)
			resolvedMap := make(map[string]*resolve.PackageResult, len(rw.resolved))
			for dir, rp := range rw.resolved {
				dotPath := dotPathFromDir(rootDir, dir)
				ws.Packages[dotPath] = rp.pkg
				resolvedMap[dotPath] = rp.res
			}

			opts := check.Opts{Stdlib: resolveStdlibProvider(ctx)}
			checks := check.Workspace(ws, resolvedMap, opts)

			// Convert dotted-path keys back to directory keys.
			byDir := make(map[string]*check.Result, len(checks))
			for dotPath, result := range checks {
				dir := dirFromDotPath(rootDir, dotPath)
				if dir != "" {
					byDir[dir] = result
				}
			}
			return &WorkspaceCheckResult{byDir: byDir}
		},
		hashWorkspaceCheckResultFn,
	)

	qs.LintFile = query.Register(db, "LintFile",
		func(ctx *query.Ctx, path string) *lint.Result {
			pr := qs.Parse.Fetch(ctx, path)
			if pr.File == nil {
				return &lint.Result{}
			}
			rr := qs.ResolveFile.Fetch(ctx, path)
			chk := qs.CheckFile.Fetch(ctx, path)
			return lint.File(pr.File, pr.Source, rr, chk)
		},
		hashLintResult,
	)

	qs.IdentIndex = query.Register(db, "IdentIndex",
		func(ctx *query.Ctx, path string) map[int]*resolve.Symbol {
			rr := qs.ResolveFile.Fetch(ctx, path)
			return buildIdentIndex(rr)
		},
		hashIdentIndex,
	)

	qs.FileDiagnostics = query.Register(db, "FileDiagnostics",
		func(ctx *query.Ctx, path string) []*diag.Diagnostic {
			pr := qs.Parse.Fetch(ctx, path)
			rr := qs.ResolveFile.Fetch(ctx, path)
			chk := qs.CheckFile.Fetch(ctx, path)
			lr := qs.LintFile.Fetch(ctx, path)
			return collectFileDiagnostics(path, pr, rr, chk, lr)
		},
		hashDiagList,
	)

	return qs
}

// ---- Helpers ----

// packageNameFromDir returns a stable name for a package given its
// directory. Uses the basename so the resolver's error messages have
// something more meaningful than a full absolute path.
func packageNameFromDir(dir string) string {
	if dir == "" {
		return "<pkg>"
	}
	for i := len(dir) - 1; i >= 0; i-- {
		if dir[i] == '/' || dir[i] == '\\' {
			if i == len(dir)-1 {
				continue
			}
			return dir[i+1:]
		}
	}
	return dir
}

// resolveStdlibProvider returns the stdlib provider suitable for
// passing to check.Opts. Returns nil if the Database was constructed
// without a stdlib registry.
func resolveStdlibProvider(ctx *query.Ctx) resolve.StdlibProvider {
	reg := ctx.Stdlib()
	if reg == nil {
		return nil
	}
	return reg
}

// buildIdentIndex inverts the resolve.Result's Refs and TypeRefs
// maps into an offset → Symbol map. Mirrors the pre-query
// implementation lifted from internal/lsp/server.go.
func buildIdentIndex(r *resolve.Result) map[int]*resolve.Symbol {
	if r == nil {
		return map[int]*resolve.Symbol{}
	}
	out := make(map[int]*resolve.Symbol, len(r.RefIdents)+len(r.TypeRefIdents))
	for _, id := range r.RefIdents {
		out[id.Pos().Offset] = r.RefsByID[id.ID]
	}
	for _, nt := range r.TypeRefIdents {
		out[nt.Pos().Offset] = r.TypeRefsByID[nt.ID]
	}
	return out
}

// collectFileDiagnostics merges every diagnostic produced by the
// pipeline into one sorted list for the given file. Filtering by
// path is necessary because the package-level resolve/check
// diagnostics may cover multiple files.
func collectFileDiagnostics(
	path string,
	pr ParseResult,
	rr *resolve.Result,
	chk *check.Result,
	lr *lint.Result,
) []*diag.Diagnostic {
	norm := NormalizePath(path)
	out := make([]*diag.Diagnostic, 0, len(pr.Diags)+len(rr.Diags)+len(chk.Diags)+len(lr.Diags))
	appendFiltered := func(ds []*diag.Diagnostic) {
		for _, d := range ds {
			if d == nil {
				continue
			}
			if !diagnosticBelongsTo(d, norm) {
				continue
			}
			out = append(out, d)
		}
	}
	// Parse diagnostics are always about the file that was parsed —
	// no position-based filtering needed.
	out = append(out, pr.Diags...)
	appendFiltered(rr.Diags)
	appendFiltered(chk.Diags)
	appendFiltered(lr.Diags)
	return out
}

// diagnosticBelongsTo is a placeholder for per-file filtering. Span
// filtering by path can't be done until token.Pos carries a file
// identifier; callers currently receive every diagnostic and sort by
// position for rendering.
func diagnosticBelongsTo(d *diag.Diagnostic, normalizedPath string) bool {
	return d != nil
}

// ---- Workspace helpers ----

// dotPathFromDir converts a normalized package directory to the
// dotted import path used by resolve.Workspace. Returns "" for the
// root package (dir == root).
//
// Both root and dir must be normalized (forward slashes, no trailing /).
func dotPathFromDir(root, dir string) string {
	if dir == root {
		return ""
	}
	prefix := root + "/"
	if !strings.HasPrefix(dir, prefix) {
		return ""
	}
	return dir[len(prefix):]
}

// dirFromDotPath converts a dotted import path back to a normalized
// directory. Returns root for the empty dotted path (root package).
func dirFromDotPath(root, dotPath string) string {
	if dotPath == "" {
		return root
	}
	return root + "/" + dotPath
}

// copyPackageForWorkspace creates a deep copy of a resolve.Package
// suitable for passing to resolve.Workspace.ResolveAll. The original
// package's *ast.File pointers are reused (they are read-only), but
// the PackageFile slice and the Package struct itself are fresh so
// ResolveAll's in-place mutation (setting PkgScope, RefsByID, etc.)
// does not corrupt the BuildPackage cache.
func copyPackageForWorkspace(src *resolve.Package) *resolve.Package {
	if src == nil {
		return nil
	}
	pkg := &resolve.Package{
		Dir:   src.Dir,
		Name:  src.Name,
		Files: make([]*resolve.PackageFile, len(src.Files)),
	}
	for i, pf := range src.Files {
		if pf == nil {
			continue
		}
		pkg.Files[i] = &resolve.PackageFile{
			Path:            pf.Path,
			Source:          pf.Source,
			CanonicalSource: pf.CanonicalSource,
			CanonicalMap:    pf.CanonicalMap,
			File:            pf.File,
			ParseDiags:      pf.ParseDiags,
			ParseProvenance: pf.ParseProvenance,
		}
	}
	return pkg
}

// hashResolvedWorkspaceFn fingerprints a ResolvedWorkspace by hashing
// each per-directory ResolvedPackage in sorted order.
func hashResolvedWorkspaceFn(rw *ResolvedWorkspace) [32]byte {
	h := newHasher()
	if rw == nil {
		return h.sum()
	}
	h.str(rw.root)

	dirs := make([]string, 0, len(rw.resolved))
	for dir := range rw.resolved {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	h.u32(uint32(len(dirs)))
	for _, dir := range dirs {
		h.str(dir)
		rp := rw.resolved[dir]
		sub := hashResolvedPackage(rp)
		h.h.Write(sub[:])
	}
	return h.sum()
}

// hashWorkspaceCheckResultFn fingerprints a WorkspaceCheckResult by
// delegating to the shared hashCheckWorkspaceMap helper.
func hashWorkspaceCheckResultFn(wcr *WorkspaceCheckResult) [32]byte {
	if wcr == nil {
		return [32]byte{}
	}
	return hashCheckWorkspaceMap(wcr.byDir)
}
