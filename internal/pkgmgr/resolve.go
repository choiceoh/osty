package pkgmgr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/lockfile"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/pkgmgr/semver"
)

// errUnsupported is the sentinel symlink fallback uses to recognize
// "this filesystem doesn't support that op". On Go 1.21+ os.ErrInvalid
// or syscall.ENOTSUP show up depending on the platform; we keep a
// dedicated value so tests can inject it deterministically.
var errUnsupported = errors.New("operation not supported")

// Graph is the resolved dependency graph for a project. Nodes maps
// the local dependency name (as written in osty.toml) to a
// ResolvedNode; Order is a stable topological ordering (deepest
// dependencies first) suitable for sequential vendoring and
// workspace registration.
type Graph struct {
	Root  *manifest.Manifest
	Nodes map[string]*ResolvedNode
	Order []string // topological, leaves first
}

// ResolvedNode is one entry in Graph.Nodes. The Source points at the
// declared origin; Fetched is populated once Fetch succeeds and
// captures everything the lockfile needs to record.
type ResolvedNode struct {
	Name    string
	Source  Source
	Fetched *FetchedPackage
	// Deps is the set of *local* names this node depends on
	// (transitive edges). Populated during graph construction.
	Deps []string
}

// Resolve walks the root manifest's dependency closure and returns
// the resolved Graph. When a lockfile is present at env.ProjectRoot,
// its pins are used to keep versions stable; unknown or newly-added
// dependencies are resolved fresh.
//
// For the first iteration we implement the simplest correct
// algorithm: DFS, first match wins within a name. The lockfile is
// honored when the declared requirement still allows its pin. This
// is good enough for path + git + a single-repo dev workflow; a
// version-picking SAT solver is future work.
func Resolve(ctx context.Context, root *manifest.Manifest, env *Env) (*Graph, error) {
	if root == nil {
		return nil, fmt.Errorf("nil root manifest")
	}
	existing, err := lockfile.Read(env.ProjectRoot)
	if err != nil {
		return nil, err
	}
	r := &resolver{
		env:   env,
		lock:  existing,
		graph: &Graph{Root: root, Nodes: map[string]*ResolvedNode{}},
	}
	rootDir := manifestBaseDir(root, env.ProjectRoot)
	var pending []resolveRequest
	for _, d := range root.Dependencies {
		pending = append(pending, resolveRequest{dep: d, baseDir: rootDir})
	}
	for _, d := range root.DevDependencies {
		pending = append(pending, resolveRequest{dep: d, baseDir: rootDir})
	}
	solved, err := r.solve(ctx, &resolveState{
		graph:   r.graph,
		pending: pending,
	})
	if err != nil {
		return nil, err
	}
	r.graph = solved.graph
	r.graph.Order = r.topoOrder()
	return r.graph, nil
}

type resolver struct {
	env   *Env
	lock  *lockfile.Lock
	graph *Graph
}

type resolveRequest struct {
	dep       manifest.Dependency
	baseDir   string
	parent    string
	ancestors map[string]bool
}

type resolveState struct {
	graph   *Graph
	pending []resolveRequest
}

type resolveCandidate struct {
	source Source
	fetch  func(context.Context) (*FetchedPackage, error)
}

func (r *resolver) solve(ctx context.Context, st *resolveState) (*resolveState, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if len(st.pending) == 0 {
		return st, nil
	}
	req := st.pending[0]
	rest := append([]resolveRequest(nil), st.pending[1:]...)
	if req.ancestors[req.dep.Name] {
		return nil, fmt.Errorf("cyclic dependency through %q", req.dep.Name)
	}
	src, err := newSourceFromDir(req.dep, req.baseDir)
	if err != nil {
		return nil, err
	}
	if existing, ok := st.graph.Nodes[req.dep.Name]; ok {
		if err := r.ensureCompatible(existing, src, req.dep); err != nil {
			return nil, err
		}
		next := st.cloneWithPending(rest)
		addDepEdge(next.graph, req.parent, existing.Name)
		return r.solve(ctx, next)
	}

	candidates, err := r.resolveCandidates(ctx, src, req.dep)
	if err != nil {
		return nil, err
	}
	var firstErr error
	for _, candidate := range candidates {
		fetched, err := candidate.fetch(ctx)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("resolve %s: %w", req.dep.Name, err)
			}
			continue
		}
		next := st.cloneWithPending(nil)
		node := &ResolvedNode{
			Name:    req.dep.Name,
			Source:  candidate.source,
			Fetched: fetched,
		}
		next.graph.Nodes[req.dep.Name] = node
		addDepEdge(next.graph, req.parent, node.Name)
		children := childRequests(req, node.Name, fetched)
		next.pending = append(children, rest...)
		solved, err := r.solve(ctx, next)
		if err == nil {
			return solved, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, fmt.Errorf("resolve %s: no candidates", req.dep.Name)
}

func (r *resolver) resolveCandidates(ctx context.Context, src Source, d manifest.Dependency) ([]resolveCandidate, error) {
	rs, ok := src.(*registrySource)
	if !ok {
		return []resolveCandidate{{
			source: src,
			fetch: func(ctx context.Context) (*FetchedPackage, error) {
				return src.Fetch(ctx, r.env)
			},
		}}, nil
	}
	candidates, err := rs.candidateRegistryVersions(ctx, r.env)
	if err != nil {
		return nil, err
	}
	candidates = r.prioritizeLockedRegistryCandidate(rs, d, candidates)
	out := make([]resolveCandidate, 0, len(candidates))
	for _, c := range candidates {
		candidate := c
		exact := &registrySource{
			name:         rs.name,
			packageName:  rs.packageName,
			versionReq:   "=" + candidate.Version.String(),
			registryName: rs.registryName,
		}
		out = append(out, resolveCandidate{
			source: exact,
			fetch: func(ctx context.Context) (*FetchedPackage, error) {
				return exact.fetchRegistryCandidate(ctx, r.env, candidate)
			},
		})
	}
	return out, nil
}

func (r *resolver) prioritizeLockedRegistryCandidate(rs *registrySource, d manifest.Dependency, candidates []registryCandidate) []registryCandidate {
	pinned, ok := r.lockedRegistryVersion(rs, d)
	if !ok {
		return candidates
	}
	for i, c := range candidates {
		if semver.Equal(c.Version, pinned) {
			if i == 0 {
				return candidates
			}
			out := append([]registryCandidate{c}, candidates[:i]...)
			return append(out, candidates[i+1:]...)
		}
	}
	return candidates
}

func (r *resolver) lockedRegistryVersion(rs *registrySource, d manifest.Dependency) (semver.Version, bool) {
	if r.lock == nil {
		return semver.Version{}, false
	}
	req, err := semver.ParseReq(rs.versionReq)
	if err != nil {
		return semver.Version{}, false
	}
	for _, p := range r.lock.FindByName(d.Name) {
		if p.Source != "" && p.Source != rs.URI() {
			continue
		}
		pv, err := semver.ParseVersion(p.Version)
		if err != nil {
			continue
		}
		if req.Match(pv) {
			return pv, true
		}
	}
	return semver.Version{}, false
}

func childRequests(parent resolveRequest, parentName string, fetched *FetchedPackage) []resolveRequest {
	if fetched == nil || fetched.Manifest == nil {
		return nil
	}
	childBase := manifestBaseDir(fetched.Manifest, fetched.LocalDir)
	ancestors := extendAncestors(parent.ancestors, parentName)
	out := make([]resolveRequest, 0, len(fetched.Manifest.Dependencies))
	for _, sub := range fetched.Manifest.Dependencies {
		out = append(out, resolveRequest{
			dep:       sub,
			baseDir:   childBase,
			parent:    parentName,
			ancestors: ancestors,
		})
	}
	return out
}

func extendAncestors(in map[string]bool, name string) map[string]bool {
	out := make(map[string]bool, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	out[name] = true
	return out
}

func (st *resolveState) cloneWithPending(pending []resolveRequest) *resolveState {
	return &resolveState{
		graph:   cloneGraph(st.graph),
		pending: append([]resolveRequest(nil), pending...),
	}
}

func cloneGraph(g *Graph) *Graph {
	if g == nil {
		return &Graph{Nodes: map[string]*ResolvedNode{}}
	}
	out := &Graph{
		Root:  g.Root,
		Nodes: make(map[string]*ResolvedNode, len(g.Nodes)),
		Order: append([]string(nil), g.Order...),
	}
	for name, node := range g.Nodes {
		if node == nil {
			continue
		}
		copyNode := *node
		copyNode.Deps = append([]string(nil), node.Deps...)
		out.Nodes[name] = &copyNode
	}
	return out
}

func addDepEdge(g *Graph, parent, child string) {
	if g == nil || parent == "" || child == "" {
		return
	}
	node := g.Nodes[parent]
	if node == nil {
		return
	}
	for _, dep := range node.Deps {
		if dep == child {
			return
		}
	}
	node.Deps = append(node.Deps, child)
}

func manifestBaseDir(m *manifest.Manifest, fallback string) string {
	if m != nil && m.Path() != "" {
		return filepath.Dir(m.Path())
	}
	return fallback
}

func (r *resolver) ensureCompatible(existing *ResolvedNode, candidate Source, d manifest.Dependency) error {
	if existing == nil || existing.Source == nil || existing.Fetched == nil {
		return fmt.Errorf("dependency %q already exists in graph but is incomplete", d.Name)
	}
	if existing.Source.Kind() != candidate.Kind() {
		return fmt.Errorf("dependency %q resolved from %s, but another dependency requires %s",
			d.Name, existing.Source.URI(), candidate.URI())
	}
	switch ex := existing.Source.(type) {
	case *registrySource:
		cand, ok := candidate.(*registrySource)
		if !ok {
			break
		}
		if ex.registryName != cand.registryName || ex.packageName != cand.packageName {
			return fmt.Errorf("dependency %q resolved from %s package %q, but another dependency requires %s package %q",
				d.Name, ex.URI(), ex.packageName, cand.URI(), cand.packageName)
		}
		req, err := semver.ParseReq(cand.versionReq)
		if err != nil {
			return fmt.Errorf("dependency %q: invalid version req %q: %w", d.Name, cand.versionReq, err)
		}
		picked, err := semver.ParseVersion(existing.Fetched.Version)
		if err != nil {
			return fmt.Errorf("dependency %q: resolved version %q is invalid: %w", d.Name, existing.Fetched.Version, err)
		}
		if !req.Match(picked) {
			return fmt.Errorf("dependency %q already resolved to %s, which does not satisfy %q",
				d.Name, existing.Fetched.Version, cand.versionReq)
		}
		return nil
	case *pathSource:
		cand, ok := candidate.(*pathSource)
		if !ok {
			break
		}
		if ex.absPath(r.env) == cand.absPath(r.env) {
			return nil
		}
		return fmt.Errorf("dependency %q resolved from %s, but another dependency requires %s",
			d.Name, ex.absPath(r.env), cand.absPath(r.env))
	}
	if existing.Source.URI() != candidate.URI() {
		return fmt.Errorf("dependency %q resolved from %s, but another dependency requires %s",
			d.Name, existing.Source.URI(), candidate.URI())
	}
	return nil
}

// topoOrder returns node names in a stable, leaves-first order.
// Deterministic: children are visited in alphabetical order so
// repeated resolves produce identical Order slices.
func (r *resolver) topoOrder() []string {
	visited := map[string]bool{}
	var out []string
	var visit func(name string)
	visit = func(name string) {
		if visited[name] {
			return
		}
		visited[name] = true
		n := r.graph.Nodes[name]
		if n == nil {
			return
		}
		deps := append([]string(nil), n.Deps...)
		sort.Strings(deps)
		for _, d := range deps {
			visit(d)
		}
		out = append(out, name)
	}
	names := make([]string, 0, len(r.graph.Nodes))
	for k := range r.graph.Nodes {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, n := range names {
		visit(n)
	}
	return out
}

// LockFromGraph builds a fresh Lock that records every node in g.
// The order + per-entry sort matches lockfile.Marshal's determinism
// policy.
func LockFromGraph(g *Graph) *lockfile.Lock {
	if g == nil {
		return &lockfile.Lock{Version: lockfile.SchemaVersion}
	}
	out := &lockfile.Lock{Version: lockfile.SchemaVersion}
	for _, name := range g.Order {
		n := g.Nodes[name]
		if n == nil || n.Fetched == nil {
			continue
		}
		var deps []lockfile.Dependency
		for _, c := range n.Deps {
			cn := g.Nodes[c]
			if cn == nil || cn.Fetched == nil {
				continue
			}
			deps = append(deps, lockfile.Dependency{
				Name:    c,
				Version: cn.Fetched.Version,
			})
		}
		out.Packages = append(out.Packages, lockfile.Package{
			Name:         n.Name,
			Version:      n.Fetched.Version,
			Source:       n.Source.URI(),
			Checksum:     n.Fetched.Checksum,
			Dependencies: deps,
		})
	}
	return out
}

// Vendor materializes every node in g into env.VendorDir so the
// downstream resolver / checker can load deps as ordinary packages.
//
// Layout written under env.VendorDir:
//
//	<env.VendorDir>/
//	├── <name>/         (canonical name — local alias in osty.toml)
//	│   ├── osty.toml
//	│   └── *.osty
//	└── ...
//
// Path dependencies are symlinked; git + registry deps are
// referenced by the cache copy through a symlink to keep vendor
// manipulation cheap. On Windows / systems where symlinks aren't
// available we fall back to a recursive directory copy.
func Vendor(g *Graph, env *Env) error {
	if g == nil {
		return nil
	}
	if err := ensureDir(env.VendorDir); err != nil {
		return err
	}
	for _, name := range g.Order {
		n := g.Nodes[name]
		if n == nil || n.Fetched == nil {
			continue
		}
		dst := filepath.Join(env.VendorDir, name)
		if err := replaceSymlink(n.Fetched.LocalDir, dst); err != nil {
			// Symlink creation can fail with ErrPermission /
			// ErrUnsupported on Windows accounts that lack the
			// SeCreateSymbolicLinkPrivilege, on filesystems mounted
			// without symlink support (some FUSE mounts), or in
			// sandboxed CI runners. Fall back to a directory copy
			// rather than failing the build.
			if !shouldFallbackToCopy(err) {
				return fmt.Errorf("vendor %s: %w", name, err)
			}
			if cerr := copyVendorDir(n.Fetched.LocalDir, dst); cerr != nil {
				return fmt.Errorf("vendor %s: copy fallback: %w (after symlink: %v)", name, cerr, err)
			}
		}
	}
	return nil
}

// shouldFallbackToCopy reports whether the symlink failure is one of
// the expected "this OS / FS doesn't allow it" classes rather than a
// real I/O bug we'd want to surface.
func shouldFallbackToCopy(err error) bool {
	return errors.Is(err, os.ErrPermission) ||
		errors.Is(err, errUnsupported)
}

// copyVendorDir does the directory copy fallback. It mirrors the
// vendoring contract of the symlink path: any existing dst (file,
// symlink, or empty dir) is removed first; non-empty user dirs are
// refused. After the copy the destination is a fresh, owned
// directory tree under env.VendorDir.
func copyVendorDir(src, dst string) error {
	if err := ensureDir(filepath.Dir(dst)); err != nil {
		return err
	}
	if err := removeIfSafe(dst); err != nil {
		return err
	}
	return copyTree(src, dst)
}

// copyTree recursively copies src into dst, creating dst along the
// way. Symlinks within src are recreated as symlinks (preserving
// intra-package layout); regular files are copied byte-for-byte;
// other special files are refused, matching the tarball extraction
// policy.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		switch {
		case info.IsDir():
			return os.MkdirAll(target, 0o755)
		case info.Mode()&os.ModeSymlink != 0:
			link, lerr := os.Readlink(path)
			if lerr != nil {
				return lerr
			}
			_ = os.Remove(target)
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			return copyFile(path, target, info.Mode().Perm())
		default:
			// Sockets, devices, pipes — never expected inside an osty
			// package; refuse explicitly so a broken cache entry
			// surfaces fast.
			return fmt.Errorf("unsupported file type at %s", path)
		}
	})
}

// copyFile streams src to dst with the given perm, creating any
// missing parent directories.
func copyFile(src, dst string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// replaceSymlink makes dst point at src. Any existing dst (file,
// symlink, or empty directory) is removed first. Non-empty directory
// contents are refused to avoid surprising deletions.
func replaceSymlink(src, dst string) error {
	if err := ensureDir(filepath.Dir(dst)); err != nil {
		return err
	}
	if err := removeIfSafe(dst); err != nil {
		return err
	}
	return symlinkFunc(src, dst)
}

// removeIfSafe is cautious: only removes symlinks or empty entries,
// never deletes a populated user directory (what if the user
// stashed work there).
func removeIfSafe(dst string) error {
	info, err := lstatFunc(dst)
	if err != nil {
		if isNotExistErr(err) {
			return nil
		}
		return err
	}
	if info.Mode()&osModeSymlink != 0 {
		return removeFunc(dst)
	}
	if info.IsDir() {
		entries, rerr := readDirFunc(dst)
		if rerr != nil {
			return rerr
		}
		if len(entries) > 0 {
			return fmt.Errorf("refusing to remove non-empty directory %s", dst)
		}
		return removeFunc(dst)
	}
	return removeFunc(dst)
}

// LockfileChange describes a single difference between an existing
// lockfile and the lockfile that would be produced by re-resolving
// the current manifest. Used by `--locked` / `--frozen` enforcement
// so CI can fail loudly when a contributor forgot to commit an
// updated osty.lock.
type LockfileChange struct {
	Name       string
	Kind       string // "added", "removed", "version", "checksum", "source"
	OldVersion string
	NewVersion string
	Detail     string // free-form context (e.g. old/new checksum)
}

// String renders a change for human display.
func (c LockfileChange) String() string {
	switch c.Kind {
	case "added":
		return fmt.Sprintf("+ %s %s", c.Name, c.NewVersion)
	case "removed":
		return fmt.Sprintf("- %s %s", c.Name, c.OldVersion)
	case "version":
		return fmt.Sprintf("~ %s %s -> %s", c.Name, c.OldVersion, c.NewVersion)
	case "checksum":
		return fmt.Sprintf("~ %s %s checksum changed (%s)", c.Name, c.NewVersion, c.Detail)
	case "source":
		return fmt.Sprintf("~ %s source changed (%s)", c.Name, c.Detail)
	}
	return fmt.Sprintf("? %s", c.Name)
}

// DiffLock returns the differences from old to new. A nil old lock is
// treated as "every package is added"; a nil new lock as "every
// package is removed". Order is sorted by package name for stable
// reporting.
func DiffLock(old, new *lockfile.Lock) []LockfileChange {
	o := map[string]lockfile.Package{}
	if old != nil {
		for _, p := range old.Packages {
			o[p.Name] = p
		}
	}
	n := map[string]lockfile.Package{}
	if new != nil {
		for _, p := range new.Packages {
			n[p.Name] = p
		}
	}
	names := map[string]bool{}
	for k := range o {
		names[k] = true
	}
	for k := range n {
		names[k] = true
	}
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []LockfileChange
	for _, k := range keys {
		op, oOK := o[k]
		np, nOK := n[k]
		switch {
		case !oOK && nOK:
			out = append(out, LockfileChange{Name: k, Kind: "added", NewVersion: np.Version})
		case oOK && !nOK:
			out = append(out, LockfileChange{Name: k, Kind: "removed", OldVersion: op.Version})
		case op.Version != np.Version:
			out = append(out, LockfileChange{
				Name: k, Kind: "version",
				OldVersion: op.Version, NewVersion: np.Version,
			})
		case op.Checksum != np.Checksum:
			out = append(out, LockfileChange{
				Name: k, Kind: "checksum",
				NewVersion: np.Version,
				Detail:     fmt.Sprintf("%s -> %s", short(op.Checksum), short(np.Checksum)),
			})
		case op.Source != np.Source:
			out = append(out, LockfileChange{
				Name: k, Kind: "source",
				Detail: fmt.Sprintf("%s -> %s", op.Source, np.Source),
			})
		}
	}
	return out
}

func short(s string) string {
	const prefix = "sha256:"
	t := strings.TrimPrefix(s, prefix)
	if len(t) > 12 {
		return prefix + t[:12]
	}
	return s
}

// applyLockPin tightens src's version requirement to the version
// recorded in the lockfile when (a) the lockfile has an entry for
// this dep's local name, and (b) the pinned version still satisfies
// the manifest's declared requirement. Currently applies only to
// registry sources — git sources already pin via tag/rev in the
// manifest itself, and path sources are re-read from disk.
func applyLockPin(src Source, d manifest.Dependency, lock *lockfile.Lock) {
	if lock == nil {
		return
	}
	rs, ok := src.(*registrySource)
	if !ok {
		return
	}
	pinned := lock.FindByName(d.Name)
	if len(pinned) == 0 {
		return
	}
	// Take the first matching-source pinned version (we don't admit
	// multiple per name in the simple resolver). Verify it still
	// satisfies the declared req before narrowing — a manifest edit may
	// have invalidated it. Also require the source URI to match so an
	// old path/git pin with the same alias does not accidentally
	// constrain a registry dep.
	var pin lockfile.Package
	found := false
	for _, p := range pinned {
		if p.Source == "" || p.Source == rs.URI() {
			pin = p
			found = true
			break
		}
	}
	if !found {
		return
	}
	pv, err := semver.ParseVersion(pin.Version)
	if err != nil {
		return
	}
	req, err := semver.ParseReq(rs.versionReq)
	if err != nil {
		return
	}
	if !req.Match(pv) {
		return
	}
	// Narrow to an exact match. ParseReq accepts "=X.Y.Z".
	rs.versionReq = "=" + pv.String()
}
