package pkgmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/registry"
)

// fakeRegistry stands in for a real osty registry server. Each entry
// in `crates` maps a package name to its index entry; `tarballs`
// holds the bytes served for `/v1/crates/<name>/<version>/tar`. The
// helper builds the index entries, packages osty.toml stubs as tar
// gzs, and returns an httptest.Server ready for plugging into Env.
type fakeRegistry struct {
	t     *testing.T
	srv   *httptest.Server
	crate map[string]*registry.IndexEntry
	tar   map[string][]byte // key: "<name>/<version>"
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	fr := &fakeRegistry{
		t:     t,
		crate: map[string]*registry.IndexEntry{},
		tar:   map[string][]byte{},
	}
	fr.srv = httptest.NewServer(http.HandlerFunc(fr.handle))
	t.Cleanup(fr.srv.Close)
	return fr
}

// publish records a package version. depReqs is a list of "name@req"
// strings declaring the version's transitive deps; an empty list
// makes a leaf package.
func (fr *fakeRegistry) publish(name, version string, depReqs ...string) {
	fr.t.Helper()
	entry, ok := fr.crate[name]
	if !ok {
		entry = &registry.IndexEntry{Name: name}
		fr.crate[name] = entry
	}
	m := &manifest.Manifest{
		Package: manifest.Package{Name: name, Version: version},
	}
	v := registry.Version{Version: version, PublishedAt: time.Now().UTC()}
	for _, dr := range depReqs {
		depName, req, ok := strings.Cut(dr, "@")
		if !ok {
			fr.t.Fatalf("bad depReq %q", dr)
		}
		m.Dependencies = append(m.Dependencies, manifest.Dependency{
			Name: depName, PackageName: depName, VersionReq: req, DefaultFeats: true,
		})
		v.Dependencies = append(v.Dependencies, registry.VersionDependency{
			Name: depName, Req: req, Kind: "normal",
		})
	}
	pkgDir := fr.t.TempDir()
	if err := manifest.Write(filepath.Join(pkgDir, manifest.ManifestFile), m); err != nil {
		fr.t.Fatal(err)
	}
	// A trivial source file ensures the tar isn't empty.
	if err := os.WriteFile(filepath.Join(pkgDir, "lib.osty"),
		[]byte("// "+name+" "+version+"\n"), 0o644); err != nil {
		fr.t.Fatal(err)
	}
	var tarBuf bytes.Buffer
	if err := CreateTarGz(pkgDir, &tarBuf); err != nil {
		fr.t.Fatal(err)
	}
	v.Checksum = HashBytes(tarBuf.Bytes())
	entry.Versions = append(entry.Versions, v)
	fr.tar[name+"/"+version] = tarBuf.Bytes()
}

// handle dispatches the three endpoint shapes the resolver hits:
// index lookup, tarball download, and a 404 default.
func (fr *fakeRegistry) handle(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/crates/"), "/")
	if len(parts) == 1 {
		entry, ok := fr.crate[parts[0]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(entry)
		return
	}
	if len(parts) == 3 && parts[2] == "tar" {
		key := parts[0] + "/" + parts[1]
		body, ok := fr.tar[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
		return
	}
	http.NotFound(w, r)
}

// envFor builds a pkgmgr.Env that points at this registry as the
// default. proj is the project root the test wants to resolve under.
func (fr *fakeRegistry) envFor(proj string) *Env {
	return &Env{
		CacheDir:    filepath.Join(fr.t.TempDir(), "cache"),
		VendorDir:   filepath.Join(proj, ".osty", "deps"),
		ProjectRoot: proj,
		Registries:  map[string]string{"": fr.srv.URL},
	}
}

// TestDiamondUnifyPicksHighestSatisfying covers the canonical diamond
// case: root requires `B ^1.2`, root also requires path-dep `A` whose
// own osty.toml requires `B ^1.0`. The greedy first pass following
// the root's `B ^1.2` constraint should pick the highest 1.x. After
// the unification pass intersects {^1.2, ^1.0} = ^1.2, the pin must
// satisfy both — i.e. land on a 1.2.x or higher.
func TestDiamondUnifyPicksHighestSatisfying(t *testing.T) {
	fr := newFakeRegistry(t)
	fr.publish("B", "1.0.0")
	fr.publish("B", "1.1.0")
	fr.publish("B", "1.2.5")
	fr.publish("B", "1.3.0")

	tmp := t.TempDir()
	proj := filepath.Join(tmp, "app")
	libA := filepath.Join(tmp, "libA")
	for _, d := range []string{proj, libA} {
		must(t, os.MkdirAll(d, 0o755))
	}
	// libA depends on B ^1.0 — would normally pick 1.3.0.
	must(t, manifest.Write(filepath.Join(libA, manifest.ManifestFile),
		&manifest.Manifest{
			Package: manifest.Package{Name: "libA", Version: "0.1.0"},
			Dependencies: []manifest.Dependency{
				{Name: "B", PackageName: "B", VersionReq: "^1.0", DefaultFeats: true},
			},
		}))

	root := &manifest.Manifest{
		Package: manifest.Package{Name: "app", Version: "0.0.1"},
		Dependencies: []manifest.Dependency{
			{Name: "libA", Path: "../libA", DefaultFeats: true},
			{Name: "B", PackageName: "B", VersionReq: "^1.2", DefaultFeats: true},
		},
	}
	env := fr.envFor(proj)

	graph, err := Resolve(context.Background(), root, env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	bNode := graph.Nodes["B"]
	if bNode == nil {
		t.Fatalf("expected B node, got %+v", graph.Nodes)
	}
	// The intersection of ^1.0 and ^1.2 admits only 1.2.x and 1.3.x.
	// The picker chooses the highest, so 1.3.0.
	if bNode.Fetched.Version != "1.3.0" {
		t.Errorf("B version: got %q, want 1.3.0", bNode.Fetched.Version)
	}
}

// TestDiamondConflictUnsatisfiable: root needs `B ^2.0` and a path
// dep needs `B ^1.0` — no single major satisfies both, so the
// resolver must fail with a clear chain in the message.
func TestDiamondConflictUnsatisfiable(t *testing.T) {
	fr := newFakeRegistry(t)
	fr.publish("B", "1.0.0")
	fr.publish("B", "1.5.0")
	fr.publish("B", "2.0.0")

	tmp := t.TempDir()
	proj := filepath.Join(tmp, "app")
	libA := filepath.Join(tmp, "libA")
	for _, d := range []string{proj, libA} {
		must(t, os.MkdirAll(d, 0o755))
	}
	must(t, manifest.Write(filepath.Join(libA, manifest.ManifestFile),
		&manifest.Manifest{
			Package: manifest.Package{Name: "libA", Version: "0.1.0"},
			Dependencies: []manifest.Dependency{
				{Name: "B", PackageName: "B", VersionReq: "^1.0", DefaultFeats: true},
			},
		}))

	root := &manifest.Manifest{
		Package: manifest.Package{Name: "app", Version: "0.0.1"},
		Dependencies: []manifest.Dependency{
			{Name: "libA", Path: "../libA", DefaultFeats: true},
			{Name: "B", PackageName: "B", VersionReq: "^2.0", DefaultFeats: true},
		},
	}
	env := fr.envFor(proj)

	_, err := Resolve(context.Background(), root, env)
	if err == nil {
		t.Fatalf("expected diamond conflict error")
	}
	msg := err.Error()
	// The error must name the offending package, state the
	// unsatisfiability, AND surface the parent chain so the user
	// sees who placed each requirement.
	for _, want := range []string{`"B"`, "no published version satisfies", "required by", "<root>"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error missing %q substring: %v", want, err)
		}
	}
}

// TestSourceKindMismatchDiamond: one parent declares B as a path
// source, another as a registry source. The unifier can't reconcile
// these structurally — the resolver must error before any version
// math.
func TestSourceKindMismatchDiamond(t *testing.T) {
	fr := newFakeRegistry(t)
	fr.publish("B", "1.0.0")

	tmp := t.TempDir()
	proj := filepath.Join(tmp, "app")
	libA := filepath.Join(tmp, "libA")
	libB := filepath.Join(tmp, "libB")
	for _, d := range []string{proj, libA, libB} {
		must(t, os.MkdirAll(d, 0o755))
	}
	// libB is a real path-shaped package that libA pulls in via path.
	must(t, manifest.Write(filepath.Join(libB, manifest.ManifestFile),
		&manifest.Manifest{Package: manifest.Package{Name: "B", Version: "9.9.9"}}))
	must(t, manifest.Write(filepath.Join(libA, manifest.ManifestFile),
		&manifest.Manifest{
			Package: manifest.Package{Name: "libA", Version: "0.1.0"},
			Dependencies: []manifest.Dependency{
				{Name: "B", Path: "../libB", DefaultFeats: true},
			},
		}))

	root := &manifest.Manifest{
		Package: manifest.Package{Name: "app", Version: "0.0.1"},
		Dependencies: []manifest.Dependency{
			{Name: "libA", Path: "../libA", DefaultFeats: true},
			// Root pulls B from registry — incompatible with libA's path.
			{Name: "B", PackageName: "B", VersionReq: "^1.0", DefaultFeats: true},
		},
	}
	env := fr.envFor(proj)

	_, err := Resolve(context.Background(), root, env)
	if err == nil {
		t.Fatalf("expected source-kind mismatch error")
	}
	if !strings.Contains(err.Error(), "source kind mismatch") {
		t.Errorf("error should mention source kind: %v", err)
	}
}

// TestDiamondCompatibleRequirements: when both sides of the diamond
// already agree (root and lib both want `B ^1.0`), no upgrade is
// triggered and the greedy pick stands. Verifies the unifier doesn't
// thrash when constraints are consistent.
func TestDiamondCompatibleRequirements(t *testing.T) {
	fr := newFakeRegistry(t)
	fr.publish("B", "1.0.0")
	fr.publish("B", "1.5.0")

	tmp := t.TempDir()
	proj := filepath.Join(tmp, "app")
	libA := filepath.Join(tmp, "libA")
	for _, d := range []string{proj, libA} {
		must(t, os.MkdirAll(d, 0o755))
	}
	must(t, manifest.Write(filepath.Join(libA, manifest.ManifestFile),
		&manifest.Manifest{
			Package: manifest.Package{Name: "libA", Version: "0.1.0"},
			Dependencies: []manifest.Dependency{
				{Name: "B", PackageName: "B", VersionReq: "^1.0", DefaultFeats: true},
			},
		}))

	root := &manifest.Manifest{
		Package: manifest.Package{Name: "app", Version: "0.0.1"},
		Dependencies: []manifest.Dependency{
			{Name: "libA", Path: "../libA", DefaultFeats: true},
			{Name: "B", PackageName: "B", VersionReq: "^1.0", DefaultFeats: true},
		},
	}
	env := fr.envFor(proj)

	graph, err := Resolve(context.Background(), root, env)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if v := graph.Nodes["B"].Fetched.Version; v != "1.5.0" {
		t.Errorf("B version: got %q, want 1.5.0 (highest in ^1.0)", v)
	}
}
