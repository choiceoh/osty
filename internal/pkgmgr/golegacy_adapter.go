package pkgmgr

import (
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/osty/osty/internal/lockfile"
	hostsemver "github.com/osty/osty/internal/pkgmgr/semver"
)

// GolegacyRegistryCandidate is the Go-facing view of one registry index row
// consumed by the Osty-authored package-manager core.
type GolegacyRegistryCandidate struct {
	PackageName string
	Version     string
	Checksum    string
	Yanked      bool
}

// GolegacyDepLookupItem is a host-facing import lookup row.
type GolegacyDepLookupItem struct {
	Name   string
	GitURL string
}

// GolegacyDepLookupDecision is the selected dependency alias for an import.
type GolegacyDepLookupDecision struct {
	Found bool
	Name  string
}

// GolegacyResolveDecision is the adapter result for registry version choice.
type GolegacyResolveDecision struct {
	Found       bool
	Name        string
	PackageName string
	Version     string
	Source      string
	Checksum    string
	FromLock    bool
	Message     string
}

// GolegacyRegistryRequest is the transport-agnostic request metadata produced
// by the Osty-authored registry protocol core. The host shim still performs
// actual HTTP I/O.
type GolegacyRegistryRequest struct {
	Method        string
	URL           string
	Accept        string
	ContentType   string
	Authorization string
	Checksum      string
	Metadata      string
}

// GolegacyMarshalLock renders l through the Osty-authored lockfile writer.
func GolegacyMarshalLock(l *lockfile.Lock) ([]byte, error) {
	pkgs, err := golegacyLockPackages(l)
	if err != nil {
		return nil, err
	}
	return []byte(selfPkgMarshalLock(pkgs)), nil
}

// GolegacyDiffLock diffs two lockfiles through the Osty-authored diff core.
func GolegacyDiffLock(old, new *lockfile.Lock) ([]LockfileChange, error) {
	oldPkgs, err := golegacyLockPackages(old)
	if err != nil {
		return nil, err
	}
	newPkgs, err := golegacyLockPackages(new)
	if err != nil {
		return nil, err
	}
	changes := selfPkgDiffLocks(selfPkgSortLockPackages(oldPkgs), selfPkgSortLockPackages(newPkgs))
	out := make([]LockfileChange, 0, len(changes))
	for _, c := range changes {
		out = append(out, golegacyLockChange(c))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GolegacySelectRegistryCandidate chooses a registry dependency version using
// the Osty-authored resolver core.
func GolegacySelectRegistryCandidate(name, packageName, registryName, versionReq string, candidates []GolegacyRegistryCandidate, locked *lockfile.Lock) (GolegacyResolveDecision, error) {
	req, err := golegacyReqFromString(versionReq)
	if err != nil {
		return GolegacyResolveDecision{}, err
	}
	lockPkgs, err := golegacyLockPackages(locked)
	if err != nil {
		return GolegacyResolveDecision{}, err
	}
	dep := selfPkgRegistryDependencyAs(name, packageName, registryName, req)
	golegacyCandidates := make([]*SelfPkgCandidate, 0, len(candidates))
	for _, c := range candidates {
		v, err := golegacyVersionFromString(c.Version)
		if err != nil {
			return GolegacyResolveDecision{}, fmt.Errorf("candidate %s@%s: %w", c.PackageName, c.Version, err)
		}
		golegacyCandidates = append(golegacyCandidates, selfPkgCandidate(c.PackageName, v, c.Checksum, c.Yanked))
	}
	return golegacyResolveDecision(selfPkgSelectRegistryCandidate(dep, golegacyCandidates, lockPkgs)), nil
}

// GolegacyRegistryVersionsRequest builds the registry index request metadata.
func GolegacyRegistryVersionsRequest(baseURL, name, token string) GolegacyRegistryRequest {
	return golegacyRegistryRequest(selfPkgRegistryVersionsRequest(baseURL, name, token))
}

// GolegacyRegistryTarballRequest builds the registry tarball request metadata.
func GolegacyRegistryTarballRequest(baseURL, name, version, token string) GolegacyRegistryRequest {
	return golegacyRegistryRequest(selfPkgRegistryTarballRequest(baseURL, name, version, token))
}

// GolegacyRegistryPublishRequest builds the registry publish request metadata.
func GolegacyRegistryPublishRequest(baseURL, name, version, token, checksum, metadata string) GolegacyRegistryRequest {
	return golegacyRegistryRequest(selfPkgRegistryPublishRequest(baseURL, name, version, token, checksum, metadata))
}

// GolegacyRegistryYankRequest builds the yank / unyank request metadata.
func GolegacyRegistryYankRequest(baseURL, name, version, token string, yanked bool) GolegacyRegistryRequest {
	return golegacyRegistryRequest(selfPkgRegistryYankRequest(baseURL, name, version, token, yanked))
}

// GolegacyRankRegistryCandidates filters and sorts registry candidates through
// the Osty-authored semver selection core.
func GolegacyRankRegistryCandidates(name, packageName, registryName, versionReq string, candidates []GolegacyRegistryCandidate) ([]GolegacyRegistryCandidate, error) {
	req, err := golegacyReqFromString(versionReq)
	if err != nil {
		return nil, err
	}
	dep := selfPkgRegistryDependencyAs(name, packageName, registryName, req)
	type ranked struct {
		candidate GolegacyRegistryCandidate
		version   *SemVersion
	}
	out := make([]ranked, 0, len(candidates))
	for _, c := range candidates {
		v, err := golegacyVersionFromString(c.Version)
		if err != nil {
			return nil, fmt.Errorf("candidate %s@%s: %w", c.PackageName, c.Version, err)
		}
		golegacyCandidate := selfPkgCandidate(c.PackageName, v, c.Checksum, c.Yanked)
		if !selfPkgCandidateMatches(dep, golegacyCandidate) {
			continue
		}
		out = append(out, ranked{candidate: c, version: v})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return compareSemVersion(out[i].version, out[j].version) > 0
	})
	result := make([]GolegacyRegistryCandidate, 0, len(out))
	for _, r := range out {
		result = append(result, r.candidate)
	}
	return result, nil
}

// GolegacyVerifyChecksum validates an already-computed checksum through the
// Osty-authored package-manager policy.
func GolegacyVerifyChecksum(want, got string) error {
	check := selfPkgVerifyChecksum(want, got)
	if check.ok {
		return nil
	}
	return fmt.Errorf("%s", check.message)
}

// GolegacyPathSourceURI renders the lockfile URI for a path dependency.
func GolegacyPathSourceURI(path string) string {
	return selfPkgSourceURI(selfPkgPathDependency("", path))
}

// GolegacyRegistrySourceURI renders the lockfile URI for a registry dependency.
func GolegacyRegistrySourceURI(registryName string) string {
	return selfPkgSourceURI(selfPkgRegistryDependencyAs("", "", registryName, anySemReq()))
}

// GolegacyGitSourceURI renders the lockfile URI for a git dependency.
func GolegacyGitSourceURI(url, tag, branch, rev string) string {
	return selfPkgGitURI(selfPkgGitRef(url, tag, branch, rev))
}

// GolegacyGitCheckoutRef returns the concrete git ref expression for a source.
func GolegacyGitCheckoutRef(url, tag, branch, rev string) string {
	return selfPkgGitCheckoutRef(selfPkgGitRef(url, tag, branch, rev))
}

// GolegacySanitizeURL returns the cache-key-safe form of a URL.
func GolegacySanitizeURL(url string) string {
	return selfPkgSanitizeURL(url)
}

// GolegacyNormalizeGitURL normalizes git URLs for import matching.
func GolegacyNormalizeGitURL(url string) string {
	return selfPkgNormalizeGitURL(url)
}

// GolegacyLookupDependency maps an import path to a dependency alias through
// the Osty-authored dep-provider policy.
func GolegacyLookupDependency(rawPath string, graphNodes, manifestDeps []GolegacyDepLookupItem) GolegacyDepLookupDecision {
	graph := make([]*SelfPkgDepLookupItem, 0, len(graphNodes))
	for _, item := range graphNodes {
		graph = append(graph, selfPkgDepLookupItem(item.Name, item.GitURL))
	}
	manifest := make([]*SelfPkgDepLookupItem, 0, len(manifestDeps))
	for _, item := range manifestDeps {
		manifest = append(manifest, selfPkgDepLookupItem(item.Name, item.GitURL))
	}
	result := selfPkgLookupDependency(rawPath, graph, manifest)
	return GolegacyDepLookupDecision{Found: result.found, Name: result.name}
}

// GolegacyTopoOrder returns a deterministic leaves-first graph order.
func GolegacyTopoOrder(g *Graph) []string {
	nodes := golegacyGraphNodes(g)
	if len(nodes) == 0 {
		return nil
	}
	return selfPkgTopoOrder(nodes)
}

// GolegacyLockFromGraph projects a resolved graph into an osty.lock through
// Osty-authored graph ordering and lock package data.
func GolegacyLockFromGraph(g *Graph) (*lockfile.Lock, error) {
	if g == nil {
		return &lockfile.Lock{Version: lockfile.SchemaVersion}, nil
	}
	order := GolegacyTopoOrder(g)
	out := &lockfile.Lock{Version: lockfile.SchemaVersion}
	for _, name := range order {
		n := g.Nodes[name]
		if n == nil || n.Fetched == nil {
			continue
		}
		pkg, err := golegacyGraphLockPackage(g, n)
		if err != nil {
			return nil, err
		}
		out.Packages = append(out.Packages, pkg)
	}
	return out, nil
}

func golegacyGraphNodes(g *Graph) []*SelfPkgGraphNode {
	if g == nil || len(g.Nodes) == 0 {
		return nil
	}
	nodes := make([]*SelfPkgGraphNode, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		if n == nil {
			continue
		}
		deps := append([]string(nil), n.Deps...)
		source := ""
		checksum := ""
		if n.Source != nil {
			source = n.Source.URI()
		}
		if n.Fetched != nil {
			checksum = n.Fetched.Checksum
		}
		nodes = append(nodes, selfPkgGraphNode(n.Name, "", stableVersion(0, 0, 0), source, checksum, deps))
	}
	return nodes
}

func golegacyGraphLockPackage(g *Graph, n *ResolvedNode) (lockfile.Package, error) {
	v, err := golegacyVersionFromString(n.Fetched.Version)
	if err != nil {
		return lockfile.Package{}, fmt.Errorf("graph package %s version %q: %w", n.Name, n.Fetched.Version, err)
	}
	deps := make([]*SelfPkgLockDependency, 0, len(n.Deps))
	for _, name := range n.Deps {
		child := g.Nodes[name]
		if child == nil || child.Fetched == nil {
			continue
		}
		dv, err := golegacyVersionFromString(child.Fetched.Version)
		if err != nil {
			return lockfile.Package{}, fmt.Errorf("graph dependency %s version %q: %w", name, child.Fetched.Version, err)
		}
		deps = append(deps, selfPkgLockDependency(name, dv, ""))
	}
	source := ""
	if n.Source != nil {
		source = n.Source.URI()
	}
	pkg := selfPkgLockPackage(n.Name, v, source, n.Fetched.Checksum, deps)
	return golegacyLockfilePackage(pkg), nil
}

func golegacyLockfilePackage(pkg *SelfPkgLockPackage) lockfile.Package {
	out := lockfile.Package{
		Name:     pkg.name,
		Version:  semVersionText(pkg.version),
		Source:   pkg.source,
		Checksum: pkg.checksum,
	}
	for _, dep := range pkg.dependencies {
		out.Dependencies = append(out.Dependencies, lockfile.Dependency{
			Name:    dep.name,
			Version: semVersionText(dep.version),
			Source:  dep.source,
		})
	}
	return out
}

func golegacyLockPackages(l *lockfile.Lock) ([]*SelfPkgLockPackage, error) {
	if l == nil {
		return nil, nil
	}
	out := make([]*SelfPkgLockPackage, 0, len(l.Packages))
	for _, p := range l.Packages {
		v, err := golegacyVersionFromString(p.Version)
		if err != nil {
			return nil, fmt.Errorf("lock package %s version %q: %w", p.Name, p.Version, err)
		}
		deps := make([]*SelfPkgLockDependency, 0, len(p.Dependencies))
		for _, d := range p.Dependencies {
			dv, err := golegacyVersionFromString(d.Version)
			if err != nil {
				return nil, fmt.Errorf("lock dependency %s version %q: %w", d.Name, d.Version, err)
			}
			deps = append(deps, selfPkgLockDependency(d.Name, dv, d.Source))
		}
		out = append(out, selfPkgLockPackage(p.Name, v, p.Source, p.Checksum, deps))
	}
	return out, nil
}

func golegacyVersionFromString(s string) (*SemVersion, error) {
	v, err := hostsemver.ParseVersion(s)
	if err != nil {
		return nil, err
	}
	return golegacyVersion(v)
}

func golegacyVersion(v hostsemver.Version) (*SemVersion, error) {
	if v.Major > math.MaxInt || v.Minor > math.MaxInt || v.Patch > math.MaxInt {
		return nil, fmt.Errorf("version component exceeds Osty Int range")
	}
	if len(v.Pre) > 3 {
		return nil, fmt.Errorf("golegacy SemVersion supports at most 3 pre-release identifiers")
	}
	pre := []SemPreIdent{preNone(), preNone(), preNone()}
	for i, item := range v.Pre {
		ident, err := golegacyPreIdent(item)
		if err != nil {
			return nil, err
		}
		pre[i] = ident
	}
	build := ""
	for i, item := range v.Build {
		if i > 0 {
			build += "."
		}
		build += item
	}
	return semVersion(int(v.Major), int(v.Minor), int(v.Patch), pre[0], pre[1], pre[2], build), nil
}

func golegacyPreIdent(s string) (SemPreIdent, error) {
	n, err := strconv.ParseInt(s, 10, 0)
	if err == nil {
		return preNumber(int(n)), nil
	}
	return preText(s), nil
}

func golegacyReqFromString(raw string) (*SemReq, error) {
	req, err := hostsemver.ParseReq(raw)
	if err != nil {
		return nil, err
	}
	allowPre := false
	clauses := make([]*SemReqClause, 0, len(req.Clauses))
	for _, c := range req.Clauses {
		if c.V.IsPrerelease() {
			allowPre = true
		}
		v, err := golegacyVersion(c.V)
		if err != nil {
			return nil, err
		}
		clauses = append(clauses, reqClause(golegacyReqOp(int(c.Op)), v))
	}
	return semReq(allowPre, clauses), nil
}

func golegacyReqOp(op int) SemReqOp {
	switch op {
	case 0:
		return SemReqOp(&SemReqOp_ReqEQ{})
	case 1:
		return SemReqOp(&SemReqOp_ReqGE{})
	case 2:
		return SemReqOp(&SemReqOp_ReqGT{})
	case 3:
		return SemReqOp(&SemReqOp_ReqLE{})
	case 4:
		return SemReqOp(&SemReqOp_ReqLT{})
	default:
		panic(fmt.Sprintf("unknown semver op %d", op))
	}
}

func golegacyLockChange(c *SelfPkgLockChange) LockfileChange {
	out := LockfileChange{
		Name:       c.name,
		OldVersion: semVersionText(c.oldVersion),
		NewVersion: semVersionText(c.newVersion),
		Detail:     c.detail,
	}
	switch c.kind.(type) {
	case *SelfPkgLockChangeKind_SelfPkgLockAdded:
		out.Kind = "added"
		out.OldVersion = ""
	case *SelfPkgLockChangeKind_SelfPkgLockRemoved:
		out.Kind = "removed"
		out.NewVersion = ""
	case *SelfPkgLockChangeKind_SelfPkgLockVersion:
		out.Kind = "version"
	case *SelfPkgLockChangeKind_SelfPkgLockChecksum:
		out.Kind = "checksum"
		out.OldVersion = ""
	case *SelfPkgLockChangeKind_SelfPkgLockSource:
		out.Kind = "source"
		out.OldVersion = ""
		out.NewVersion = ""
	default:
		out.Kind = "unknown"
	}
	return out
}

func golegacyResolveDecision(d *SelfPkgResolveDecision) GolegacyResolveDecision {
	return GolegacyResolveDecision{
		Found:       d.found,
		Name:        d.name,
		PackageName: d.packageName,
		Version:     semVersionText(d.version),
		Source:      d.source,
		Checksum:    d.checksum,
		FromLock:    d.fromLock,
		Message:     d.message,
	}
}

func golegacyRegistryRequest(r *SelfPkgRegistryRequest) GolegacyRegistryRequest {
	return GolegacyRegistryRequest{
		Method:        r.method,
		URL:           r.url,
		Accept:        r.accept,
		ContentType:   r.contentType,
		Authorization: r.authorization,
		Checksum:      r.checksum,
		Metadata:      r.metadata,
	}
}
