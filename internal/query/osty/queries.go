package osty

import (
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
	CheckFile *query.Query[string, *check.Result]

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

	qs.LintFile = query.Register(db, "LintFile",
		func(ctx *query.Ctx, path string) *lint.Result {
			pr := qs.Parse.Fetch(ctx, path)
			if pr.File == nil {
				return &lint.Result{}
			}
			rr := qs.ResolveFile.Fetch(ctx, path)
			chk := qs.CheckFile.Fetch(ctx, path)
			return lint.File(pr.File, rr, chk)
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
