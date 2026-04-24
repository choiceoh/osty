package cihost

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/format"
	"github.com/osty/osty/internal/lint"
	"github.com/osty/osty/internal/lockfile"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

type Manifest = manifest.Manifest
type ResolveWorkspace = resolve.Workspace
type ResolvePackage = resolve.Package
type PackageResult = resolve.PackageResult
type Diagnostic = diag.Diagnostic
type Time = time.Time

type LoadResult struct {
	Root      string
	Manifest  *manifest.Manifest
	Workspace *resolve.Workspace
	Packages  []*resolve.Package
	Results   []*resolve.PackageResult
	Error     string
}

type CheckResult struct {
	Note        string
	Diagnostics []*diag.Diagnostic
}

type ManifestCore struct {
	HasPackage  bool
	Name        string
	Version     string
	Edition     string
	License     string
	Description string
}

type DependencyCore struct {
	Name   string
	Path   string
	HasGit bool
	GitTag string
	GitRev string
}

type SnapshotReadResult struct {
	Snapshot Snapshot
	Error    string
}

const ciSkipDirective = "osty:ci-skip"

func KeepAlive() bool { return true }

func NowUTC() time.Time { return time.Now().UTC() }

func DiagnosticSeverity(d *diag.Diagnostic) string {
	if d == nil {
		return ""
	}
	return d.Severity.String()
}

func Synthetic(severity, code, msg string) *diag.Diagnostic {
	return synthetic(coreSeverity(severity), code, msg)
}

func EmptyDiagnostics() []*diag.Diagnostic { return []*diag.Diagnostic{} }

func EmptyPackages() []*resolve.Package { return []*resolve.Package{} }

func EmptyPackageResults() []*resolve.PackageResult { return []*resolve.PackageResult{} }

func LoadRunnerState(root string, preloaded *manifest.Manifest) LoadResult {
	r := &LoadResult{Root: root, Manifest: preloaded}
	if r.Manifest == nil {
		if mRoot, err := manifest.FindRoot(r.Root); err == nil {
			m, _, loadErr := manifest.LoadDir(mRoot)
			if loadErr != nil {
				r.Error = loadErr.Error()
				return *r
			}
			r.Manifest = m
			r.Root = mRoot
		}
	}

	if r.Manifest != nil && r.Manifest.Workspace != nil {
		return loadWorkspace(r, true)
	}
	if !hasDirectPackageSources(r.Root) && resolve.IsWorkspaceRoot(r.Root, "") {
		return loadWorkspace(r, false)
	}

	pkg, err := resolve.LoadPackageArenaFirst(r.Root)
	if err != nil {
		return *r
	}
	filterPackageCIFiles(pkg)
	r.Packages = []*resolve.Package{pkg}
	pr := resolve.ResolvePackage(pkg, resolve.NewPrelude())
	r.Results = []*resolve.PackageResult{pr}
	return *r
}

func loadWorkspace(r *LoadResult, byManifest bool) LoadResult {
	ws, err := resolve.NewWorkspace(r.Root)
	if err != nil {
		r.Error = err.Error()
		return *r
	}
	ws.Stdlib = stdlib.LoadCached()
	r.Workspace = ws

	seen := map[string]bool{}
	pkgPaths := []string{}
	add := func(member string) {
		if seen[member] {
			return
		}
		seen[member] = true
		if pkg, err := ws.LoadPackageArenaFirst(member); err == nil && pkg != nil {
			filterPackageCIFiles(pkg)
			r.Packages = append(r.Packages, pkg)
			pkgPaths = append(pkgPaths, member)
		}
	}
	if byManifest && r.Manifest != nil {
		if r.Manifest.HasPackage {
			add("")
		}
		if r.Manifest.Workspace != nil {
			for _, mem := range r.Manifest.Workspace.Members {
				add(mem)
			}
		}
	} else {
		for _, p := range resolve.WorkspacePackagePaths(r.Root) {
			add(p)
		}
	}

	allResults := ws.ResolveAll()
	for _, path := range pkgPaths {
		r.Results = append(r.Results, allResults[path])
	}
	return *r
}

func hasDirectPackageSources(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".osty") && !strings.HasSuffix(name, "_test.osty") {
			return true
		}
	}
	return false
}

func filterPackageCIFiles(pkg *resolve.Package) {
	if pkg == nil || len(pkg.Files) == 0 {
		return
	}
	files := pkg.Files[:0]
	for _, pf := range pkg.Files {
		if pf == nil || bytes.Contains(pf.Source, []byte(ciSkipDirective)) {
			continue
		}
		files = append(files, pf)
	}
	pkg.Files = files
}

func OstyFiles(root string, packages []*resolve.Package) []string {
	files := map[string]bool{}
	for _, pkg := range packages {
		for _, pf := range pkg.Files {
			if pf == nil || pf.Path == "" {
				continue
			}
			files[pf.Path] = true
		}
	}
	if len(files) == 0 && len(packages) == 0 {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d == nil {
				return nil
			}
			if d.IsDir() {
				switch d.Name() {
				case ".osty", ".git", "node_modules":
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) == ".osty" {
				src, err := os.ReadFile(path)
				if err != nil || !bytes.Contains(src, []byte(ciSkipDirective)) {
					files[path] = true
				}
			}
			return nil
		})
	}
	out := make([]string, 0, len(files))
	for p := range files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func CheckFormat(root string, files []string) CheckResult {
	c := &CheckResult{}
	if len(files) == 0 {
		c.Note = "no .osty files found"
		return *c
	}
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			c.Diagnostics = append(c.Diagnostics, synthetic(diag.Error, "CI001",
				fmt.Sprintf("%s: %v", prettyPath(root, path), err)))
			continue
		}
		out, fdiags, ferr := format.Source(src)
		if ferr != nil {
			c.Diagnostics = append(c.Diagnostics, fdiags...)
			c.Diagnostics = append(c.Diagnostics, synthetic(diag.Error, "CI002",
				fmt.Sprintf("%s: format: %v", prettyPath(root, path), ferr)))
			continue
		}
		if string(src) != string(out) {
			c.Diagnostics = append(c.Diagnostics, synthetic(diag.Error, "CI003",
				fmt.Sprintf("%s: not canonically formatted (run `osty fmt --write`)",
					prettyPath(root, path))))
		}
	}
	return *c
}

func CheckLint(m *manifest.Manifest, packages []*resolve.Package, results []*resolve.PackageResult) CheckResult {
	c := &CheckResult{}
	if len(packages) == 0 {
		c.Note = "no packages loaded"
		return *c
	}
	cfg := lintConfigFromManifest(m)
	opts := checkOpts()
	for i, pkg := range packages {
		var pr *resolve.PackageResult
		if i < len(results) {
			pr = results[i]
		}
		if pr == nil {
			pr = resolve.ResolvePackage(pkg, resolve.NewPrelude())
		}
		chk := check.Package(pkg, pr, opts)
		lr := lint.Package(pkg, pr, chk)
		if cfg != nil {
			lr = cfg.Apply(lr)
		}
		c.Diagnostics = append(c.Diagnostics, pr.Diags...)
		c.Diagnostics = append(c.Diagnostics, chk.Diags...)
		c.Diagnostics = append(c.Diagnostics, lr.Diags...)
	}
	return *c
}

func checkOpts() check.Opts {
	reg := stdlib.LoadCached()
	return check.Opts{Stdlib: reg, Primitives: reg.Primitives, ResultMethods: reg.ResultMethods}
}

func lintConfigFromManifest(m *manifest.Manifest) *lint.Config {
	if m == nil || m.Lint == nil {
		return nil
	}
	return &lint.Config{
		Allow:   m.Lint.Allow,
		Deny:    m.Lint.Deny,
		Exclude: m.Lint.Exclude,
	}
}

func ManifestCoreOf(m *manifest.Manifest) ManifestCore {
	if m == nil {
		return ManifestCore{}
	}
	p := m.Package
	return ManifestCore{
		HasPackage:  m.HasPackage,
		Name:        p.Name,
		Version:     p.Version,
		Edition:     p.Edition,
		License:     p.License,
		Description: p.Description,
	}
}

func WorkspaceMembers(m *manifest.Manifest) []string {
	if m == nil || m.Workspace == nil {
		return nil
	}
	return append([]string(nil), m.Workspace.Members...)
}

func HasWorkspace(m *manifest.Manifest) bool { return m != nil && m.Workspace != nil }

func HasManifest(m *manifest.Manifest) bool { return m != nil }

func DependencyCores(m *manifest.Manifest) []DependencyCore {
	if m == nil {
		return nil
	}
	all := append([]manifest.Dependency{}, m.Dependencies...)
	all = append(all, m.DevDependencies...)
	out := make([]DependencyCore, 0, len(all))
	for _, d := range all {
		core := DependencyCore{Name: d.Name, Path: d.Path}
		if d.Git != nil {
			core.HasGit = true
			core.GitTag = d.Git.Tag
			core.GitRev = d.Git.Rev
		}
		out = append(out, core)
	}
	return out
}

func DependencyCoreRows(m *manifest.Manifest) []string {
	cores := DependencyCores(m)
	out := make([]string, 0, len(cores))
	for _, d := range cores {
		hasGit := "false"
		if d.HasGit {
			hasGit = "true"
		}
		out = append(out, strings.Join([]string{d.Name, d.Path, hasGit, d.GitTag, d.GitRev}, "\t"))
	}
	return out
}

func HasDependencies(m *manifest.Manifest) bool {
	return m != nil && (len(m.Dependencies) > 0 || len(m.DevDependencies) > 0)
}

func CheckWorkspaceMemberPaths(root string, members []string) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	for _, mem := range members {
		if mem == "" || strings.HasPrefix(mem, "..") || strings.HasPrefix(mem, "/") {
			continue
		}
		path := filepath.Join(root, mem)
		info, err := os.Stat(path)
		if err != nil {
			out = append(out, synthetic(diag.Error, "CI112",
				fmt.Sprintf("workspace member %q does not exist at %s", mem, path)))
			continue
		}
		if !info.IsDir() {
			out = append(out, synthetic(diag.Error, "CI113",
				fmt.Sprintf("workspace member %q is not a directory", mem)))
		}
	}
	return out
}

func CheckPolicyFileSizes(root string, files []string, maxFileBytes int) []*diag.Diagnostic {
	if maxFileBytes <= 0 {
		return nil
	}
	var out []*diag.Diagnostic
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if info.Size() > int64(maxFileBytes) {
			out = append(out, synthetic(diag.Warning, "CI120",
				fmt.Sprintf("%s is %d bytes (limit %d)",
					prettyPath(root, path), info.Size(), maxFileBytes)))
		}
	}
	return out
}

func CheckLockfile(root string, m *manifest.Manifest) CheckResult {
	c := &CheckResult{}
	if m == nil {
		c.Note = "no osty.toml found; lockfile check skipped"
		return *c
	}
	if len(m.Dependencies) == 0 && len(m.DevDependencies) == 0 {
		c.Note = "no dependencies declared"
		return *c
	}
	lock, err := lockfile.Read(root)
	if err != nil {
		c.Diagnostics = append(c.Diagnostics, synthetic(diag.Error, "CI201",
			fmt.Sprintf("failed to read osty.lock: %v", err)))
		return *c
	}
	if lock == nil {
		c.Diagnostics = append(c.Diagnostics, synthetic(diag.Error, "CI202",
			"osty.lock is missing — run `osty build` to generate it"))
		return *c
	}
	if lock.Version > lockfile.SchemaVersion {
		c.Diagnostics = append(c.Diagnostics, synthetic(diag.Error, "CI203",
			fmt.Sprintf("osty.lock schema version %d is newer than this toolchain (max %d)",
				lock.Version, lockfile.SchemaVersion)))
	}
	all := append([]manifest.Dependency{}, m.Dependencies...)
	all = append(all, m.DevDependencies...)
	for _, d := range all {
		name := d.PackageName
		if name == "" {
			name = d.Name
		}
		if !lockHas(lock, name) {
			c.Diagnostics = append(c.Diagnostics, synthetic(diag.Error, "CI204",
				fmt.Sprintf("dependency %q declared in osty.toml is not pinned in osty.lock (run `osty update`)",
					name)))
		}
	}
	return *c
}

func CheckReleaseLockfile(root string, m *manifest.Manifest) []*diag.Diagnostic {
	if !HasDependencies(m) {
		return nil
	}
	lock, err := lockfile.Read(root)
	if err != nil {
		return []*diag.Diagnostic{synthetic(diag.Error, "CI308",
			fmt.Sprintf("failed to read osty.lock: %v", err))}
	}
	if lock == nil {
		return []*diag.Diagnostic{synthetic(diag.Error, "CI309",
			"osty.lock is required for release — run `osty build`")}
	}
	return nil
}

func lockHas(lock *lockfile.Lock, name string) bool {
	for _, p := range lock.Packages {
		if p.Name == name {
			return true
		}
	}
	return false
}

func ReadSnapshotHost(path string) SnapshotReadResult {
	s, err := ReadSnapshot(path)
	if err != nil {
		return SnapshotReadResult{Error: err.Error()}
	}
	return SnapshotReadResult{Snapshot: *s}
}

func WriteSnapshotHost(path string, s Snapshot) string {
	if err := WriteSnapshot(path, &s); err != nil {
		return err.Error()
	}
	return ""
}

func NewSingleSnapshotHost(pkg *resolve.Package, version, edition string) Snapshot {
	return *NewSingleSnapshot(pkg, version, edition)
}

func NewWorkspaceSnapshotHost(pkgs []*resolve.Package, version, edition string) Snapshot {
	return *NewWorkspaceSnapshot(pkgs, version, edition)
}

func CapturePackageHost(pkg *resolve.Package) []Symbol { return CapturePackage(pkg) }

func CompareSnapshots(baseline, current Snapshot) Diff {
	return Compare(&baseline, &current)
}

func synthetic(sev diag.Severity, code, msg string) *diag.Diagnostic {
	return diag.New(sev, msg).Code(code).Build()
}

func coreSeverity(sev string) diag.Severity {
	switch sev {
	case "error":
		return diag.Error
	case "warning":
		return diag.Warning
	default:
		return diag.Note
	}
}

func prettyPath(root, p string) string {
	if root == "" {
		return p
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "" {
		return p
	}
	return rel
}
