package resolve

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/token"
)

// IsWorkspaceRoot reports whether dir is structured as a workspace —
// it contains at least one immediate subdirectory (other than
// skipDir) whose files include at least one .osty source. Pass "" for
// skipDir to scan every subdirectory. Tools that support both
// package and workspace layouts use this as a mode switch.
func IsWorkspaceRoot(dir, skipDir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(dir, e.Name())
		if sub == skipDir {
			continue
		}
		subs, err := os.ReadDir(sub)
		if err != nil {
			continue
		}
		for _, s := range subs {
			if !s.IsDir() && filepath.Ext(s.Name()) == ".osty" {
				return true
			}
		}
	}
	return false
}

// WorkspacePackagePaths enumerates the dotted import paths that should
// be seeded into a Workspace rooted at dir. The output contains:
//
//   - "" (the root package) when dir itself holds at least one .osty
//     file; and
//   - each immediate subdirectory name whose files include at least
//     one .osty source.
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
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".osty" {
			paths = append(paths, "")
			break
		}
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		subs, _ := os.ReadDir(filepath.Join(root, e.Name()))
		for _, s := range subs {
			if !s.IsDir() && filepath.Ext(s.Name()) == ".osty" {
				paths = append(paths, e.Name())
				break
			}
		}
	}
	return paths
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

	// stdlibStub is set to true when the workspace should tolerate
	// `std.*` imports even though no stdlib sources are present. Useful
	// in tests and before the stdlib is bundled with the compiler.
	stdlibStub bool

	// loading tracks packages currently mid-load for cycle detection.
	// A package appears here only during the DFS descent initiated by
	// LoadPackage.
	loading map[string]bool
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

// LoadPackage loads the package identified by dotPath into the
// workspace. If the package has already been loaded it is returned from
// cache. The walk recursively loads any packages referenced by `use`
// declarations so a single top-level call assembles the whole import
// closure.
//
// Cycles are detected via the `loading` set and produce a
// CodeCyclicImport diagnostic attached to the offending `use` site.
// The cyclic package is still returned (so the caller can keep resolving
// other parts of the tree), but its diagnostics list will contain the
// cycle notice.
func (w *Workspace) LoadPackage(dotPath string) (*Package, error) {
	if pkg, ok := w.Packages[dotPath]; ok {
		return pkg, nil
	}
	if w.loading[dotPath] {
		// Caller is the cycle-detection entry; return a marker package
		// with a cycle-note diagnostic so downstream reporting lines up.
		return cycleMarker(dotPath), nil
	}

	// URL-style keys never live under w.Root — they route straight
	// through the DepProvider to the vendor directory. No `stat` on
	// a bogus path, no std-prefix branch.
	if isURLStyle(dotPath) {
		return w.loadExternalDep(dotPath)
	}

	dir := w.dirFor(dotPath)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) && strings.HasPrefix(dotPath, StdPrefix) {
			// Prefer a resolver-ready Package from the Stdlib provider
			// when one is attached. The provider owns the symbol table
			// for the module, so member access (`io.print`) validates
			// against real `Pub` symbols rather than falling through to
			// the opaque stub.
			if w.Stdlib != nil {
				if pkg := w.Stdlib.LookupPackage(dotPath); pkg != nil {
					w.Packages[dotPath] = pkg
					return pkg, nil
				}
			}
			if w.stdlibStub {
				// Opaque stub fallback: the package exists for resolver
				// purposes, but there are no sources to parse. Member
				// access is permissive so `std.fs.readFile` compiles
				// cleanly until the stdlib provider is wired up.
				stub := stdlibStub(dotPath)
				w.Packages[dotPath] = stub
				return stub, nil
			}
		}
		// Bare-alias fallback: single-segment name that isn't a sibling
		// package. Consult the DepProvider in case the package manager
		// has vendored a matching dep.
		if os.IsNotExist(err) && w.Deps != nil && !strings.ContainsAny(dotPath, ".") {
			if extDir, ok := w.Deps.LookupDep(dotPath); ok {
				return w.loadFromExternalDir(dotPath, extDir)
			}
		}
		return nil, fmt.Errorf("package %q: %w", dotPath, err)
	}

	w.loading[dotPath] = true
	defer delete(w.loading, dotPath)

	pkg, err := LoadPackage(dir)
	if err != nil {
		return nil, err
	}
	pkg.Name = lastDotSeg(dotPath)
	w.Packages[dotPath] = pkg

	// Recurse into every `use` target so the closure of dependencies is
	// loaded before we run resolution on this package.
	for _, f := range pkg.Files {
		for _, u := range f.File.Uses {
			if u.IsGoFFI {
				continue
			}
			target := UseKey(u)
			if target == "" {
				continue
			}
			if w.loading[target] {
				// Defer the cycle diagnostic to resolver pass — it has
				// access to the use-site position. We just stop
				// recursing here.
				continue
			}
			if _, alreadyLoaded := w.Packages[target]; alreadyLoaded {
				continue
			}
			// Best-effort load; missing-package diagnostics are
			// reported by the resolver, not here, so we swallow the
			// error and let resolution surface it with source context.
			_, _ = w.LoadPackage(target)
		}
	}
	return pkg, nil
}

// loadExternalDep routes a URL-style use target through the
// DepProvider. Returns a descriptive error when no provider is
// attached or the provider doesn't recognize the path — the
// resolver surfaces this as an unknown-package diagnostic with
// source context.
func (w *Workspace) loadExternalDep(rawPath string) (*Package, error) {
	if w.Deps == nil {
		return nil, fmt.Errorf("package %q: no dependency provider configured (did you forget `osty add`?)", rawPath)
	}
	dir, ok := w.Deps.LookupDep(rawPath)
	if !ok {
		return nil, fmt.Errorf("package %q: not found among declared dependencies", rawPath)
	}
	return w.loadFromExternalDir(rawPath, dir)
}

// loadFromExternalDir reads the package at dir and registers it
// under key. Used by both URL-style imports and bare-alias
// fallbacks once the DepProvider has located the vendored directory.
// Recursion into transitive `use` declarations still happens — the
// DepProvider is expected to know about them too.
func (w *Workspace) loadFromExternalDir(key, dir string) (*Package, error) {
	w.loading[key] = true
	defer delete(w.loading, key)

	pkg, err := LoadPackage(dir)
	if err != nil {
		return nil, err
	}
	// Name is the final segment: the last `/` chunk for URL paths,
	// the alias itself for bare single-segment aliases.
	pkg.Name = lastSegment(key)
	w.Packages[key] = pkg

	for _, f := range pkg.Files {
		for _, u := range f.File.Uses {
			if u.IsGoFFI {
				continue
			}
			target := UseKey(u)
			if target == "" || w.loading[target] {
				continue
			}
			if _, alreadyLoaded := w.Packages[target]; alreadyLoaded {
				continue
			}
			_, _ = w.LoadPackage(target)
		}
	}
	return pkg, nil
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

// ResolveAll runs ResolvePackage on every loaded package, sharing one
// prelude across the workspace. Before walking bodies it does a cycle
// check over the import graph; every edge that would close a cycle
// emits CodeCyclicImport against the offending `use` site.
//
// The function does NOT re-walk packages across workspaces; call it
// once after all LoadPackage calls are done.
func (w *Workspace) ResolveAll() map[string]*PackageResult {
	cycleDiags := w.detectCycles()

	prelude := NewPrelude()

	// Per-package resolvers are needed so diagnostics stay
	// segregated, but every one shares the same prelude. Build a
	// resolver for each non-stub package up front.
	resolvers := map[string]*resolver{}
	results := map[string]*PackageResult{}
	for path, pkg := range w.Packages {
		if pkg.isStub {
			results[path] = &PackageResult{PackageScope: nil}
			continue
		}
		pkg.workspace = w
		if w.isPreResolvedStdlib(path, pkg) {
			results[path] = &PackageResult{PackageScope: pkg.PkgScope}
			continue
		}
		resolvers[path] = newPkgResolver(pkg, prelude)
	}
	// Pass A (across workspace): populate every package's PkgScope
	// with its top-level declarations. Cross-package member lookups in
	// Pass B can now succeed regardless of iteration order.
	for path, pkg := range w.Packages {
		if pkg.isStub {
			continue
		}
		if r := resolvers[path]; r != nil {
			r.declarePass(pkg)
		}
	}
	// Pass B (across workspace): walk bodies, expression references,
	// type references. Each resolver sees every other package through
	// their already-populated PkgScope.
	for path, pkg := range w.Packages {
		if pkg.isStub {
			continue
		}
		r := resolvers[path]
		if r == nil {
			continue
		}
		r.bodyPass(pkg)
		results[path] = &PackageResult{
			PackageScope: pkg.PkgScope,
			Diags:        r.diags,
		}
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

// detectCycles walks the import graph induced by every loaded package's
// `use` declarations and returns one diagnostic per edge that completes
// a cycle. Stub/cycle-marker packages contribute no edges.
func (w *Workspace) detectCycles() []importCycleDiag {
	// Build adjacency with positions so diagnostics can point at the
	// exact `use` statement that closes the cycle.
	type edge struct {
		target string
		pos    token.Pos
	}
	adj := map[string][]edge{}
	for path, pkg := range w.Packages {
		if pkg.isStub || pkg.isCycleMarker {
			continue
		}
		for _, f := range pkg.Files {
			for _, u := range f.File.Uses {
				if u.IsGoFFI {
					continue
				}
				target := UseKey(u)
				if target == "" {
					continue
				}
				adj[path] = append(adj[path], edge{target: target, pos: u.PosV})
			}
		}
	}
	var out []importCycleDiag
	state := map[string]int{} // 0 unvisited, 1 on stack, 2 done
	var dfs func(n string)
	dfs = func(n string) {
		if state[n] == 2 {
			return
		}
		state[n] = 1
		for _, e := range adj[n] {
			if state[e.target] == 1 {
				out = append(out, importCycleDiag{
					importer: n,
					diag: diag.New(diag.Error,
						fmt.Sprintf("cyclic import: `%s` imports `%s` which (transitively) imports `%s`",
							n, e.target, n)).
						Code(diag.CodeCyclicImport).
						PrimaryPos(e.pos, "completes an import cycle").
						Note("v0.2 §5.4: package imports must form a DAG").
						Hint("break the cycle by extracting the shared names into a third package that both sides import").
						Build(),
				})
				continue
			}
			if state[e.target] == 0 {
				dfs(e.target)
			}
		}
		state[n] = 2
	}
	// Stable iteration for deterministic diagnostic order.
	keys := make([]string, 0, len(adj))
	for k := range adj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		dfs(k)
	}
	return out
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
		Note("v0.3 §5.2: imports resolve to subdirectories of the project root")
	if err != nil {
		d.Note(err.Error())
	}
	if strings.HasPrefix(dotPath, StdPrefix) {
		d.Hint("the standard library is not bundled with this build; `std.*` is accepted as an opaque package")
	}
	return nil, d.Build()
}
