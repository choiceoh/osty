package pkgmgr

import (
	"path/filepath"
	"strings"

	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/resolve"
)

// NewDepProvider returns a resolve.DepProvider backed by the given
// resolved graph + manifest. The provider maps the path the user
// wrote in `use <rawPath>` to the on-disk directory we vendored.
//
// Mapping rules (first match wins):
//
//  1. rawPath equals a top-level [dependencies] alias  → vendor dir.
//     Matches bare-name imports like `use fastjson` against a
//     `fastjson = { git = "..." }` entry.
//
//  2. rawPath is a URL that matches a dep's git URL  → vendor dir.
//     Matches `use github.com/user/fastjson` against the same
//     `fastjson = { git = "github.com/user/fastjson" }`. Trailing
//     `.git` and scheme are tolerated in either direction.
//
//  3. rawPath's last segment equals a dep alias  → vendor dir.
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
	// Rule 1: direct alias match.
	if dep, ok := findDepByAlias(p.m, rawPath); ok {
		return filepath.Join(p.env.VendorDir, dep.Name), true
	}
	// Rule 2: git URL match.
	if strings.ContainsAny(rawPath, "/") {
		if dep, ok := findDepByGitURL(p.m, rawPath); ok {
			return filepath.Join(p.env.VendorDir, dep.Name), true
		}
	}
	// Rule 3: last-segment alias match.
	if i := strings.LastIndex(rawPath, "/"); i >= 0 {
		lastSeg := rawPath[i+1:]
		if dep, ok := findDepByAlias(p.m, lastSeg); ok {
			return filepath.Join(p.env.VendorDir, dep.Name), true
		}
	}
	return "", false
}

// findDepByAlias walks both dependency tables and returns the entry
// whose Name equals alias. Matches exactly — callers normalize
// before calling.
func findDepByAlias(m *manifest.Manifest, alias string) (manifest.Dependency, bool) {
	for _, d := range m.Dependencies {
		if d.Name == alias {
			return d, true
		}
	}
	for _, d := range m.DevDependencies {
		if d.Name == alias {
			return d, true
		}
	}
	return manifest.Dependency{}, false
}

// findDepByGitURL matches rawPath against every Dependency.Git.URL,
// tolerating trailing `.git` and scheme differences (https:// vs
// bare `github.com/...`). Extracts the comparable form and checks
// both sides.
func findDepByGitURL(m *manifest.Manifest, rawPath string) (manifest.Dependency, bool) {
	needle := normalizeGitURL(rawPath)
	check := func(d manifest.Dependency) bool {
		if d.Git == nil {
			return false
		}
		return normalizeGitURL(d.Git.URL) == needle
	}
	for _, d := range m.Dependencies {
		if check(d) {
			return d, true
		}
	}
	for _, d := range m.DevDependencies {
		if check(d) {
			return d, true
		}
	}
	return manifest.Dependency{}, false
}

// normalizeGitURL strips scheme, trailing `.git`, and trailing
// slash so two git URLs pointing at the same repo compare equal.
// Input can be `https://github.com/user/lib.git`, `github.com/user/lib`,
// `git@github.com:user/lib` (converted to slash form), etc.
func normalizeGitURL(u string) string {
	u = strings.TrimSuffix(u, "/")
	u = strings.TrimSuffix(u, ".git")
	// Strip scheme.
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	// scp-style `git@host:user/repo` → `host/user/repo`.
	if strings.HasPrefix(u, "git@") {
		u = strings.TrimPrefix(u, "git@")
		u = strings.Replace(u, ":", "/", 1)
	}
	return u
}
