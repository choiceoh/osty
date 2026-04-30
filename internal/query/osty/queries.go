package osty

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/canonical"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/cst"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/lint"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/query"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/semanticdb"
	"github.com/osty/osty/internal/sourcemap"
	"github.com/osty/osty/internal/spanid"
	"github.com/osty/osty/internal/stdlib"
)

// ---- Value types ----

// ParseResult is the output of the [Parse] query — the retained FrontendRun,
// public-AST compatibility output, and any lexer/parser diagnostics. The
// Source field holds the bytes the parser consumed, enabling diagnostic
// rendering without re-reading the file.
type ParseResult struct {
	Source          []byte
	SourceFileID    spanid.SourceFileID
	CanonicalSource []byte
	CanonicalMap    *sourcemap.Map
	File            *ast.File
	Run             *selfhost.FrontendRun
	Diags           []*diag.Diagnostic
	Provenance      *parser.Provenance
}

// CSTParseResult is the output of the [ParseCST] query — the lossless
// Red/Green tree plus parser diagnostics. The Source field is the normalized
// byte buffer covered by Tree.
type CSTParseResult struct {
	Source []byte
	Tree   *cst.Tree
	Diags  []*diag.Diagnostic
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

// SemanticDB returns the package's authoritative selfhost semantic database.
func (rp *ResolvedPackage) SemanticDB() *semanticdb.DB {
	if rp == nil || rp.res == nil {
		return nil
	}
	return rp.res.SemanticDB
}

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
	graph    *resolve.PackageGraph
	pkgs     []*resolve.Package
	pkgMap   map[string]*resolve.Package       // normalized dir → Package
	results  map[string]*resolve.PackageResult // normalized dir → PackageResult
	resolved map[string]*ResolvedPackage       // normalized dir → ResolvedPackage
	dotByDir map[string]string                 // normalized dir → resolve.Workspace dotted key
	dirByDot map[string]string                 // resolve.Workspace dotted key → normalized dir
}

// Root returns the workspace root directory (normalized).
func (rw *ResolvedWorkspace) Root() string { return rw.root }

// Graph returns the first-class package graph used for workspace resolution.
func (rw *ResolvedWorkspace) Graph() *resolve.PackageGraph {
	if rw == nil {
		return nil
	}
	return rw.graph
}

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

// SemanticDBByDir returns the resolve-phase semantic database for dir.
func (rw *ResolvedWorkspace) SemanticDBByDir(dir string) *semanticdb.DB {
	if rw == nil {
		return nil
	}
	pr := rw.results[dir]
	if pr == nil {
		return nil
	}
	return pr.SemanticDB
}

// DotPathByDir returns the resolve.Workspace key for dir.
func (rw *ResolvedWorkspace) DotPathByDir(dir string) string {
	if rw == nil {
		return ""
	}
	return rw.dotByDir[dir]
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

// SemanticDBByDir returns the combined resolve/check semantic database for dir.
func (wcr *WorkspaceCheckResult) SemanticDBByDir(dir string) *semanticdb.DB {
	result := wcr.ResultByDir(dir)
	if result == nil {
		return nil
	}
	return result.Semantic()
}

// Len returns the number of packages with check results.
func (wcr *WorkspaceCheckResult) Len() int {
	if wcr == nil {
		return 0
	}
	return len(wcr.byDir)
}

// LowerKey identifies a package-lowering query. Dir is the normalized package
// directory; PackageName is the backend module name; EntryPath selects the
// diagnostic/source anchor inside the package. When WorkspaceRoot is set,
// lowering slices the package out of ResolveWorkspace / CheckWorkspace instead
// of using standalone ResolvePackage / CheckPackage. SourcePath defaults to
// EntryPath when left empty.
type LowerKey struct {
	WorkspaceRoot string
	Dir           string
	PackageName   string
	SourcePath    string
	EntryPath     string
}

func (k LowerKey) normalized() LowerKey {
	if k.WorkspaceRoot != "" {
		k.WorkspaceRoot = NormalizePath(k.WorkspaceRoot)
	}
	k.Dir = NormalizePath(k.Dir)
	if k.EntryPath != "" {
		k.EntryPath = NormalizePath(k.EntryPath)
	}
	if k.SourcePath == "" {
		k.SourcePath = k.EntryPath
	}
	if k.SourcePath != "" {
		k.SourcePath = NormalizePath(k.SourcePath)
	}
	if k.PackageName == "" {
		k.PackageName = packageNameFromDir(k.Dir)
	}
	return k
}

// LowerIRResult is the output of LowerIRPackage. Err is set when the HIR
// boundary rejects the package (for example, IR validation fails).
type LowerIRResult struct {
	Entry backend.Entry
	Err   error
}

// LowerMIRResult is the output of LowerMIRPackage. Err carries any HIR-stage
// failure from LowerIRPackage; MIR validation findings remain non-fatal
// Entry.MIRIssues, matching backend.PreparePackage.
type LowerMIRResult struct {
	Entry backend.Entry
	Err   error
}

// EmitTarget identifies a backend artifact query. FeaturesKey is the stable
// feature-list encoding returned by FeatureKey.
type EmitTarget struct {
	Lower            LowerKey
	Backend          backend.Name
	Emit             backend.EmitMode
	Root             string
	Profile          string
	Target           string
	BinaryName       string
	FeaturesKey      string
	LinkLibrariesKey string
}

// NewEmitTarget builds a normalized key for the Emit query.
func NewEmitTarget(lower LowerKey, name backend.Name, mode backend.EmitMode, layout backend.Layout, binaryName string, features []string) EmitTarget {
	return EmitTarget{
		Lower:       lower,
		Backend:     name,
		Emit:        mode,
		Root:        layout.Root,
		Profile:     layout.Profile,
		Target:      layout.Target,
		BinaryName:  binaryName,
		FeaturesKey: FeatureKey(features),
	}.normalized()
}

// WithLinkLibraries returns a copy of t carrying ordered native link library
// names that should be forwarded to the backend link step.
func (t EmitTarget) WithLinkLibraries(libraries []string) EmitTarget {
	t.LinkLibrariesKey = LinkLibrariesKey(libraries)
	return t.normalized()
}

func (t EmitTarget) normalized() EmitTarget {
	t.Lower = t.Lower.normalized()
	if t.Backend == "" {
		t.Backend = backend.NameLLVM
	}
	if t.Emit == "" {
		t.Emit = backend.EmitLLVMIR
	}
	if t.Root != "" {
		t.Root = NormalizePath(t.Root)
	}
	t.FeaturesKey = FeatureKey(t.Features())
	t.LinkLibrariesKey = LinkLibrariesKey(t.LinkLibraries())
	return t
}

// Features decodes the stable feature-list encoding on EmitTarget.
func (t EmitTarget) Features() []string {
	if t.FeaturesKey == "" {
		return nil
	}
	return strings.Split(t.FeaturesKey, featureKeySep)
}

// LinkLibraries decodes the ordered native link-library list on EmitTarget.
func (t EmitTarget) LinkLibraries() []string {
	if t.LinkLibrariesKey == "" {
		return nil
	}
	return strings.Split(t.LinkLibrariesKey, featureKeySep)
}

const featureKeySep = "\x00"

// FeatureKey returns a comparable, order-insensitive feature-list key for
// query keys. Backend feature handling treats the list as a set.
func FeatureKey(features []string) string {
	if len(features) == 0 {
		return ""
	}
	clean := make([]string, 0, len(features))
	for _, f := range features {
		f = strings.TrimSpace(f)
		if f != "" {
			clean = append(clean, f)
		}
	}
	if len(clean) == 0 {
		return ""
	}
	sort.Strings(clean)
	return strings.Join(clean, featureKeySep)
}

// LinkLibrariesKey returns an order-preserving comparable key for linker
// libraries. Unlike features, link order can be semantically meaningful.
func LinkLibrariesKey(libraries []string) string {
	if len(libraries) == 0 {
		return ""
	}
	clean := make([]string, 0, len(libraries))
	for _, lib := range libraries {
		lib = strings.TrimSpace(lib)
		if lib != "" {
			clean = append(clean, lib)
		}
	}
	return strings.Join(clean, featureKeySep)
}

// EmitResult is the output of the Emit query.
type EmitResult struct {
	Result *backend.Result
	Err    error
}

// ---- Queries struct ----

// Queries groups the derived query handles. Constructed once by
// [NewEngine] and reused for the Database's lifetime.
type Queries struct {
	// Parse: (path) -> retained FrontendRun + public AST compatibility output
	// + diagnostics.
	// Depends on: SourceText(path).
	Parse *query.Query[string, ParseResult]

	// ParseCST: (path) -> lossless Red/Green CST + diagnostics.
	// Depends on: SourceText(path).
	ParseCST *query.Query[string, CSTParseResult]

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
	// Assembles a PackageGraph from BuildPackage outputs and runs
	// resolve.ResolveGraph. Uses WorkspacePackages when seeded so CLI
	// build can preserve dependency-manager import keys, otherwise
	// falls back to WorkspaceMembers for LSP/workspace callers.
	// Depends on: WorkspacePackages or WorkspaceMembers, BuildPackage(dir)
	// for each dir.
	ResolveWorkspace *query.Query[string, *ResolvedWorkspace]

	// CheckWorkspace: (rootDir) -> per-package check results for
	// the entire workspace. Calls check.PackageGraph with the output
	// of ResolveWorkspace.
	// Depends on: ResolveWorkspace(rootDir).
	CheckWorkspace *query.Query[string, *WorkspaceCheckResult]

	// LowerIRPackage: (LowerKey) -> optimized, validated HIR backend entry.
	// Depends on: ResolvePackage(dir), CheckPackage(dir).
	LowerIRPackage *query.Query[LowerKey, LowerIRResult]

	// LowerMIRPackage: (LowerKey) -> backend entry with MIR populated.
	// Depends on: LowerIRPackage(key).
	LowerMIRPackage *query.Query[LowerKey, LowerMIRResult]

	// Emit: (EmitTarget) -> backend artifact result.
	// Depends on: LowerMIRPackage(target.Lower).
	Emit *query.Query[EmitTarget, EmitResult]

	// LintFile: (path) -> lint diagnostics for one file. The self-hosted
	// lint pass owns parsing internally, so this query stays source-only and
	// does not force public AST materialization through Parse.
	// Depends on: SourceText(path).
	LintFile *query.Query[string, *lint.Result]

	// IdentIndex: (path) -> offset → symbol kind. Used by LSP semantic
	// tokens for O(1) position lookup. This is derived directly from the
	// selfhost structured resolve result, so it does not need Go Scope /
	// RefsByID compatibility maps.
	// Depends on: BuildPackage(packageDirOf(path)).
	IdentIndex *query.Query[string, map[int]string]

	// ResolveDiagnostics: (path) -> resolver diagnostics for the owning
	// package, sliced to this file. Derived directly from the selfhost
	// structured resolve result instead of the Go compatibility Result.
	// Depends on: BuildPackage(packageDirOf(path)).
	ResolveDiagnostics *query.Query[string, []*diag.Diagnostic]

	// FileDiagnostics: (path) -> unified diagnostic list for the
	// file, aggregating parse, resolve (per-file), check (per-file),
	// and lint results. This is the one query LSP's publishDiagnostics
	// ultimately needs.
	// Depends on: Parse(path), ResolveDiagnostics(path), CheckFile(path),
	// LintFile(path).
	FileDiagnostics *query.Query[string, []*diag.Diagnostic]
}

func registerQueries(db *query.Database, inp Inputs) Queries {
	var qs Queries

	qs.Parse = query.Register(db, "Parse",
		func(ctx *query.Ctx, path string) ParseResult {
			src := inp.SourceText.Fetch(ctx, path)
			parsed := parser.ParseDetailed(src)
			canonicalSrc, canonicalMap := canonical.SourceWithMapForFile(path, src, parsed.File)
			diag.StampFile(parsed.Diagnostics, path)
			return ParseResult{
				Source:          src,
				SourceFileID:    spanid.SourceFileIDFor(path),
				CanonicalSource: canonicalSrc,
				CanonicalMap:    canonicalMap,
				File:            parsed.File,
				Run:             parsed.Run,
				Diags:           parsed.Diagnostics,
				Provenance:      parsed.Provenance,
			}
		},
		hashParseResult,
	)

	qs.ParseCST = query.Register(db, "ParseCST",
		func(ctx *query.Ctx, path string) CSTParseResult {
			src := inp.SourceText.Fetch(ctx, path)
			tree, diags := parser.ParseCST(src)
			normalized := cst.Normalize(src)
			return CSTParseResult{
				Source: normalized,
				Tree:   tree,
				Diags:  diags,
			}
		},
		hashCSTParseResult,
	)

	// BuildPackage has no hashFn: its output carries fresh
	// *resolve.Package + FrontendRun / *ast.File pointers every run and
	// cannot be content-compared cheaply. Downstream ResolvePackage supplies
	// the real cutoff via its semantic output hash, so BuildPackage just bumps
	// computedAt on every rerun.
	qs.BuildPackage = query.Register(db, "BuildPackage",
		func(ctx *query.Ctx, dir string) *resolve.Package {
			files := inp.PackageFiles.Fetch(ctx, dir)
			metadata := PackageMetadata{Name: packageNameFromDir(dir)}
			if inp.PackageMetadata.HasFetch(ctx, dir) {
				metadata = inp.PackageMetadata.Fetch(ctx, dir)
				if metadata.Name == "" {
					metadata.Name = packageNameFromDir(dir)
				}
			}
			pkg := &resolve.Package{
				Dir:               dir,
				Name:              metadata.Name,
				RuntimeCapability: metadata.RuntimeCapability,
				Files:             make([]*resolve.PackageFile, 0, len(files)),
			}
			for _, f := range files {
				pr := qs.Parse.Fetch(ctx, f)
				pkg.Files = append(pkg.Files, &resolve.PackageFile{
					Path:            f,
					Source:          pr.Source,
					SourceFileID:    pr.SourceFileID,
					CanonicalSource: pr.CanonicalSource,
					CanonicalMap:    pr.CanonicalMap,
					File:            pr.File,
					Run:             pr.Run,
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
			// already-parsed FrontendRun / *ast.File pointers — they
			// are read-only.
			pkg := &resolve.Package{
				Dir:               built.Dir,
				Name:              built.Name,
				RuntimeCapability: built.RuntimeCapability,
				Files:             make([]*resolve.PackageFile, len(built.Files)),
			}
			for i, pf := range built.Files {
				pkg.Files[i] = &resolve.PackageFile{
					Path:                   pf.Path,
					Source:                 pf.Source,
					SourceFileID:           pf.SourceFileID,
					OriginalSource:         pf.OriginalSource,
					SourceTransformApplied: pf.SourceTransformApplied,
					SourceTransformChanged: pf.SourceTransformChanged,
					TransformMap:           pf.TransformMap,
					CanonicalSource:        pf.CanonicalSource,
					CanonicalMap:           pf.CanonicalMap,
					File:                   pf.File,
					Run:                    pf.Run,
					ParseDiags:             pf.ParseDiags,
					ParseProvenance:        pf.ParseProvenance,
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
			opts := checkOptsFromCtx(ctx)
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

	// ResolveWorkspace assembles all seeded packages into a resolve.Workspace,
	// deep-copies each package (so ResolveAll's in-place mutation doesn't
	// corrupt the BuildPackage cache), and runs cross-package resolution.
	qs.ResolveWorkspace = query.Register(db, "ResolveWorkspace",
		func(ctx *query.Ctx, rootDir string) *ResolvedWorkspace {
			members := workspaceMembersForRoot(ctx, inp, rootDir)
			if len(members) == 0 {
				return nil
			}

			// Build all packages from the engine's cached Parse
			// results. Deep-copy each one so ResolveAll's in-place
			// mutation doesn't corrupt the BuildPackage cache.
			builtByDir := make(map[string]*resolve.Package, len(members))
			builtByDot := make(map[string]*resolve.Package, len(members))
			dotByDir := make(map[string]string, len(members))
			dirByDot := make(map[string]string, len(members))
			for _, member := range members {
				bp := qs.BuildPackage.Fetch(ctx, member.Dir)
				if bp == nil || len(bp.Files) == 0 {
					continue
				}
				pkg := copyPackageForWorkspace(bp)
				if member.Name != "" {
					pkg.Name = member.Name
				}
				builtByDir[member.Dir] = pkg
				builtByDot[member.DotPath] = pkg
				dotByDir[member.Dir] = member.DotPath
				dirByDot[member.DotPath] = member.Dir
			}
			if len(builtByDot) == 0 {
				return nil
			}

			// Assemble a resolve.Workspace. Package keys are dotted
			// import paths (e.g. "", "app", "lib", or a vendored
			// external dependency key).
			ws, _ := resolve.NewWorkspace(rootDir)
			ws.Stdlib = resolveStdlibProvider(ctx)
			for dotPath, pkg := range builtByDot {
				ws.Packages[dotPath] = pkg
			}

			// Cross-package resolution (declare pass + body pass).
			graph := resolve.NewPackageGraph(ws)
			resolved := resolve.ResolveGraph(graph)

			// Build the result, converting dotted-path keys back to
			// normalized directory keys.
			pkgList := make([]*resolve.Package, 0, len(builtByDir))
			resultsByDir := make(map[string]*resolve.PackageResult, len(builtByDir))
			resolvedByDir := make(map[string]*ResolvedPackage, len(builtByDir))
			for dir, pkg := range builtByDir {
				pkgList = append(pkgList, pkg)
				dotPath := dotByDir[dir]
				if pr, ok := resolved[dotPath]; ok {
					resultsByDir[dir] = pr
					resolvedByDir[dir] = &ResolvedPackage{pkg: pkg, res: pr}
				}
			}
			return &ResolvedWorkspace{
				root:     rootDir,
				graph:    graph,
				pkgs:     pkgList,
				pkgMap:   builtByDir,
				results:  resultsByDir,
				resolved: resolvedByDir,
				dotByDir: dotByDir,
				dirByDot: dirByDot,
			}
		},
		hashResolvedWorkspaceFn,
	)

	// CheckWorkspace runs check.PackageGraph over the resolved packages,
	// reusing the first-class graph captured by ResolveWorkspace.
	qs.CheckWorkspace = query.Register(db, "CheckWorkspace",
		func(ctx *query.Ctx, rootDir string) *WorkspaceCheckResult {
			rw := qs.ResolveWorkspace.Fetch(ctx, rootDir)
			if rw == nil || len(rw.resolved) == 0 {
				return &WorkspaceCheckResult{}
			}

			// Rebuild dotted-path result keys for check.PackageGraph.
			resolvedMap := make(map[string]*resolve.PackageResult, len(rw.resolved))
			for dir, rp := range rw.resolved {
				dotPath := rw.DotPathByDir(dir)
				resolvedMap[dotPath] = rp.res
			}

			opts := checkOptsFromCtx(ctx)
			checks := check.PackageGraph(rw.Graph(), resolvedMap, opts)

			// Convert dotted-path keys back to directory keys.
			byDir := make(map[string]*check.Result, len(checks))
			for dotPath, result := range checks {
				dir := rw.dirByDot[dotPath]
				if dir != "" {
					byDir[dir] = result
				}
			}
			return &WorkspaceCheckResult{byDir: byDir}
		},
		hashWorkspaceCheckResultFn,
	)

	qs.LowerIRPackage = query.Register(db, "LowerIRPackage",
		func(ctx *query.Ctx, key LowerKey) LowerIRResult {
			key = key.normalized()
			if key.WorkspaceRoot != "" {
				rw := qs.ResolveWorkspace.Fetch(ctx, key.WorkspaceRoot)
				cw := qs.CheckWorkspace.Fetch(ctx, key.WorkspaceRoot)
				if rw == nil {
					return LowerIRResult{Err: fmt.Errorf("query lowerIR: workspace %s not resolved", key.WorkspaceRoot)}
				}
				pkg := rw.PackageByDir(key.Dir)
				if pkg == nil {
					return LowerIRResult{Err: fmt.Errorf("query lowerIR: package %s not in workspace %s", key.Dir, key.WorkspaceRoot)}
				}
				var chk *check.Result
				if cw != nil {
					chk = cw.ResultByDir(key.Dir)
				}
				if chk == nil {
					chk = &check.Result{}
				}
				entryFile := packageEntryFileForPath(pkg, key.EntryPath)
				entry, err := backend.LowerGraphPackageIR(key.PackageName, key.SourcePath, rw.Graph(), rw.DotPathByDir(key.Dir), entryFile, chk)
				return LowerIRResult{Entry: entry, Err: err}
			}
			rp := qs.ResolvePackage.Fetch(ctx, key.Dir)
			chk := qs.CheckPackage.Fetch(ctx, key.Dir)
			if rp == nil || rp.pkg == nil {
				return LowerIRResult{Err: fmt.Errorf("query lowerIR: package %s not resolved", key.Dir)}
			}
			entryFile := packageEntryFileForPath(rp.pkg, key.EntryPath)
			entry, err := backend.LowerPackageIR(key.PackageName, key.SourcePath, rp.pkg, entryFile, chk)
			return LowerIRResult{Entry: entry, Err: err}
		},
		hashLowerIRResult,
	)

	qs.LowerMIRPackage = query.Register(db, "LowerMIRPackage",
		func(ctx *query.Ctx, key LowerKey) LowerMIRResult {
			key = key.normalized()
			lowered := qs.LowerIRPackage.Fetch(ctx, key)
			if lowered.Err != nil {
				return LowerMIRResult{Entry: lowered.Entry, Err: lowered.Err}
			}
			entry, err := backend.LowerEntryMIR(lowered.Entry)
			return LowerMIRResult{Entry: entry, Err: err}
		},
		hashLowerMIRResult,
	)

	qs.Emit = query.Register(db, "Emit",
		func(ctx *query.Ctx, target EmitTarget) EmitResult {
			target = target.normalized()
			lowered := qs.LowerMIRPackage.Fetch(ctx, target.Lower)
			if lowered.Err != nil {
				return EmitResult{Err: lowered.Err}
			}
			b, err := backend.New(target.Backend)
			if err != nil {
				return EmitResult{Err: err}
			}
			result, err := b.Emit(context.Background(), backend.Request{
				Layout: backend.Layout{
					Root:    target.Root,
					Profile: target.Profile,
					Target:  target.Target,
				},
				Emit:          target.Emit,
				Entry:         lowered.Entry,
				BinaryName:    target.BinaryName,
				Features:      target.Features(),
				LinkLibraries: target.LinkLibraries(),
			})
			return EmitResult{Result: result, Err: err}
		},
		hashEmitResult,
	)

	qs.LintFile = query.Register(db, "LintFile",
		func(ctx *query.Ctx, path string) *lint.Result {
			src := inp.SourceText.Fetch(ctx, path)
			return lint.Source(src)
		},
		hashLintResult,
	)

	qs.IdentIndex = query.Register(db, "IdentIndex",
		func(ctx *query.Ctx, path string) map[int]string {
			dir := PackageDirOf(path)
			pkg := qs.BuildPackage.Fetch(ctx, dir)
			index, err := resolve.NativeIdentKindIndex(pkg, path)
			if err != nil {
				return map[int]string{}
			}
			return index
		},
		hashIdentIndex,
	)

	qs.ResolveDiagnostics = query.Register(db, "ResolveDiagnostics",
		func(ctx *query.Ctx, path string) []*diag.Diagnostic {
			dir := PackageDirOf(path)
			pkg := qs.BuildPackage.Fetch(ctx, dir)
			return nativeResolveDiagnosticsForFile(path, pkg)
		},
		hashDiagList,
	)

	qs.FileDiagnostics = query.Register(db, "FileDiagnostics",
		func(ctx *query.Ctx, path string) []*diag.Diagnostic {
			pr := qs.Parse.Fetch(ctx, path)
			resolveDiags := qs.ResolveDiagnostics.Fetch(ctx, path)
			chk := qs.CheckFile.Fetch(ctx, path)
			lr := qs.LintFile.Fetch(ctx, path)
			return collectFileDiagnostics(path, pr, resolveDiags, chk, lr)
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

func packageEntryFileForPath(pkg *resolve.Package, entryPath string) *resolve.PackageFile {
	if pkg == nil {
		return nil
	}
	if entryPath != "" {
		norm := NormalizePath(entryPath)
		for _, pf := range pkg.Files {
			if pf != nil && NormalizePath(pf.Path) == norm {
				return pf
			}
		}
	}
	for _, pf := range pkg.Files {
		if pf != nil && pf.CanMaterializeFile() {
			return pf
		}
	}
	return nil
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

type stdlibRegistryAccessor interface {
	registry() *stdlib.Registry
}

func checkOptsFromCtx(ctx *query.Ctx) check.Opts {
	provider := resolveStdlibProvider(ctx)
	opts := check.Opts{Stdlib: provider}
	if reg := stdlibRegistryFromProvider(provider); reg != nil {
		opts.Primitives = reg.Primitives
		opts.ResultMethods = reg.ResultMethods
	}
	return opts
}

func stdlibRegistryFromProvider(provider resolve.StdlibProvider) *stdlib.Registry {
	switch p := provider.(type) {
	case nil:
		return nil
	case *stdlib.Registry:
		return p
	case stdlibRegistryAccessor:
		return p.registry()
	default:
		return nil
	}
}

// collectFileDiagnostics merges every diagnostic produced by the
// pipeline into one sorted list for the given file. Filtering by
// path is necessary because the package-level resolve/check
// diagnostics may cover multiple files.
func collectFileDiagnostics(
	path string,
	pr ParseResult,
	resolveDiags []*diag.Diagnostic,
	chk *check.Result,
	lr *lint.Result,
) []*diag.Diagnostic {
	norm := NormalizePath(path)
	fileID := pr.SourceFileID
	if fileID == "" {
		fileID = spanid.SourceFileIDFor(norm)
	}
	diag.StampFile(pr.Diags, norm)
	if lr != nil {
		// LintFile is source-only and therefore has no file context until this
		// per-path aggregation boundary.
		diag.StampFile(lr.Diags, norm)
	}
	out := make([]*diag.Diagnostic, 0, len(pr.Diags)+len(resolveDiags)+diagLen(chk)+diagLen(lr))
	appendFiltered := func(ds []*diag.Diagnostic) {
		for _, d := range ds {
			if d == nil {
				continue
			}
			if !diagnosticBelongsTo(d, norm, fileID) {
				continue
			}
			out = append(out, d)
		}
	}
	// Parse diagnostics are always about the file that was parsed —
	// no position-based filtering needed.
	out = append(out, pr.Diags...)
	appendFiltered(resolveDiags)
	if chk != nil {
		appendFiltered(chk.Diags)
	}
	if lr != nil {
		appendFiltered(lr.Diags)
	}
	return out
}

func nativeResolveDiagnosticsForFile(path string, pkg *resolve.Package) []*diag.Diagnostic {
	if pkg == nil {
		return nil
	}
	norm := NormalizePath(path)
	fileID := spanid.SourceFileIDFor(norm)
	for _, pf := range pkg.Files {
		if pf != nil && NormalizePath(pf.Path) == norm && pf.SourceFileID != "" {
			fileID = pf.SourceFileID
			break
		}
	}
	ds, err := resolve.NativeDiagnostics(pkg)
	if err != nil {
		return nil
	}
	out := make([]*diag.Diagnostic, 0, len(ds))
	for _, d := range ds {
		if d == nil {
			continue
		}
		if !diagnosticBelongsTo(d, norm, fileID) {
			continue
		}
		out = append(out, d)
	}
	return out
}

func diagLen(v any) int {
	switch x := v.(type) {
	case *check.Result:
		if x != nil {
			return len(x.Diags)
		}
	case *lint.Result:
		if x != nil {
			return len(x.Diags)
		}
	}
	return 0
}

// diagnosticBelongsTo routes multi-file diagnostics by explicit path first and
// then by SourceFileID carried on the primary span. Diagnostics without either
// identity are retained as a compatibility fallback so older single-file
// producers do not disappear from editor output.
func diagnosticBelongsTo(d *diag.Diagnostic, normalizedPath string, fileID spanid.SourceFileID) bool {
	if d == nil {
		return false
	}
	if d.File != "" {
		return NormalizePath(d.File) == normalizedPath
	}
	if span, ok := d.PrimarySpan(); ok && span.SourceFileID != "" {
		return span.SourceFileID == fileID
	}
	return true
}

// ---- Workspace helpers ----

func workspaceMembersForRoot(ctx *query.Ctx, inp Inputs, rootDir string) []WorkspacePackage {
	rootDir = NormalizePath(rootDir)
	if inp.WorkspacePackages.HasFetch(ctx, rootDir) {
		members := inp.WorkspacePackages.Fetch(ctx, rootDir)
		return normalizeWorkspacePackages(rootDir, members)
	}
	dirs := inp.WorkspaceMembers.Fetch(ctx, struct{}{})
	members := make([]WorkspacePackage, 0, len(dirs))
	for _, dir := range dirs {
		dir = NormalizePath(dir)
		members = append(members, WorkspacePackage{
			Dir:     dir,
			DotPath: dotPathFromDir(rootDir, dir),
			Name:    packageNameFromDir(dir),
		})
	}
	return normalizeWorkspacePackages(rootDir, members)
}

func normalizeWorkspacePackages(rootDir string, members []WorkspacePackage) []WorkspacePackage {
	if len(members) == 0 {
		return nil
	}
	out := make([]WorkspacePackage, 0, len(members))
	seen := map[string]bool{}
	for _, m := range members {
		m.Dir = NormalizePath(m.Dir)
		if m.DotPath == "" && m.Dir != rootDir {
			m.DotPath = dotPathFromDir(rootDir, m.Dir)
		}
		if m.Name == "" {
			if m.DotPath != "" {
				m.Name = packageNameFromDir(m.DotPath)
			} else {
				m.Name = packageNameFromDir(m.Dir)
			}
		}
		key := m.DotPath + "\x00" + m.Dir
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DotPath != out[j].DotPath {
			return out[i].DotPath < out[j].DotPath
		}
		return out[i].Dir < out[j].Dir
	})
	return out
}

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
// package's FrontendRun / *ast.File pointers are reused (they are read-only),
// but the PackageFile slice and the Package struct itself are fresh so
// ResolveAll's in-place mutation (setting PkgScope, RefsByID, etc.) does not
// corrupt the BuildPackage cache.
func copyPackageForWorkspace(src *resolve.Package) *resolve.Package {
	if src == nil {
		return nil
	}
	pkg := &resolve.Package{
		Dir:               src.Dir,
		Name:              src.Name,
		RuntimeCapability: src.RuntimeCapability,
		Files:             make([]*resolve.PackageFile, len(src.Files)),
	}
	for i, pf := range src.Files {
		if pf == nil {
			continue
		}
		pkg.Files[i] = &resolve.PackageFile{
			Path:                   pf.Path,
			Source:                 pf.Source,
			SourceFileID:           pf.SourceFileID,
			OriginalSource:         pf.OriginalSource,
			SourceTransformApplied: pf.SourceTransformApplied,
			SourceTransformChanged: pf.SourceTransformChanged,
			TransformMap:           pf.TransformMap,
			CanonicalSource:        pf.CanonicalSource,
			CanonicalMap:           pf.CanonicalMap,
			File:                   pf.File,
			Run:                    pf.Run,
			ParseDiags:             pf.ParseDiags,
			ParseProvenance:        pf.ParseProvenance,
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
