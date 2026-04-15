package pkgmgr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/registry"
)

func TestRegistryFetchOfflineUsesCachedIndexAndTarball(t *testing.T) {
	tmp := t.TempDir()
	cacheDir := filepath.Join(tmp, "cache")

	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "offline fetch must not contact registry", http.StatusInternalServerError)
	}))
	defer server.Close()

	checksum := seedRegistryTarball(t, cacheDir, server.URL, "lib", "1.2.3")
	seedRegistryIndex(t, cacheDir, server.URL, "lib", registry.Version{
		Version:  "1.2.3",
		Checksum: checksum,
	})

	src := &registrySource{name: "lib", packageName: "lib", versionReq: "^1.0.0"}
	env := &Env{
		CacheDir:   cacheDir,
		Registries: map[string]string{"": server.URL},
		Offline:    true,
	}

	fetched, err := src.Fetch(context.Background(), env)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("offline fetch contacted registry %d time(s)", got)
	}
	if fetched.Version != "1.2.3" {
		t.Fatalf("version: got %q, want 1.2.3", fetched.Version)
	}
	if fetched.Checksum != checksum {
		t.Fatalf("checksum: got %q, want %q", fetched.Checksum, checksum)
	}
	if fetched.Manifest.Package.Name != "lib" {
		t.Fatalf("manifest package: got %q, want lib", fetched.Manifest.Package.Name)
	}
	if _, err := os.Stat(filepath.Join(fetched.LocalDir, manifest.ManifestFile)); err != nil {
		t.Fatalf("unpacked manifest missing: %v", err)
	}
}

func TestRegistryFetchOfflineFailsWhenTarballCacheIsMissing(t *testing.T) {
	tmp := t.TempDir()
	cacheDir := filepath.Join(tmp, "cache")

	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "offline fetch must not contact registry", http.StatusInternalServerError)
	}))
	defer server.Close()

	seedRegistryIndex(t, cacheDir, server.URL, "lib", registry.Version{
		Version:  "1.2.3",
		Checksum: HashBytes([]byte("not-present")),
	})

	src := &registrySource{name: "lib", packageName: "lib", versionReq: "^1.0.0"}
	env := &Env{
		CacheDir:   cacheDir,
		Registries: map[string]string{"": server.URL},
		Offline:    true,
	}

	_, err := src.Fetch(context.Background(), env)
	if err == nil {
		t.Fatal("Fetch should fail without a cached tarball")
	}
	if !strings.Contains(err.Error(), "offline mode requires cached registry tarball") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("offline fetch contacted registry %d time(s)", got)
	}
}

func seedRegistryIndex(t *testing.T, cacheDir, registryURL, name string, version registry.Version) {
	t.Helper()

	indexCache := registry.NewDirIndexCache(filepath.Join(cacheDir, "registry-index", sanitizeURL(registryURL)))
	must(t, indexCache.Store(name, &registry.IndexEntry{
		Name:     name,
		Versions: []registry.Version{version},
	}, "test-etag"))
}

func seedRegistryTarball(t *testing.T, cacheDir, registryURL, name, version string) string {
	t.Helper()

	srcDir := filepath.Join(t.TempDir(), name)
	must(t, os.MkdirAll(srcDir, 0o755))
	must(t, manifest.Write(filepath.Join(srcDir, manifest.ManifestFile), &manifest.Manifest{
		Package: manifest.Package{Name: name, Version: version},
	}))
	must(t, os.WriteFile(filepath.Join(srcDir, name+".osty"), []byte("pub fn ping() {}\n"), 0o644))

	cacheRoot := filepath.Join(cacheDir, "registry", sanitizeURL(registryURL), name, version)
	must(t, os.MkdirAll(cacheRoot, 0o755))
	tarPath := filepath.Join(cacheRoot, "package.tgz")
	f, err := os.Create(tarPath)
	must(t, err)
	if err := CreateTarGz(srcDir, f); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	must(t, f.Close())
	checksum, err := hashFile(tarPath)
	must(t, err)
	return checksum
}
