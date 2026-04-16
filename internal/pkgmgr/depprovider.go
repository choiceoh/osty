package pkgmgr

import (
	"path/filepath"
	"sort"

	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/resolve"
)

// NewDepProvider returns a resolve.DepProvider backed by the given
// resolved graph + manifest. The provider maps the path the user
// wrote in `use <rawPath>` to the on-disk directory we vendored.
//
// Mapping rules (first match wins):
//
//  1. rawPath equals a resolved graph alias or top-level
//     [dependencies] alias  → vendor dir.
//     Graph aliases include transitive deps discovered while walking
//     fetched package manifests.
//     Matches bare-name imports like `use fastjson` against a
//     `fastjson = { git = "..." }` entry.
//
//  2. rawPath is a URL that matches a graph/source git URL  →
//     vendor dir.
//     Matches `use github.com/user/fastjson` against the same
//     `fastjson = { git = "github.com/user/fastjson" }`. Trailing
//     `.git` and scheme are tolerated in either direction.
//
//  3. rawPath's last segment equals a graph or manifest alias  →
//     vendor dir.
//     Fallback for `use github.com/user/lib` when the alias is just
//     `lib` — the spec says `lib` is the default alias and the source
//     form still resolves.
//
// Returns ("", false) when no match is found; the workspace then
// surfaces a clear "not in dependencies" diagnostic.
func NewDepProvider(m *manifest.Manifest, graph *Graph, env *Env) resolve.DepProvider {
	return &depProvider{m: m, graph: graph, env: env}
}

type depProvider struct {
	m     *manifest.Manifest
	graph *Graph
	env   *Env
}

func (p *depProvider) LookupDep(rawPath string) (string, bool) {
	if p == nil || p.m == nil || p.env == nil {
		return "", false
	}
	decision := GolegacyLookupDependency(rawPath, p.graphLookupItems(), manifestLookupItems(p.m))
	if decision.Found {
		return filepath.Join(p.env.VendorDir, decision.Name), true
	}
	return "", false
}

func (p *depProvider) graphLookupItems() []GolegacyDepLookupItem {
	if p == nil || p.graph == nil {
		return nil
	}
	out := make([]GolegacyDepLookupItem, 0, len(p.graph.Nodes))
	for _, name := range p.graphNodeNames() {
		n := p.graph.Nodes[name]
		if n == nil {
			continue
		}
		item := GolegacyDepLookupItem{Name: name}
		gs, ok := n.Source.(*gitSource)
		if ok {
			item.GitURL = gs.url
		}
		out = append(out, item)
	}
	return out
}

func (p *depProvider) graphNodeNames() []string {
	if p == nil || p.graph == nil {
		return nil
	}
	if len(p.graph.Order) > 0 {
		return append([]string(nil), p.graph.Order...)
	}
	names := make([]string, 0, len(p.graph.Nodes))
	for name := range p.graph.Nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func manifestLookupItems(m *manifest.Manifest) []GolegacyDepLookupItem {
	if m == nil {
		return nil
	}
	out := make([]GolegacyDepLookupItem, 0, len(m.Dependencies)+len(m.DevDependencies))
	for _, d := range m.Dependencies {
		out = append(out, dependencyLookupItem(d))
	}
	for _, d := range m.DevDependencies {
		out = append(out, dependencyLookupItem(d))
	}
	return out
}

func dependencyLookupItem(d manifest.Dependency) GolegacyDepLookupItem {
	item := GolegacyDepLookupItem{Name: d.Name}
	if d.Git != nil {
		item.GitURL = d.Git.URL
	}
	return item
}
