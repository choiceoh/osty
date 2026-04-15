package pkgmgr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/lockfile"
	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/resolve"
)

// TestResolvePathDep wires up a project with a single local-path
// dependency. The dependency's manifest is hand-written to a tmpdir;
// Resolve should walk it, fetch the dep, and produce a graph with one
// node.
func TestResolvePathDep(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "app")
	lib := filepath.Join(tmp, "lib")
	must(t, os.MkdirAll(proj, 0o755))
	must(t, os.MkdirAll(lib, 0o755))

	libManifest := &manifest.Manifest{
		Package: manifest.Package{Name: "lib", Version: "0.1.0"},
	}
	must(t, manifest.Write(filepath.Join(lib, manifest.ManifestFile), libManifest))
	// A tiny source file so the package looks real.
	must(t, os.WriteFile(filepath.Join(lib, "lib.osty"),
		[]byte("pub fn hi() -> String { \"hello\" }\n"), 0o644))

	appManifest := &manifest.Manifest{
		Package: manifest.Package{Name: "app", Version: "0.0.1"},
		Dependencies: []manifest.Dependency{
			{Name: "lib", Path: "../lib", DefaultFeats: true},
		},
	}
	env := &Env{
		CacheDir:    filepath.Join(tmp, "cache"),
		VendorDir:   filepath.Join(proj, ".osty", "deps"),
		ProjectRoot: proj,
		Registries:  map[string]string{},
	}
	graph, err := Resolve(context.Background(), appManifest, env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := len(graph.Nodes); got != 1 {
		t.Fatalf("nodes: got %d, want 1", got)
	}
	node := graph.Nodes["lib"]
	if node == nil || node.Fetched == nil {
		t.Fatalf("lib node missing: %+v", graph.Nodes)
	}
	if node.Fetched.Version != "0.1.0" {
		t.Errorf("version: got %q", node.Fetched.Version)
	}
	if node.Source.Kind() != SourcePath {
		t.Errorf("kind: got %v", node.Source.Kind())
	}
}

func TestResolveTransitivePathDepsAreRelativeToParentManifest(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "app")
	parent := filepath.Join(tmp, "deps", "parent")
	child := filepath.Join(tmp, "deps", "child")
	for _, d := range []string{proj, parent, child} {
		must(t, os.MkdirAll(d, 0o755))
	}
	must(t, manifest.Write(filepath.Join(child, manifest.ManifestFile),
		&manifest.Manifest{Package: manifest.Package{Name: "child", Version: "0.1.0"}}))
	must(t, manifest.Write(filepath.Join(parent, manifest.ManifestFile), &manifest.Manifest{
		Package: manifest.Package{Name: "parent", Version: "0.1.0"},
		Dependencies: []manifest.Dependency{
			{Name: "child", Path: "../child", DefaultFeats: true},
		},
	}))

	env := &Env{
		CacheDir:    filepath.Join(tmp, "cache"),
		VendorDir:   filepath.Join(proj, ".osty", "deps"),
		ProjectRoot: proj,
		Registries:  map[string]string{},
	}
	graph, err := Resolve(context.Background(), &manifest.Manifest{
		Package: manifest.Package{Name: "app", Version: "0.0.1"},
		Dependencies: []manifest.Dependency{
			{Name: "parent", Path: "../deps/parent", DefaultFeats: true},
		},
	}, env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := graph.Nodes["child"].Fetched.LocalDir; filepath.Clean(got) != filepath.Clean(child) {
		t.Fatalf("child LocalDir: got %q, want %q", got, child)
	}
}

func TestResolveRejectsConflictingTransitivePathAlias(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "app")
	libA := filepath.Join(tmp, "deps", "a")
	libB := filepath.Join(tmp, "deps", "b")
	utilA := filepath.Join(tmp, "deps", "util-a")
	utilB := filepath.Join(tmp, "deps", "util-b")
	for _, d := range []string{proj, libA, libB, utilA, utilB} {
		must(t, os.MkdirAll(d, 0o755))
	}
	must(t, manifest.Write(filepath.Join(utilA, manifest.ManifestFile),
		&manifest.Manifest{Package: manifest.Package{Name: "util", Version: "1.0.0"}}))
	must(t, manifest.Write(filepath.Join(utilB, manifest.ManifestFile),
		&manifest.Manifest{Package: manifest.Package{Name: "util", Version: "2.0.0"}}))
	must(t, manifest.Write(filepath.Join(libA, manifest.ManifestFile), &manifest.Manifest{
		Package: manifest.Package{Name: "a", Version: "0.1.0"},
		Dependencies: []manifest.Dependency{
			{Name: "util", Path: "../util-a", DefaultFeats: true},
		},
	}))
	must(t, manifest.Write(filepath.Join(libB, manifest.ManifestFile), &manifest.Manifest{
		Package: manifest.Package{Name: "b", Version: "0.1.0"},
		Dependencies: []manifest.Dependency{
			{Name: "util", Path: "../util-b", DefaultFeats: true},
		},
	}))

	env := &Env{
		CacheDir:    filepath.Join(tmp, "cache"),
		VendorDir:   filepath.Join(proj, ".osty", "deps"),
		ProjectRoot: proj,
		Registries:  map[string]string{},
	}
	_, err := Resolve(context.Background(), &manifest.Manifest{
		Package: manifest.Package{Name: "app", Version: "0.0.1"},
		Dependencies: []manifest.Dependency{
			{Name: "a", Path: "../deps/a", DefaultFeats: true},
			{Name: "b", Path: "../deps/b", DefaultFeats: true},
		},
	}, env)
	if err == nil {
		t.Fatal("Resolve should reject same alias pointing at different path deps")
	}
	if !strings.Contains(err.Error(), `dependency "util" resolved from`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestResolveTransitive confirms transitive path deps are followed
// and returned in topological order (leaves first).
func TestResolveTransitive(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "app")
	libA := filepath.Join(tmp, "packages", "libA")
	libB := filepath.Join(tmp, "packages", "libB")
	for _, d := range []string{proj, libA, libB} {
		must(t, os.MkdirAll(d, 0o755))
	}
	bMani := &manifest.Manifest{
		Package: manifest.Package{Name: "libB", Version: "1.0.0"},
	}
	must(t, manifest.Write(filepath.Join(libB, manifest.ManifestFile), bMani))
	aMani := &manifest.Manifest{
		Package: manifest.Package{Name: "libA", Version: "1.0.0"},
		Dependencies: []manifest.Dependency{
			{Name: "libB", Path: "../libB", DefaultFeats: true},
		},
	}
	must(t, manifest.Write(filepath.Join(libA, manifest.ManifestFile), aMani))
	appMani := &manifest.Manifest{
		Package: manifest.Package{Name: "app", Version: "0.0.1"},
		Dependencies: []manifest.Dependency{
			{Name: "libA", Path: "../packages/libA", DefaultFeats: true},
		},
	}
	env := &Env{
		CacheDir:    filepath.Join(tmp, "cache"),
		VendorDir:   filepath.Join(proj, ".osty", "deps"),
		ProjectRoot: proj,
		Registries:  map[string]string{},
	}
	graph, err := Resolve(context.Background(), appMani, env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("nodes: got %d, want 2", len(graph.Nodes))
	}
	// Order must have libB (leaf) before libA (parent).
	order := graph.Order
	idxA, idxB := -1, -1
	for i, n := range order {
		if n == "libA" {
			idxA = i
		}
		if n == "libB" {
			idxB = i
		}
	}
	if idxA < 0 || idxB < 0 {
		t.Fatalf("missing order entries: %v", order)
	}
	if idxB >= idxA {
		t.Errorf("libB should come before libA; got %v", order)
	}
}

// TestResolveUnknownDep propagates Fetch errors from a path source
// whose directory doesn't exist.
func TestResolveUnknownDep(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "app")
	must(t, os.MkdirAll(proj, 0o755))
	appMani := &manifest.Manifest{
		Package: manifest.Package{Name: "app", Version: "0.0.1"},
		Dependencies: []manifest.Dependency{
			{Name: "ghost", Path: "../no-such-dir", DefaultFeats: true},
		},
	}
	env := &Env{
		CacheDir:    filepath.Join(tmp, "cache"),
		VendorDir:   filepath.Join(proj, ".osty", "deps"),
		ProjectRoot: proj,
		Registries:  map[string]string{},
	}
	_, err := Resolve(context.Background(), appMani, env)
	if err == nil {
		t.Fatalf("Resolve should fail for missing path dep")
	}
}

// TestVendorCreatesSymlinks walks a resolved graph and confirms each
// node materializes as a symlink under env.VendorDir.
func TestVendorCreatesSymlinks(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "app")
	lib := filepath.Join(tmp, "lib")
	for _, d := range []string{proj, lib} {
		must(t, os.MkdirAll(d, 0o755))
	}
	must(t, manifest.Write(filepath.Join(lib, manifest.ManifestFile),
		&manifest.Manifest{Package: manifest.Package{Name: "lib", Version: "0.1.0"}}))

	env := &Env{
		CacheDir:    filepath.Join(tmp, "cache"),
		VendorDir:   filepath.Join(proj, ".osty", "deps"),
		ProjectRoot: proj,
		Registries:  map[string]string{},
	}
	graph, err := Resolve(context.Background(), &manifest.Manifest{
		Package: manifest.Package{Name: "app", Version: "0.0.1"},
		Dependencies: []manifest.Dependency{
			{Name: "lib", Path: "../lib", DefaultFeats: true},
		},
	}, env)
	must(t, err)
	must(t, Vendor(graph, env))

	link := filepath.Join(env.VendorDir, "lib")
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("vendor missing: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("vendored lib is not a symlink (mode=%v)", info.Mode())
	}
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if absTarget := filepath.Clean(target); absTarget != filepath.Clean(lib) {
		t.Errorf("target: got %q, want %q", target, lib)
	}
}

// TestLockFromGraph builds a lockfile from a resolved graph and spot-
// checks the key fields (name, version, source URI).
func TestLockFromGraph(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "app")
	lib := filepath.Join(tmp, "lib")
	for _, d := range []string{proj, lib} {
		must(t, os.MkdirAll(d, 0o755))
	}
	must(t, manifest.Write(filepath.Join(lib, manifest.ManifestFile),
		&manifest.Manifest{Package: manifest.Package{Name: "lib", Version: "0.1.0"}}))

	env := &Env{
		CacheDir:    filepath.Join(tmp, "cache"),
		VendorDir:   filepath.Join(proj, ".osty", "deps"),
		ProjectRoot: proj,
		Registries:  map[string]string{},
	}
	graph, err := Resolve(context.Background(), &manifest.Manifest{
		Package: manifest.Package{Name: "app", Version: "0.0.1"},
		Dependencies: []manifest.Dependency{
			{Name: "lib", Path: "../lib", DefaultFeats: true},
		},
	}, env)
	must(t, err)
	lock := LockFromGraph(graph)
	if lock == nil || len(lock.Packages) != 1 {
		t.Fatalf("lock: %+v", lock)
	}
	p := lock.Packages[0]
	if p.Name != "lib" || p.Version != "0.1.0" || p.Source != "path+../lib" {
		t.Errorf("pkg: %+v", p)
	}
	// Path sources have empty checksum.
	if p.Checksum != "" {
		t.Errorf("path dep should have empty checksum, got %q", p.Checksum)
	}
}

// TestVendorFallsBackToCopy injects a symlink function that fails
// with the platform-shaped "unsupported" error and confirms the
// dependency lands as a real directory tree rather than aborting
// the build.
func TestVendorFallsBackToCopy(t *testing.T) {
	tmp := t.TempDir()
	proj := filepath.Join(tmp, "app")
	lib := filepath.Join(tmp, "lib")
	for _, d := range []string{proj, lib} {
		must(t, os.MkdirAll(d, 0o755))
	}
	must(t, manifest.Write(filepath.Join(lib, manifest.ManifestFile),
		&manifest.Manifest{Package: manifest.Package{Name: "lib", Version: "0.1.0"}}))
	must(t, os.WriteFile(filepath.Join(lib, "lib.osty"),
		[]byte("pub fn hi() -> String { \"hello\" }\n"), 0o644))

	// Force the symlink op to look "unsupported" so Vendor falls
	// through to copyVendorDir.
	prev := symlinkFunc
	symlinkFunc = func(oldname, newname string) error { return errUnsupported }
	t.Cleanup(func() { symlinkFunc = prev })

	env := &Env{
		CacheDir:    filepath.Join(tmp, "cache"),
		VendorDir:   filepath.Join(proj, ".osty", "deps"),
		ProjectRoot: proj,
		Registries:  map[string]string{},
	}
	graph, err := Resolve(context.Background(), &manifest.Manifest{
		Package: manifest.Package{Name: "app", Version: "0.0.1"},
		Dependencies: []manifest.Dependency{
			{Name: "lib", Path: "../lib", DefaultFeats: true},
		},
	}, env)
	must(t, err)
	must(t, Vendor(graph, env))

	dst := filepath.Join(env.VendorDir, "lib")
	info, err := os.Lstat(dst)
	if err != nil {
		t.Fatalf("vendored copy missing: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected real directory copy, got symlink (mode=%v)", info.Mode())
	}
	if !info.IsDir() {
		t.Fatalf("expected directory, got %v", info.Mode())
	}
	if _, err := os.Stat(filepath.Join(dst, "lib.osty")); err != nil {
		t.Errorf("copy missing source file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, manifest.ManifestFile)); err != nil {
		t.Errorf("copy missing manifest: %v", err)
	}
}

// TestApplyLockPinNarrowsRegistryReq covers the lockfile-honoring
// branch in resolveDep: when osty.lock pins a registry dep at a
// version that still matches the manifest req, we mutate the source's
// versionReq to the exact pinned version. Path / git deps are
// untouched.
func TestApplyLockPinNarrowsRegistryReq(t *testing.T) {
	rs := &registrySource{name: "x", packageName: "x", versionReq: "^1.0.0"}
	dep := manifest.Dependency{Name: "x", PackageName: "x", VersionReq: "^1.0.0"}
	lock := &lockfile.Lock{
		Version: lockfile.SchemaVersion,
		Packages: []lockfile.Package{
			{Name: "x", Version: "1.2.3", Source: "registry+default"},
		},
	}
	applyLockPin(rs, dep, lock)
	if rs.versionReq != "=1.2.3" {
		t.Errorf("versionReq: got %q, want =1.2.3", rs.versionReq)
	}
}

// TestApplyLockPinIgnoresMismatchedReq: a lockfile pin that no longer
// satisfies the manifest's requirement (because the user edited the
// req) must not be honored — the resolver needs to pick a fresh
// matching version.
func TestApplyLockPinIgnoresMismatchedReq(t *testing.T) {
	rs := &registrySource{name: "x", packageName: "x", versionReq: "^2.0.0"}
	dep := manifest.Dependency{Name: "x", PackageName: "x", VersionReq: "^2.0.0"}
	lock := &lockfile.Lock{
		Version: lockfile.SchemaVersion,
		Packages: []lockfile.Package{
			{Name: "x", Version: "1.2.3", Source: "registry+default"},
		},
	}
	applyLockPin(rs, dep, lock)
	if rs.versionReq != "^2.0.0" {
		t.Errorf("versionReq: got %q, want ^2.0.0 (unchanged)", rs.versionReq)
	}
}

func TestApplyLockPinIgnoresMismatchedSource(t *testing.T) {
	rs := &registrySource{name: "x", packageName: "x", versionReq: "^1.0.0"}
	dep := manifest.Dependency{Name: "x", PackageName: "x", VersionReq: "^1.0.0"}
	lock := &lockfile.Lock{
		Version: lockfile.SchemaVersion,
		Packages: []lockfile.Package{
			{Name: "x", Version: "1.2.3", Source: "path+../x"},
		},
	}
	applyLockPin(rs, dep, lock)
	if rs.versionReq != "^1.0.0" {
		t.Errorf("versionReq: got %q, want ^1.0.0 (unchanged)", rs.versionReq)
	}
}

// TestApplyLockPinSkipsPathSources: path / git sources don't get
// rewritten; their identity comes from the manifest, not the lock.
func TestApplyLockPinSkipsPathSources(t *testing.T) {
	ps := &pathSource{name: "lib", path: "../lib"}
	dep := manifest.Dependency{Name: "lib", Path: "../lib"}
	lock := &lockfile.Lock{
		Version: lockfile.SchemaVersion,
		Packages: []lockfile.Package{
			{Name: "lib", Version: "9.9.9", Source: "path+../lib"},
		},
	}
	applyLockPin(ps, dep, lock) // must not panic; ps has no versionReq
	if ps.path != "../lib" {
		t.Errorf("pathSource mutated: %+v", ps)
	}
}

// TestDiffLockReportsAddRemoveChange exercises the four kinds of
// changes DiffLock needs to recognize for the --locked CI guard:
// added, removed, version-changed, and checksum-changed entries.
func TestDiffLockReportsAddRemoveChange(t *testing.T) {
	old := &lockfile.Lock{
		Version: lockfile.SchemaVersion,
		Packages: []lockfile.Package{
			{Name: "a", Version: "1.0.0", Source: "registry+default", Checksum: "sha256:aaaa"},
			{Name: "b", Version: "1.0.0", Source: "registry+default", Checksum: "sha256:bbbb"},
			{Name: "c", Version: "1.0.0", Source: "registry+default", Checksum: "sha256:cccc"},
		},
	}
	new := &lockfile.Lock{
		Version: lockfile.SchemaVersion,
		Packages: []lockfile.Package{
			{Name: "a", Version: "1.0.0", Source: "registry+default", Checksum: "sha256:aaaa"}, // unchanged
			{Name: "b", Version: "1.1.0", Source: "registry+default", Checksum: "sha256:bbbb"}, // version bump
			{Name: "c", Version: "1.0.0", Source: "registry+default", Checksum: "sha256:dddd"}, // checksum drift
			{Name: "d", Version: "0.1.0", Source: "registry+default", Checksum: "sha256:dddd"}, // added
		},
	}
	changes := DiffLock(old, new)
	if len(changes) != 3 {
		t.Fatalf("expected 3 changes, got %d: %+v", len(changes), changes)
	}
	kinds := map[string]string{}
	for _, c := range changes {
		kinds[c.Name] = c.Kind
	}
	if kinds["b"] != "version" {
		t.Errorf("b kind: %v", kinds["b"])
	}
	if kinds["c"] != "checksum" {
		t.Errorf("c kind: %v", kinds["c"])
	}
	if kinds["d"] != "added" {
		t.Errorf("d kind: %v", kinds["d"])
	}
	// Removed packages should also be reported.
	old2 := &lockfile.Lock{Packages: []lockfile.Package{{Name: "a", Version: "1.0.0"}}}
	new2 := &lockfile.Lock{}
	if changes := DiffLock(old2, new2); len(changes) != 1 || changes[0].Kind != "removed" {
		t.Errorf("removed: %+v", changes)
	}
}

// TestDiffLockNilInputs: nil inputs should mean "everything added"
// or "everything removed" — used when the project is brand new or
// the lockfile has been deleted.
func TestDiffLockNilInputs(t *testing.T) {
	new := &lockfile.Lock{Packages: []lockfile.Package{{Name: "a", Version: "1"}}}
	if changes := DiffLock(nil, new); len(changes) != 1 || changes[0].Kind != "added" {
		t.Errorf("nil-old: %+v", changes)
	}
	if changes := DiffLock(new, nil); len(changes) != 1 || changes[0].Kind != "removed" {
		t.Errorf("nil-new: %+v", changes)
	}
	if changes := DiffLock(nil, nil); len(changes) != 0 {
		t.Errorf("nil-both: %+v", changes)
	}
}

func TestDepProviderFindsTransitiveGraphAlias(t *testing.T) {
	tmp := t.TempDir()
	env := &Env{VendorDir: filepath.Join(tmp, ".osty", "deps")}
	graph := &Graph{
		Nodes: map[string]*ResolvedNode{
			"leaf": &ResolvedNode{Name: "leaf"},
		},
		Order: []string{"leaf"},
	}
	provider := NewDepProvider(&manifest.Manifest{}, graph, env)
	dir, ok := provider.LookupDep("leaf")
	if !ok {
		t.Fatal("LookupDep should find transitive graph alias")
	}
	if want := filepath.Join(env.VendorDir, "leaf"); dir != want {
		t.Fatalf("dir: got %q, want %q", dir, want)
	}
}

func TestDepProviderFindsTransitiveGraphGitURL(t *testing.T) {
	tmp := t.TempDir()
	env := &Env{VendorDir: filepath.Join(tmp, ".osty", "deps")}
	graph := &Graph{
		Nodes: map[string]*ResolvedNode{
			"json": &ResolvedNode{
				Name:   "json",
				Source: &gitSource{name: "json", url: "https://github.com/acme/json.git"},
			},
		},
		Order: []string{"json"},
	}
	provider := NewDepProvider(&manifest.Manifest{}, graph, env)
	dir, ok := provider.LookupDep("github.com/acme/json")
	if !ok {
		t.Fatal("LookupDep should find transitive graph git URL")
	}
	if want := filepath.Join(env.VendorDir, "json"); dir != want {
		t.Fatalf("dir: got %q, want %q", dir, want)
	}
}

func TestWorkspaceCanImportTransitiveVendoredDependency(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "app")
	vendor := filepath.Join(root, ".osty", "deps")
	parentDir := filepath.Join(vendor, "parent")
	leafDir := filepath.Join(vendor, "leaf")
	for _, d := range []string{root, parentDir, leafDir} {
		must(t, os.MkdirAll(d, 0o755))
	}
	must(t, os.WriteFile(filepath.Join(root, "main.osty"), []byte(`
use parent

fn main() {
    parent.ping()
}
`), 0o644))
	must(t, os.WriteFile(filepath.Join(parentDir, "parent.osty"), []byte(`
use leaf

pub fn ping() {
    leaf.pong()
}
`), 0o644))
	must(t, os.WriteFile(filepath.Join(leafDir, "leaf.osty"), []byte(`
pub fn pong() {}
`), 0o644))

	m := &manifest.Manifest{
		Package: manifest.Package{Name: "app", Version: "0.0.1"},
		Dependencies: []manifest.Dependency{
			{Name: "parent", Path: "../parent", DefaultFeats: true},
		},
	}
	graph := &Graph{
		Nodes: map[string]*ResolvedNode{
			"parent": &ResolvedNode{Name: "parent"},
			"leaf":   &ResolvedNode{Name: "leaf"},
		},
		Order: []string{"leaf", "parent"},
	}
	env := &Env{VendorDir: vendor}
	ws, err := resolve.NewWorkspace(root)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	ws.Deps = NewDepProvider(m, graph, env)
	if _, err := ws.LoadPackage(""); err != nil {
		t.Fatalf("LoadPackage: %v", err)
	}
	results := ws.ResolveAll()
	for key, res := range results {
		for _, d := range res.Diags {
			if d.Severity == diag.Error {
				t.Fatalf("%s: unexpected diagnostic: %s", key, d.Message)
			}
		}
	}
	if _, ok := ws.Packages["leaf"]; !ok {
		t.Fatalf("transitive leaf package was not loaded: %v", ws.Packages)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
