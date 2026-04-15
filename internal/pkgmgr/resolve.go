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
	Name     string
	Source   Source
	Fetched  *FetchedPackage
	// Deps is the set of *local* names this node depends on
	// (transitive edges). Populated during graph construction.
	Deps []string
}

// Resolve walks the root manifest's dependency closure and returns
// the resolved Graph. When a lockfile is present at env.ProjectRoot,
// its pins are used to keep versions stable; unknown or newly-added
// dependencies are resolved fresh.
//
// Algorithm — two phases:
//
//  1. Greedy DFS. Each unique local name is fetched once at the
//     first version that satisfies its requirement (lockfile pin
//     honored when present). Every constraint encountered along the
//     way is recorded with its parent chain.
//
//  2. Iterative unification. For each registry-backed node, the
//     resolver intersects every recorded version constraint and
//     verifies the chosen pin still satisfies them all. When it
//     doesn't, the node is re-fetched at the highest version
//     satisfying the union and the new pin's transitive deps are
//     re-walked so newly-introduced constraints fold in. The pass
//     loops to a fixed point or reports an unsatisfiable conflict.
//
// Source-kind mismatches (path vs git vs registry for the same
// local name) and path / git URI mismatches are caught structurally
// during phase 1 with the offending parent chain in the message.
//
// True backtracking (changing a parent's already-pinned version to
// admit a child) is not yet implemented; that would be a follow-up
// pubgrub-style solver.
func Resolve(ctx context.Context, root *manifest.Manifest, env *Env) (*Graph, error) {
	if root == nil {
		return nil, fmt.Errorf("nil root manifest")
	}
	existing, err := lockfile.Read(env.ProjectRoot)
	if err != nil {
		return nil, err
	}
	r := &resolver{
		env:         env,
		lock:        existing,
		root:        root,
		graph:       &Graph{Root: root, Nodes: map[string]*ResolvedNode{}},
		inflight:    map[string]bool{},
		constraints: map[string][]constraintRef{},
	}
	for _, d := range root.Dependencies {
		if _, err := r.resolveDep(ctx, d, ""); err != nil {
			return nil, err
		}
	}
	for _, d := range root.DevDependencies {
		if _, err := r.resolveDep(ctx, d, ""); err != nil {
			return nil, err
		}
	}
	// Iterative refinement: the greedy first pass may have committed
	// to versions that don't satisfy every constraint a transitive
	// parent placed on them (the classic diamond case). Re-walk the
	// graph upgrading registry pins to the highest version that
	// satisfies the union of every recorded constraint, then revisit
	// the new pin's transitive deps. Loop until the graph stops
	// changing or we exceed the safety bound.
	if err := r.unifyConstraints(ctx); err != nil {
		return nil, err
	}
	r.graph.Order = r.topoOrder()
	return r.graph, nil
}

// constraintRef is one observation of a dependency requirement.
// Captured at every encounter (first-time and re-encounter) so the
// unification pass can compute the intersection of every constraint
// placed on a single local name and surface the parent chain in
// conflict reports.
type constraintRef struct {
	parent string              // local name of the parent ("" = root manifest)
	dep    manifest.Dependency // the original dep spec, preserves Path/Git/VersionReq
}

type resolver struct {
	env         *Env
	lock        *lockfile.Lock
	root        *manifest.Manifest
	graph       *Graph
	inflight    map[string]bool
	constraints map[string][]constraintRef
}

// resolveDep fetches dep, records it in the graph, then recurses
// into its own dependencies. The parent argument is the local name
// of whoever introduced this dep (the empty string for root deps);
// it's recorded with each constraint so conflict reports can show
// the full chain.
//
// Re-encounters: when the same local name is requested again from a
// different point in the graph, the second spec is recorded as a
// constraint. If it's structurally incompatible with the first
// (different source kind, mismatched path / git URI), the conflict
// is fatal. Version-only differences are deferred to unifyConstraints,
// which runs after the greedy walk and can pick a higher version
// satisfying every collected requirement.
func (r *resolver) resolveDep(ctx context.Context, d manifest.Dependency, parent string) (*ResolvedNode, error) {
	r.recordConstraint(d, parent)
	if existing, ok := r.graph.Nodes[d.Name]; ok {
		if err := r.checkSourceKindCompatible(existing, d, parent); err != nil {
			return nil, err
		}
		return existing, nil
	}
	if r.inflight[d.Name] {
		return nil, fmt.Errorf("cyclic dependency through %q", d.Name)
	}
	r.inflight[d.Name] = true
	defer delete(r.inflight, d.Name)

	src, err := NewSource(d)
	if err != nil {
		return nil, err
	}
	// If the lockfile pins a previously-resolved version that still
	// satisfies the manifest's requirement, narrow the source so we
	// fetch exactly that version. This keeps `osty build` reproducible
	// across runs without forcing a network round-trip on every
	// resolve. `osty update` clears the pin before calling Resolve, so
	// honoring it here doesn't block intended upgrades.
	applyLockPin(src, d, r.lock)
	fetched, err := src.Fetch(ctx, r.env)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", d.Name, err)
	}
	node := &ResolvedNode{
		Name:    d.Name,
		Source:  src,
		Fetched: fetched,
	}
	r.graph.Nodes[d.Name] = node

	// Recurse into the fetched package's own deps. Dev-deps are NOT
	// followed for transitive packages — only the root's dev-deps
	// matter.
	for _, sub := range fetched.Manifest.Dependencies {
		child, err := r.resolveDep(ctx, sub, d.Name)
		if err != nil {
			return nil, err
		}
		node.Deps = append(node.Deps, child.Name)
	}
	return node, nil
}

// recordConstraint appends one (parent, dep) pair to the running
// constraint list for d.Name. Called on every encounter (first time
// and every re-encounter) so unifyConstraints sees the full picture.
func (r *resolver) recordConstraint(d manifest.Dependency, parent string) {
	r.constraints[d.Name] = append(r.constraints[d.Name], constraintRef{
		parent: parent,
		dep:    d,
	})
}

// checkSourceKindCompatible enforces the structural rules: a name
// already pinned at a path source can't later be requested from git
// or a registry; a name pinned to a git URL must come from the same
// URL+ref everywhere; etc. Version-only conflicts are NOT raised
// here — those are handled by unifyConstraints.
func (r *resolver) checkSourceKindCompatible(existing *ResolvedNode, d manifest.Dependency, parent string) error {
	wantSrc, err := NewSource(d)
	if err != nil {
		return err
	}
	if existing.Source.Kind() != wantSrc.Kind() {
		return r.conflictError(d.Name,
			fmt.Sprintf("source kind mismatch: %s wants %s, but %s already pinned as %s",
				describeParent(parent), wantSrc.Kind(),
				d.Name, existing.Source.Kind()))
	}
	switch existing.Source.Kind() {
	case SourcePath:
		if existing.Source.URI() != wantSrc.URI() {
			return r.conflictError(d.Name,
				fmt.Sprintf("path mismatch: %s vs %s",
					existing.Source.URI(), wantSrc.URI()))
		}
	case SourceGit:
		if existing.Source.URI() != wantSrc.URI() {
			return r.conflictError(d.Name,
				fmt.Sprintf("git source mismatch: %s vs %s",
					existing.Source.URI(), wantSrc.URI()))
		}
	}
	return nil
}

// conflictError formats a diamond-style conflict report including
// every parent chain that referenced the offending name. The output
// is multi-line so the user can immediately see who placed which
// requirement.
func (r *resolver) conflictError(name, reason string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "dependency conflict on %q: %s", name, reason)
	if refs := r.constraints[name]; len(refs) > 0 {
		b.WriteString("\n  required by:")
		for _, c := range refs {
			fmt.Fprintf(&b, "\n    - %s -> %s", describeParent(c.parent), describeDepReq(c.dep))
		}
	}
	return errors.New(b.String())
}

func describeParent(p string) string {
	if p == "" {
		return "<root>"
	}
	return p
}

func describeDepReq(d manifest.Dependency) string {
	switch {
	case d.Path != "":
		return fmt.Sprintf("%s (path %s)", d.Name, d.Path)
	case d.Git != nil:
		return fmt.Sprintf("%s (git %s)", d.Name, gitRefSummary(d.Git))
	case d.VersionReq != "":
		return fmt.Sprintf("%s %s", d.Name, d.VersionReq)
	}
	return d.Name
}

func gitRefSummary(g *manifest.GitSource) string {
	parts := []string{g.URL}
	if g.Tag != "" {
		parts = append(parts, "tag="+g.Tag)
	}
	if g.Branch != "" {
		parts = append(parts, "branch="+g.Branch)
	}
	if g.Rev != "" {
		parts = append(parts, "rev="+g.Rev)
	}
	return strings.Join(parts, " ")
}

// unifyConstraints is the post-DFS validation/repair pass. For each
// registry-backed name, it intersects every recorded version
// requirement and verifies that the currently-pinned version
// satisfies them all. When it doesn't, the resolver re-fetches at
// the highest version satisfying the union, swaps the node in
// place, and re-walks the new pin's transitive deps so any new
// constraints they introduce are caught the next iteration.
//
// Iterates to a fixed point. Bounded at maxIters to defend against
// pathological version-graph oscillation; in practice convergence
// happens in 1–2 passes for real-world graphs.
func (r *resolver) unifyConstraints(ctx context.Context) error {
	const maxIters = 16
	for iter := 0; iter < maxIters; iter++ {
		changed := false
		// Iterate names in deterministic order so error messages and
		// trace output don't depend on map iteration order.
		names := make([]string, 0, len(r.graph.Nodes))
		for name := range r.graph.Nodes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			node := r.graph.Nodes[name]
			if node == nil || node.Source == nil || node.Source.Kind() != SourceRegistry {
				continue
			}
			rs, ok := node.Source.(*registrySource)
			if !ok {
				continue
			}
			combined, err := r.intersectRegistryReqs(name)
			if err != nil {
				return err
			}
			cur, err := semver.ParseVersion(node.Fetched.Version)
			if err != nil {
				return fmt.Errorf("internal: cannot parse pinned version %q for %s: %w",
					node.Fetched.Version, name, err)
			}
			if combined.Match(cur) {
				continue // pin already satisfies every constraint
			}
			// Re-fetch at the union req. The narrowed source is
			// driven by mutating versionReq directly; the existing
			// applyLockPin path uses the same trick.
			newRs := &registrySource{
				name:         rs.name,
				packageName:  rs.packageName,
				versionReq:   combined.String(),
				registryName: rs.registryName,
			}
			fetched, ferr := newRs.Fetch(ctx, r.env)
			if ferr != nil {
				return r.conflictError(name,
					fmt.Sprintf("no version satisfies the combined constraints (%s): %v",
						combined.String(), ferr))
			}
			// Swap in the upgraded pin. Drop the old transitive
			// edges; we'll repopulate them by re-walking the new
			// pin's manifest below. Children that are still reached
			// from elsewhere remain valid in r.graph.Nodes; orphans
			// are pruned by topoOrder later.
			node.Source = newRs
			node.Fetched = fetched
			node.Deps = node.Deps[:0]
			for _, sub := range fetched.Manifest.Dependencies {
				child, cerr := r.resolveDep(ctx, sub, name)
				if cerr != nil {
					return cerr
				}
				node.Deps = append(node.Deps, child.Name)
			}
			changed = true
		}
		if !changed {
			return nil
		}
	}
	return errors.New("dependency resolver did not converge after 16 iterations (cyclic version constraints?)")
}

// intersectRegistryReqs returns a single semver.Req representing the
// AND of every version requirement recorded for `name`. Empty / "*"
// reqs contribute nothing; explicit reqs are joined with a space
// (semver's ParseReq accepts space-separated conjunctions).
func (r *resolver) intersectRegistryReqs(name string) (semver.Req, error) {
	var clauses []string
	seen := map[string]bool{}
	for _, c := range r.constraints[name] {
		req := strings.TrimSpace(c.dep.VersionReq)
		if req == "" || req == "*" {
			continue
		}
		if seen[req] {
			continue
		}
		seen[req] = true
		clauses = append(clauses, req)
	}
	if len(clauses) == 0 {
		return semver.ParseReq("*")
	}
	combined := strings.Join(clauses, " ")
	parsed, err := semver.ParseReq(combined)
	if err != nil {
		return semver.Req{}, fmt.Errorf("intersect %s: %w", name, err)
	}
	return parsed, nil
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
	// Take the first pinned version (we don't admit multiple per name
	// in the simple resolver). Verify it still satisfies the declared
	// req before narrowing — a manifest edit may have invalidated it.
	pin := pinned[0]
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

// joinURIFields renders human-readable source info for logging. Kept
// package-local because most callers only need Source.URI().
func joinURIFields(parts ...string) string {
	parts2 := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			parts2 = append(parts2, p)
		}
	}
	return strings.Join(parts2, " ")
}
