package selfhostcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRegistry serves a `(manifest, binary)` pair from in-memory
// state. Tests configure individual handlers via `set*` helpers so
// each scenario can model 200 / 404 / mismatch / truncated etc.
type fakeRegistry struct {
	t        *testing.T
	manifest map[string]Manifest
	binary   map[string][]byte
	hits     map[string]int
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	return &fakeRegistry{
		t:        t,
		manifest: map[string]Manifest{},
		binary:   map[string][]byte{},
		hits:     map[string]int{},
	}
}

func (r *fakeRegistry) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.hits[req.URL.Path]++
		// Manifest endpoint: /<key>.json.
		if strings.HasSuffix(req.URL.Path, ".json") {
			key := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/"), ".json")
			m, ok := r.manifest[key]
			if !ok {
				http.NotFound(w, req)
				return
			}
			body, err := json.Marshal(m)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
			return
		}
		// Binary endpoint: anything else.
		body, ok := r.binary[req.URL.Path]
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(body)
	})
}

func (r *fakeRegistry) publish(key Key, body []byte) Manifest {
	hash := sha256.Sum256(body)
	m := Manifest{
		Version:      ManifestVersion,
		Key:          key.String(),
		BinarySHA256: hex.EncodeToString(hash[:]),
		BinaryURL:    "/" + key.String() + "/osty-self",
	}
	r.manifest[key.String()] = m
	r.binary["/"+key.String()+"/osty-self"] = body
	return m
}

func TestHTTPFetcherFetchSuccess(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{"main.osty": "fn main() {}\n"})
	t.Setenv(SelfBinEnv, "")

	key, err := ComputeKey(root)
	if err != nil {
		t.Fatalf("ComputeKey: %v", err)
	}

	reg := newFakeRegistry(t)
	body := []byte("real osty-self body")
	reg.publish(key, body)
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	fetcher := HTTPFetcher{BaseURL: srv.URL}
	manifest, rdr, err := fetcher.Fetch(context.Background(), key)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer rdr.Close()
	got, err := io.ReadAll(rdr)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("body mismatch: %q vs %q", got, body)
	}
	if manifest.Key != key.String() {
		t.Fatalf("manifest key = %q, want %q", manifest.Key, key.String())
	}
	if manifest.Version != ManifestVersion {
		t.Fatalf("manifest version = %d, want %d", manifest.Version, ManifestVersion)
	}
}

func TestHTTPFetcherReturnsErrNotAvailableOn404(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{"main.osty": "fn main() {}\n"})
	t.Setenv(SelfBinEnv, "")
	key, _ := ComputeKey(root)

	reg := newFakeRegistry(t)
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	fetcher := HTTPFetcher{BaseURL: srv.URL}
	_, _, err := fetcher.Fetch(context.Background(), key)
	if !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("err = %v, want ErrNotAvailable", err)
	}
}

func TestHTTPFetcherRejectsManifestWithBadVersion(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{"main.osty": "fn main() {}\n"})
	t.Setenv(SelfBinEnv, "")
	key, _ := ComputeKey(root)

	reg := newFakeRegistry(t)
	bad := Manifest{
		Version:      99,
		Key:          key.String(),
		BinarySHA256: strings.Repeat("a", 64),
		BinaryURL:    "/" + key.String() + "/osty-self",
	}
	reg.manifest[key.String()] = bad
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	fetcher := HTTPFetcher{BaseURL: srv.URL}
	_, _, err := fetcher.Fetch(context.Background(), key)
	if err == nil {
		t.Fatal("expected error for unsupported version")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Fatalf("err = %v, want version error", err)
	}
}

func TestHTTPFetcherRejectsKeyMismatch(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{"main.osty": "fn main() {}\n"})
	t.Setenv(SelfBinEnv, "")
	key, _ := ComputeKey(root)

	reg := newFakeRegistry(t)
	bad := Manifest{
		Version:      ManifestVersion,
		Key:          "wrong-key",
		BinarySHA256: strings.Repeat("a", 64),
		BinaryURL:    "/foo",
	}
	reg.manifest[key.String()] = bad
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	fetcher := HTTPFetcher{BaseURL: srv.URL}
	_, _, err := fetcher.Fetch(context.Background(), key)
	if err == nil || !strings.Contains(err.Error(), "key") {
		t.Fatalf("err = %v, want key mismatch", err)
	}
}

func TestHTTPFetcherEmptyBaseURL(t *testing.T) {
	fetcher := HTTPFetcher{}
	_, _, err := fetcher.Fetch(context.Background(), Key{ToolchainSHA: "x", Triple: "y"})
	if !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("err = %v, want ErrNotAvailable for empty BaseURL", err)
	}
}

func TestFetchAndInstallVerifiesAndCaches(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{"main.osty": "fn main() {}\n"})
	t.Setenv(SelfBinEnv, "")
	key, _ := ComputeKey(root)

	reg := newFakeRegistry(t)
	body := []byte("verified osty-self bytes")
	reg.publish(key, body)
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	cached, err := FetchAndInstall(context.Background(), root, key, HTTPFetcher{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("FetchAndInstall: %v", err)
	}
	if cached != CachePath(root, key) {
		t.Fatalf("cached path = %q, want CachePath(root, key)", cached)
	}
	got, err := os.ReadFile(cached)
	if err != nil {
		t.Fatalf("read cached: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("cached body mismatch: %q vs %q", got, body)
	}

	// Subsequent ResolveBinary lookup should hit the cache.
	resolved, gotKey, err := ResolveBinary(root)
	if err != nil {
		t.Fatalf("ResolveBinary: %v", err)
	}
	if resolved != cached || gotKey != key {
		t.Fatalf("resolver miss after install: got %q / %v, want %q / %v", resolved, gotKey, cached, key)
	}
}

func TestFetchAndInstallRejectsSHAMismatch(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{"main.osty": "fn main() {}\n"})
	t.Setenv(SelfBinEnv, "")
	key, _ := ComputeKey(root)

	reg := newFakeRegistry(t)
	body := []byte("real bytes")
	m := reg.publish(key, body)
	// Tamper: replace the binary body with different bytes after
	// the manifest has captured the original hash.
	reg.binary["/"+key.String()+"/osty-self"] = []byte("tampered bytes")
	_ = m
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	_, err := FetchAndInstall(context.Background(), root, key, HTTPFetcher{BaseURL: srv.URL})
	if !errors.Is(err, ErrSHAMismatch) {
		t.Fatalf("err = %v, want ErrSHAMismatch", err)
	}
	// Verify nothing landed in the cache.
	if _, statErr := os.Stat(CachePath(root, key)); statErr == nil {
		t.Fatal("cache entry exists after SHA mismatch — must not have written")
	}
	// Also: tmp files must be cleaned up.
	cacheDir := filepath.Dir(CachePath(root, key))
	entries, _ := os.ReadDir(cacheDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".osty-self-fetch-") {
			t.Fatalf("leftover tmp file: %s", e.Name())
		}
	}
}

func TestFetchAndInstallNilFetcher(t *testing.T) {
	root := t.TempDir()
	_, err := FetchAndInstall(context.Background(), root, Key{ToolchainSHA: "x", Triple: "y"}, nil)
	if !errors.Is(err, ErrNotAvailable) {
		t.Fatalf("err = %v, want ErrNotAvailable for nil fetcher", err)
	}
}

func TestFetchAndInstallZeroKey(t *testing.T) {
	root := t.TempDir()
	reg := newFakeRegistry(t)
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()
	_, err := FetchAndInstall(context.Background(), root, Key{}, HTTPFetcher{BaseURL: srv.URL})
	if err == nil {
		t.Fatal("expected error for zero key")
	}
}

func TestEnvFetcherDisabledWhenAllEmpty(t *testing.T) {
	// No env URL, no default URL → nil (preserves the legacy
	// "no fetcher without explicit URL" contract for fork users
	// who blank out DefaultRegistryURL).
	t.Setenv(RegistryURLEnv, "")
	t.Setenv(RegistryOfflineEnv, "")
	if envFetcherWithDefault("") != nil {
		t.Fatal("expected nil when both env and default URLs are empty")
	}
}

func TestEnvFetcherUsesDefaultWhenEnvUnset(t *testing.T) {
	// The env var is unset but a non-empty default exists — fresh
	// clones must reach the upstream registry without the user
	// exporting OSTY_SELF_REGISTRY_URL by hand.
	t.Setenv(RegistryURLEnv, "")
	t.Setenv(RegistryOfflineEnv, "")
	f := envFetcherWithDefault("https://example.com/default")
	if f == nil {
		t.Fatal("expected fetcher to use the default URL when env is unset")
	}
	httpF, ok := f.(HTTPFetcher)
	if !ok {
		t.Fatalf("expected HTTPFetcher, got %T", f)
	}
	if httpF.BaseURL != "https://example.com/default" {
		t.Fatalf("BaseURL = %q, want default URL", httpF.BaseURL)
	}
}

func TestEnvFetcherHonoursOfflineMode(t *testing.T) {
	// Offline mode must override both an explicit env URL and any
	// configured default — air-gapped CI must never reach out.
	t.Setenv(RegistryURLEnv, "https://example.com/osty-self")
	t.Setenv(RegistryOfflineEnv, "1")
	if envFetcherWithDefault("https://example.com/default") != nil {
		t.Fatal("offline mode must disable the fetcher even with default set")
	}
}

func TestEnvFetcherEnvWinsOverDefault(t *testing.T) {
	// Explicit env var must override the default — fork users and
	// developers exercising staging registries rely on this.
	t.Setenv(RegistryURLEnv, "https://example.com/osty-self")
	t.Setenv(RegistryOfflineEnv, "")
	f := envFetcherWithDefault("https://example.com/default")
	if f == nil {
		t.Fatal("expected non-nil fetcher with URL set")
	}
	httpF, ok := f.(HTTPFetcher)
	if !ok {
		t.Fatalf("expected HTTPFetcher, got %T", f)
	}
	if httpF.BaseURL != "https://example.com/osty-self" {
		t.Fatalf("BaseURL = %q, want canonical URL (env wins over default)", httpF.BaseURL)
	}
}

func TestDefaultRegistryURLPointsAtUpstream(t *testing.T) {
	// Sanity-pin the const so a careless edit (e.g. accidentally
	// blanking the URL) lights up here before merging.
	const want = "https://github.com/choiceoh/osty/releases/download/osty-self-snapshots"
	if DefaultRegistryURL != want {
		t.Fatalf("DefaultRegistryURL = %q, want %q", DefaultRegistryURL, want)
	}
}

func TestResolveBinaryWithFetchHitsCacheBeforeNetwork(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{"main.osty": "fn main() {}\n"})
	t.Setenv(SelfBinEnv, "")

	key, _ := ComputeKey(root)
	cached := CachePath(root, key)
	if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cached, []byte("pre-cached bin"), 0o755); err != nil {
		t.Fatalf("write cached: %v", err)
	}

	reg := newFakeRegistry(t)
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	got, gotKey, err := ResolveBinaryWithFetch(context.Background(), root, HTTPFetcher{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("ResolveBinaryWithFetch: %v", err)
	}
	if got != cached {
		t.Fatalf("got = %q, want pre-cached", got)
	}
	if gotKey != key {
		t.Fatalf("key mismatch: %v vs %v", gotKey, key)
	}
	// Network must NOT have been touched.
	if total := totalRequestCount(reg); total != 0 {
		t.Fatalf("network was hit even though cache was warm; %d requests", total)
	}
}

func TestResolveBinaryWithFetchPullsFromNetwork(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{"main.osty": "fn main() {}\n"})
	t.Setenv(SelfBinEnv, "")

	key, _ := ComputeKey(root)
	reg := newFakeRegistry(t)
	body := []byte("network-fetched bin")
	reg.publish(key, body)
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	got, gotKey, err := ResolveBinaryWithFetch(context.Background(), root, HTTPFetcher{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("ResolveBinaryWithFetch: %v", err)
	}
	if got != CachePath(root, key) {
		t.Fatalf("got = %q, want cache path", got)
	}
	if gotKey != key {
		t.Fatalf("key mismatch")
	}
	bodyOnDisk, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("read cache entry: %v", err)
	}
	if string(bodyOnDisk) != string(body) {
		t.Fatalf("cache entry body mismatch")
	}
}

func TestResolveBinaryWithFetchFallsBackOnRegistryMiss(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{"main.osty": "fn main() {}\n"})
	t.Setenv(SelfBinEnv, "")

	reg := newFakeRegistry(t)
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	_, _, err := ResolveBinaryWithFetch(context.Background(), root, HTTPFetcher{BaseURL: srv.URL})
	if !errors.Is(err, ErrNotCached) {
		t.Fatalf("err = %v, want ErrNotCached when registry misses", err)
	}
}

func TestResolveBinaryWithFetchSurfacesSHAMismatch(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{"main.osty": "fn main() {}\n"})
	t.Setenv(SelfBinEnv, "")
	key, _ := ComputeKey(root)

	reg := newFakeRegistry(t)
	reg.publish(key, []byte("real"))
	reg.binary["/"+key.String()+"/osty-self"] = []byte("tampered")
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	_, _, err := ResolveBinaryWithFetch(context.Background(), root, HTTPFetcher{BaseURL: srv.URL})
	if !errors.Is(err, ErrSHAMismatch) {
		t.Fatalf("err = %v, want ErrSHAMismatch surfaced through resolver", err)
	}
}

func TestResolveBinaryWithFetchNilFetcherActsLikeResolveBinary(t *testing.T) {
	root := t.TempDir()
	scaffoldToolchain(t, root, map[string]string{"main.osty": "fn main() {}\n"})
	t.Setenv(SelfBinEnv, "")

	_, _, err := ResolveBinaryWithFetch(context.Background(), root, nil)
	if !errors.Is(err, ErrNotCached) {
		t.Fatalf("err = %v, want ErrNotCached with nil fetcher", err)
	}
}

func totalRequestCount(r *fakeRegistry) int {
	total := 0
	for _, n := range r.hits {
		total += n
	}
	return total
}
