package resolve

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/token"
)

// IsWorkspaceRoot reports whether dir is structured as a workspace —
// it contains at least one immediate subdirectory (other than skipDir)
// whose package source discovery yields at least one .osty source. Pass
// "" for skipDir to scan every subdirectory. Tools that support both
// package and workspace layouts use this as a mode switch.
func IsWorkspaceRoot(dir, skipDir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	rootOwnedSubdirs := packageSourceSubdirs(dir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if rootOwnedSubdirs[e.Name()] {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		if sub == skipDir {
			continue
		}
		if packageHasSource(sub) {
			return true
		}
	}
	return false
}

// WorkspacePackagePaths enumerates the dotted import paths that should
// be seeded into a Workspace rooted at dir. The output contains:
//
//   - "" (the root package) when dir itself has package sources, including
//     manifest-declared [bin].path / [lib].path entries; and
//   - each immediate subdirectory name whose package source discovery yields
//     at least one .osty source.
//
// The result is a read-only filesystem scan — no Workspace state is
// touched. Callers typically iterate the returned slice and invoke
// Workspace.LoadPackage on each entry.
func WorkspacePackagePaths(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var paths []string
	if packageHasSource(root) {
		paths = append(paths, "")
	}
	rootOwnedSubdirs := packageSourceSubdirs(root)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if rootOwnedSubdirs[e.Name()] {
			continue
		}
		if packageHasSource(filepath.Join(root, e.Name())) {
			paths = append(paths, e.Name())
		}
	}
	return paths
}

func packageHasSource(dir string) bool {
	paths, err := PackageSourcePaths(dir, false)
	return err == nil && len(paths) > 0
}

func packageSourceSubdirs(root string) map[string]bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	paths, err := PackageSourcePaths(absRoot, false)
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, path := range paths {
		rel, err := filepath.Rel(absRoot, path)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
		if len(parts) > 1 && parts[0] != "." && parts[0] != "" {
			out[parts[0]] = true
		}
	}
	return out
}

// StdPrefix is the dotted-path prefix that identifies a stdlib import.
// Every path that begins with this prefix is eligible for provider
// routing and opaque-stub fallback.
const StdPrefix = "std."

// StdlibProvider supplies resolver-ready Package objects for `std.*`
// imports. A Workspace with a non-nil Stdlib consults the provider
// before falling back to the opaque stub.
//
// The returned Package must have its PkgScope populated with the
// module's exported symbols (marked `Pub: true`). A nil return means
// the provider does not recognize the dotted path, and the workspace
// should apply its default handling.
type StdlibProvider interface {
	LookupPackage(dotPath string) *Package
}

// Workspace owns every package loaded from a single on-disk project
// tree. Packages are keyed by their dotted import path (e.g.
// `auth.login`), which maps directly onto a subdirectory of Root.
//
// The zero value is not usable; construct one via NewWorkspace.
type Workspace struct {
	// Root is the absolute filesystem path that anchors dotted import
	// paths. `use auth.login` resolves to `<Root>/auth/login`.
	Root string

	// Packages maps the dotted import path to the loaded Package. A
	// package is present here iff it has been parsed (possibly with
	// errors). ResolveAll fills in each Package's resolver state.
	Packages map[string]*Package

	// Stdlib, when non-nil, supplies Package objects for `std.*` imports.
	// It is consulted before the opaque-stub fallback. Leaving it nil
	// preserves the legacy behavior where every `std.*` import becomes
	// an opaque stub.
	Stdlib StdlibProvider

	// Deps, when non-nil, resolves external `use` targets (URL-style
	// imports and bare aliases) into on-disk directories. The package
	// manager injects this after vendoring — see
	// internal/pkgmgr.NewDepProvider. A nil value preserves the
	// legacy behavior where any non-std, non-workspace import is an
	// error.
	Deps DepProvider

	// SourceTransform rewrites source bytes before parsing. The CLI uses
	// this to adapt AI-authored foreign syntax before any parser or
	// resolver work begins.
	SourceTransform SourceTransform

	// SourceTransformer is the structured source rewrite hook. When set, it
	// takes precedence over SourceTransform and may provide exact source-map
	// metadata or deliberately opt out of original-source remapping.
	SourceTransformer SourceTransformer

	// stdlibStub is set to true when the workspace should tolerate
	// `std.*` imports even though no stdlib sources are present. Useful
	// in tests and before the stdlib is bundled with the compiler.
	stdlibStub bool

	// loading tracks packages currently mid-load for cycle detection.
	// A package appears here only during the DFS descent initiated by
	// LoadPackage.
	loading map[string]bool

	// cfgEnv carries the `#[cfg(...)]` evaluation environment
	// (os/target/arch/feature). When nil, ResolveAll populates it
	// from `DefaultCfgEnv()` — i.e. the Go host values. The build
	// driver sets this explicitly when cross-compiling or toggling
	// features.
	cfgEnv *CfgEnv
}

func (w *Workspace) loadOptions() LoadOptions {
	if w == nil {
		return LoadOptions{}
	}
	return LoadOptions{
		Transform:   w.SourceTransform,
		Transformer: w.SourceTransformer,
	}
}

// SetCfgEnv installs a CfgEnv that subsequent ResolveAll calls use
// for `#[cfg(...)]` evaluation. Passing nil restores the default
// (host-derived) environment.
func (w *Workspace) SetCfgEnv(env *CfgEnv) {
	w.cfgEnv = env
}

// NewWorkspace creates a workspace anchored at the given filesystem
// path. The path is canonicalized to an absolute path so that the
// dotted-path → directory mapping is stable across calls regardless of
// the caller's working directory.
func NewWorkspace(root string) (*Workspace, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &Workspace{
		Root:       abs,
		Packages:   map[string]*Package{},
		stdlibStub: true,
		loading:    map[string]bool{},
	}, nil
}

// LoadPackage loads the package identified by dotPath into the workspace.
// If the package has already been loaded it is returned from cache. Loading is
// native by default: PackageFile.Run is populated while the public AST
// compatibility surface stays nil until a legacy consumer explicitly asks for
// it.
func (w *Workspace) LoadPackage(dotPath string) (*Package, error) {
	return w.LoadPackageNative(dotPath)
}

// lastSegment picks the final `/`- or `.`-separated chunk of key.
// Used to compute a human-readable Package.Name for external deps.
func lastSegment(key string) string {
	if i := strings.LastIndexAny(key, "/."); i >= 0 {
		return key[i+1:]
	}
	return key
}

// dirFor converts a dotted import path to an absolute filesystem path
// under w.Root. Path segments are joined with the OS separator.
func (w *Workspace) dirFor(dotPath string) string {
	if dotPath == "" {
		return w.Root
	}
	segs := strings.Split(dotPath, ".")
	return filepath.Join(append([]string{w.Root}, segs...)...)
}

// MaterializePublicCompatibility forces public-AST compatibility output for
// every loaded package. Keep calls to this method close to legacy boundaries
// such as Go AST doc/refactor/lowering paths.
func (w *Workspace) MaterializePublicCompatibility() {
	if w == nil {
		return
	}
	for _, pkg := range w.Packages {
		pkg.MaterializePublicCompatibility()
	}
}

// MaterializeCanonicalSources populates canonical sources for every loaded
// package whose public-AST compatibility surface has been materialized.
func (w *Workspace) MaterializeCanonicalSources() {
	if w == nil {
		return
	}
	for _, pkg := range w.Packages {
		pkg.MaterializeCanonicalSources()
	}
}

// ResolveAll runs the selfhost resolver over every loaded package,
// sharing one prelude across the workspace, then stitches cycle
// diagnostics back onto the owning package result. The Go side no
// longer performs a workspace-wide two-pass resolve walk.
func (w *Workspace) ResolveAll() map[string]*PackageResult {
	cycleDiags := w.detectCycles()
	prelude := NewPrelude()
	results := map[string]*PackageResult{}
	paths := make([]string, 0, len(w.Packages))
	for path := range w.Packages {
		paths = append(paths, path)
	}
	paths = w.resolveOrder(paths)
	for _, path := range paths {
		pkg := w.Packages[path]
		if pkg == nil {
			continue
		}
		if pkg.isStub {
			results[path] = &PackageResult{PackageScope: nil}
			continue
		}
		pkg.workspace = w
		if w.isPreResolvedStdlib(path, pkg) {
			results[path] = &PackageResult{PackageScope: pkg.PkgScope}
			continue
		}
		results[path] = ResolvePackage(pkg, prelude)
	}
	// Attach cycle diagnostics to the package they were reported from.
	// Each entry is (importerPath, diagnostic).
	for _, cd := range cycleDiags {
		if r, ok := results[cd.importer]; ok {
			r.Diags = append(r.Diags, cd.diag)
		}
	}
	return results
}

func (w *Workspace) resolveOrder(paths []string) []string {
	sort.Strings(paths)
	inSet := make(map[string]bool, len(paths))
	for _, path := range paths {
		inSet[path] = true
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	out := make([]string, 0, len(paths))
	var visit func(string)
	visit = func(path string) {
		if visited[path] {
			return
		}
		if visiting[path] {
			return
		}
		visiting[path] = true
		for _, dep := range w.packageUseTargets(path) {
			if inSet[dep] {
				visit(dep)
			}
		}
		visiting[path] = false
		visited[path] = true
		out = append(out, path)
	}
	for _, path := range paths {
		visit(path)
	}
	return out
}

func (w *Workspace) packageUseTargets(path string) []string {
	pkg := w.Packages[path]
	if pkg == nil || pkg.isStub || pkg.isCycleMarker {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, edge := range packageUseGraphEdges(pkg) {
		if edge.target == "" || seen[edge.target] {
			continue
		}
		seen[edge.target] = true
		out = append(out, edge.target)
	}
	sort.Strings(out)
	return out
}

// isPreResolvedStdlib reports whether pkg came from an attached
// StdlibProvider. Bundled stdlib modules are resolved when the registry
// is loaded, so a workspace pass should reuse their scopes instead of
// declaring the same top-level names a second time.
func (w *Workspace) isPreResolvedStdlib(path string, pkg *Package) bool {
	return w.Stdlib != nil &&
		strings.HasPrefix(path, StdPrefix) &&
		pkg != nil &&
		pkg.PkgScope != nil
}

// importCycleDiag pairs a cycle diagnostic with the package that
// actually contained the offending `use`.
type importCycleDiag struct {
	importer string
	diag     *diag.Diagnostic
}

type importGraphEdge struct {
	target string
	pos    token.Pos
	pub    bool
}

// detectCycles walks the import graph induced by every loaded package's
// `use` declarations and returns one diagnostic per edge that completes
// a cycle. Stub/cycle-marker packages contribute no edges. The DFS
// itself lives in toolchain/resolve.osty::selfDetectImportCycles —
// this Go side prepares the graph, dispatches to the Osty algorithm,
// and reconstructs rich diag.Diagnostic objects from the returned
// offset-based CycleDiag records.
func (w *Workspace) detectCycles() []importCycleDiag {
	// Build adjacency with positions so diagnostics can point at the
	// exact `use` statement that closes the cycle. The selfhost
	// algorithm round-trips pos as an offset only; keeping the
	// token.Pos here lets us restore Line/Column when rendering
	// the diagnostic.
	adj := map[string][]importGraphEdge{}
	for path, pkg := range w.Packages {
		if pkg.isStub || pkg.isCycleMarker {
			continue
		}
		for _, edge := range packageUseGraphEdges(pkg) {
			if edge.target == "" {
				continue
			}
			adj[path] = append(adj[path], importGraphEdge{target: edge.target, pos: edge.pos, pub: edge.pub})
		}
	}
	pubReexportClosers := reexportCycleClosers(adj)
	// Stable iteration for deterministic diagnostic order — mirrors
	// the previous in-process DFS, which sorted adj keys before DFS.
	keys := make([]string, 0, len(adj))
	for k := range adj {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	input := selfhost.WorkspaceUses{Packages: make([]selfhost.PackageUses, 0, len(keys))}
	for _, k := range keys {
		uses := make([]selfhost.UseEdge, 0, len(adj[k]))
		for _, e := range adj[k] {
			uses = append(uses, selfhost.UseEdge{
				Target: e.target,
				Pos:    e.pos.Offset,
			})
		}
		input.Packages = append(input.Packages, selfhost.PackageUses{
			Path: k,
			Uses: uses,
		})
	}
	cycles := selfhost.DetectImportCycles(input)

	out := make([]importCycleDiag, 0, len(cycles))
	for _, cd := range cycles {
		// Re-find the original token.Pos via (target, pos offset) —
		// multiple edges between the same nodes may exist (one
		// package importing another twice from different files) so
		// match on offset, not just target.
		var pos token.Pos
		for _, e := range adj[cd.Importer] {
			if e.target == cd.Target && e.pos.Offset == cd.Pos {
				pos = e.pos
				break
			}
		}
		code := diag.CodeCyclicImport
		message := cd.Message
		primary := "completes an import cycle"
		note := "v0.2 §5.4: package imports must form a DAG"
		hint := "break the cycle by extracting the shared names into a third package that both sides import"
		if pubReexportClosers[cycleEdgeKey{importer: cd.Importer, target: cd.Target, pos: cd.Pos}] {
			code = diag.CodeReexportCycle
			message = fmt.Sprintf("re-export cycle: `%s` pub-uses `%s`", cd.Importer, cd.Target)
			primary = "completes a re-export cycle"
			note = "v0.5 §5: `pub use` re-export chains must be acyclic"
			hint = "break the cycle by importing the original package from one side instead of re-exporting through it"
		}
		out = append(out, importCycleDiag{
			importer: cd.Importer,
			diag: diag.New(diag.Error, message).
				Code(code).
				PrimaryPos(pos, primary).
				Note(note).
				Hint(hint).
				Build(),
		})
	}
	return out
}

type packageUseGraphEdge struct {
	target string
	pos    token.Pos
	pub    bool
}

func packageUseGraphEdges(pkg *Package) []packageUseGraphEdge {
	if pkg == nil {
		return nil
	}
	var out []packageUseGraphEdge
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		if pf.Run != nil {
			for _, use := range selfhost.PackageUsesFromRun(pf.Run) {
				if use.IsGo {
					continue
				}
				target := use.Path
				if use.IsScoped {
					target = use.ScopedBase
				}
				if target == "" {
					continue
				}
				out = append(out, packageUseGraphEdge{
					target: target,
					pos:    sourcePosAt(pf.Source, use.Start),
					pub:    use.IsPub,
				})
			}
			continue
		}
		if pf.File == nil {
			continue
		}
		for _, u := range pf.File.Uses {
			if u == nil || u.IsFFI() {
				continue
			}
			target := useDependencyKey(u)
			if target == "" {
				continue
			}
			out = append(out, packageUseGraphEdge{target: target, pos: u.PosV, pub: u.IsPub})
		}
	}
	return out
}

func sourcePosAt(src []byte, offset int) token.Pos {
	if offset < 0 {
		offset = 0
	}
	if offset > len(src) {
		offset = len(src)
	}
	pos := token.Pos{Line: 1, Column: 1, Offset: offset}
	for i, b := range src {
		if i >= offset {
			break
		}
		if b == '\n' {
			pos.Line++
			pos.Column = 1
			continue
		}
		pos.Column++
	}
	return pos
}

type cycleEdgeKey struct {
	importer string
	target   string
	pos      int
}

func reexportCycleClosers(adj map[string][]importGraphEdge) map[cycleEdgeKey]bool {
	out := map[cycleEdgeKey]bool{}
	for importer, edges := range adj {
		for _, e := range edges {
			if !e.pub {
				continue
			}
			if pubPathExists(adj, e.target, importer, map[string]bool{}) {
				out[cycleEdgeKey{importer: importer, target: e.target, pos: e.pos.Offset}] = true
			}
		}
	}
	return out
}

func pubPathExists(adj map[string][]importGraphEdge, from, target string, seen map[string]bool) bool {
	if from == target {
		return true
	}
	if seen[from] {
		return false
	}
	seen[from] = true
	for _, e := range adj[from] {
		if !e.pub {
			continue
		}
		if pubPathExists(adj, e.target, target, seen) {
			return true
		}
	}
	return false
}

// cycleMarker returns a sentinel Package used when LoadPackage detects
// a cycle. Callers check Package.isCycleMarker to decide whether to
// emit CodeCyclicImport.
func cycleMarker(dotPath string) *Package {
	return &Package{
		Name:          lastDotSeg(dotPath),
		isCycleMarker: true,
	}
}

// stdlibStub returns an opaque Package representing a `std.*` import
// that has no on-disk sources yet. The resolver treats it as "member
// access is allowed but yields no further type info."
func stdlibStub(dotPath string) *Package {
	return &Package{
		Name:   lastDotSeg(dotPath),
		isStub: true,
	}
}

func lastDotSeg(dotPath string) string {
	if i := strings.LastIndex(dotPath, "."); i >= 0 {
		return dotPath[i+1:]
	}
	return dotPath
}

// ResolveUseTarget looks up a dotted import path in the workspace. It
// returns:
//
//   - (pkg, nil) when the package is loaded and usable;
//   - (stub, nil) for `std.*` stubs when stdlibStub is enabled;
//   - (nil, diag) when the lookup failed, carrying a diagnostic the
//     caller should attach to the use-site AST node.
//
// `usePos` is used to position any generated diagnostic.
func (w *Workspace) ResolveUseTarget(dotPath string, usePos token.Pos) (*Package, *diag.Diagnostic) {
	if pkg, ok := w.Packages[dotPath]; ok {
		if pkg.isCycleMarker {
			return pkg, diag.New(diag.Error,
				fmt.Sprintf("cyclic import: package `%s` is already being loaded", dotPath)).
				Code(diag.CodeCyclicImport).
				PrimaryPos(usePos, "completes an import cycle").
				Note("v0.2 §5.4: imports must form a DAG — A importing B and B importing A is rejected").
				Build()
		}
		return pkg, nil
	}
	// Not cached: attempt to load on demand.
	pkg, err := w.LoadPackage(dotPath)
	if err == nil && pkg != nil && !pkg.isCycleMarker {
		return pkg, nil
	}
	// Build a friendly "unknown package" diagnostic.
	d := diag.New(diag.Error,
		fmt.Sprintf("package `%s` not found in this workspace", dotPath)).
		Code(diag.CodeUnknownPackage).
		PrimaryPos(usePos, "unknown package").
		Note("v0.4 §5.2: imports resolve to subdirectories of the project root")
	if err != nil {
		d.Note(err.Error())
	}
	if strings.HasPrefix(dotPath, StdPrefix) {
		d.Hint("the standard library is not bundled with this build; `std.*` is accepted as an opaque package")
	}
	return nil, d.Build()
}
