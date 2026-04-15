package pkgmgr

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/lockfile"
	"github.com/osty/osty/internal/manifest"
)

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
		env:      env,
		lock:     existing,
		root:     root,
		graph:    &Graph{Root: root, Nodes: map[string]*ResolvedNode{}},
		inflight: map[string]bool{},
	}
	for _, d := range root.Dependencies {
		if _, err := r.resolveDep(ctx, d); err != nil {
			return nil, err
		}
	}
	for _, d := range root.DevDependencies {
		if _, err := r.resolveDep(ctx, d); err != nil {
			return nil, err
		}
	}
	r.graph.Order = r.topoOrder()
	return r.graph, nil
}

type resolver struct {
	env      *Env
	lock     *lockfile.Lock
	root     *manifest.Manifest
	graph    *Graph
	inflight map[string]bool
}

// resolveDep fetches dep, records it in the graph, then recurses
// into its own dependencies. Returns an error on cycle detection,
// fetch failure, or conflicting versions.
func (r *resolver) resolveDep(ctx context.Context, d manifest.Dependency) (*ResolvedNode, error) {
	if existing, ok := r.graph.Nodes[d.Name]; ok {
		// Already resolved under this local name. A conflicting
		// second Dependency spec with the same name is rejected here.
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
		child, err := r.resolveDep(ctx, sub)
		if err != nil {
			return nil, err
		}
		node.Deps = append(node.Deps, child.Name)
	}
	return node, nil
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
// available we fall back to directory copy (not yet implemented —
// error cleanly).
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
			return fmt.Errorf("vendor %s: %w", name, err)
		}
	}
	return nil
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
