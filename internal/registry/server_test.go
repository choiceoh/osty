package registry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestServer wires a Storage + AllowAll authorizer + httptest
// server in three lines so each test can focus on the behavior it
// is asserting. Returns the client-facing URL and a cleanup func.
func newTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStorage(dir)
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	srv := &Server{Storage: store, Auth: AllowAll{}}
	hts := httptest.NewServer(srv)
	t.Cleanup(hts.Close)
	return hts, srv
}

// sha256Hex returns the sha256: prefixed hex digest of b — used by
// tests that need to predict what the server will compute.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hashPrefix + hex.EncodeToString(sum[:])
}

// TestRoundTripPublishThenFetch: the full flow. A client publishes
// a tarball, then retrieves the index, then downloads the tarball,
// and we check bytes match byte-for-byte.
func TestRoundTripPublishThenFetch(t *testing.T) {
	hts, _ := newTestServer(t)
	c := NewClient(hts.URL)
	c.Token = "dev"

	payload := []byte("fake tarball contents: hello, registry")
	err := c.Publish(context.Background(), PublishRequest{
		Name:     "hello",
		Version:  "1.0.0",
		Checksum: sha256Hex(payload),
		Tarball:  bytes.NewReader(payload),
		Metadata: PublishMetadata{
			Description: "a greeting lib",
			License:     "MIT",
			Keywords:    []string{"cli", "demo"},
		},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	vs, err := c.Versions(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(vs) != 1 || vs[0].Version != "1.0.0" {
		t.Fatalf("unexpected versions: %+v", vs)
	}
	if vs[0].Checksum != sha256Hex(payload) {
		t.Errorf("checksum: got %s", vs[0].Checksum)
	}

	dest := filepath.Join(t.TempDir(), "out.tgz")
	if err := c.DownloadTarball(context.Background(), "hello", "1.0.0", dest); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("tarball mismatch")
	}
}

// TestPublishRejectsDuplicate: the second publish of the same
// (name, version) must 409.
func TestPublishRejectsDuplicate(t *testing.T) {
	hts, _ := newTestServer(t)
	c := NewClient(hts.URL)
	c.Token = "dev"
	body := []byte("x")
	req := func() PublishRequest {
		return PublishRequest{
			Name: "foo", Version: "1.0.0",
			Checksum: sha256Hex(body),
			Tarball:  bytes.NewReader(body),
		}
	}
	if err := c.Publish(context.Background(), req()); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	err := c.Publish(context.Background(), req())
	if err == nil || !strings.Contains(err.Error(), "already published") {
		t.Errorf("expected duplicate-version error, got %v", err)
	}
}

// TestPublishRejectsBadChecksum: if the advertised checksum disagrees
// with the body, the server must reject *and* not persist anything.
func TestPublishRejectsBadChecksum(t *testing.T) {
	hts, srv := newTestServer(t)
	c := NewClient(hts.URL)
	c.Token = "dev"
	err := c.Publish(context.Background(), PublishRequest{
		Name: "foo", Version: "1.0.0",
		Checksum: "sha256:deadbeef",
		Tarball:  bytes.NewReader([]byte("real body")),
	})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum error, got %v", err)
	}
	// Nothing should have landed in the index.
	if _, err := srv.Storage.ReadIndex("foo"); !os.IsNotExist(err) {
		t.Errorf("expected no index entry, got err=%v", err)
	}
}

// TestPublishRequiresAuth: when Auth is nil or empty-token, PUTs
// return 401.
func TestPublishRequiresAuth(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStorage(dir)
	// No Auth set → every publish rejected.
	s := &Server{Storage: store}
	hts := httptest.NewServer(s)
	defer hts.Close()

	body := []byte("x")
	req, _ := http.NewRequest(http.MethodPut,
		hts.URL+"/v1/crates/foo/1.0.0", bytes.NewReader(body))
	req.Header.Set("Osty-Checksum", sha256Hex(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

// TestTokenAuthScopes: a token that is scoped to package "alpha"
// may publish alpha but not beta.
func TestTokenAuthScopes(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStorage(dir)
	ta := &TokenAuth{tokens: map[string]TokenRecord{}}
	ta.Add(TokenRecord{Token: "scoped", Packages: []string{"alpha"}})
	s := &Server{Storage: store, Auth: ta}
	hts := httptest.NewServer(s)
	defer hts.Close()

	mk := func(name string) *http.Request {
		body := []byte("x")
		req, _ := http.NewRequest(http.MethodPut,
			hts.URL+"/v1/crates/"+name+"/1.0.0", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer scoped")
		req.Header.Set("Osty-Checksum", sha256Hex(body))
		return req
	}
	r1, _ := http.DefaultClient.Do(mk("alpha"))
	if r1.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(r1.Body)
		t.Errorf("alpha: expected 201, got %d: %s", r1.StatusCode, body)
	}
	r1.Body.Close()
	r2, _ := http.DefaultClient.Do(mk("beta"))
	if r2.StatusCode != http.StatusForbidden {
		t.Errorf("beta: expected 403, got %d", r2.StatusCode)
	}
	r2.Body.Close()
}

// TestYankHidesFromIndex: publishing and then yanking a version
// should make it invisible to subsequent Versions() calls (client
// filters yanked).
func TestYankHidesFromIndex(t *testing.T) {
	hts, _ := newTestServer(t)
	c := NewClient(hts.URL)
	c.Token = "dev"
	body := []byte("v1")
	if err := c.Publish(context.Background(), PublishRequest{
		Name: "foo", Version: "1.0.0",
		Checksum: sha256Hex(body),
		Tarball:  bytes.NewReader(body),
	}); err != nil {
		t.Fatal(err)
	}
	// Yank via raw HTTP — the client doesn't expose a yank helper.
	req, _ := http.NewRequest(http.MethodPost,
		hts.URL+"/v1/crates/foo/1.0.0/yank", nil)
	req.Header.Set("Authorization", "Bearer dev")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("yank: %d", resp.StatusCode)
	}
	got, err := c.Versions(context.Background(), "foo")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected yanked version hidden, got %+v", got)
	}
}

// TestIndexMissingReturns404: never-published package is a 404.
func TestIndexMissingReturns404(t *testing.T) {
	hts, _ := newTestServer(t)
	resp, err := http.Get(hts.URL + "/v1/crates/nobody")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

// TestUploadTooLarge: MaxTarballBytes is respected and returns 413.
func TestUploadTooLarge(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStorage(dir)
	s := &Server{Storage: store, Auth: AllowAll{}, MaxTarballBytes: 8}
	hts := httptest.NewServer(s)
	defer hts.Close()
	body := []byte("too large body") // > 8 bytes
	req, _ := http.NewRequest(http.MethodPut,
		hts.URL+"/v1/crates/foo/1.0.0", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer t")
	req.Header.Set("Osty-Checksum", sha256Hex(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", resp.StatusCode)
	}
}

// TestHealthz: trivial liveness endpoint.
func TestHealthz(t *testing.T) {
	hts, _ := newTestServer(t)
	resp, err := http.Get(hts.URL + "/v1/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "ok" {
		t.Errorf("healthz: %d %q", resp.StatusCode, body)
	}
}

// TestStorageValidatesName: path traversal and empty names are
// rejected before touching the filesystem.
func TestStorageValidatesName(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStorage(dir)
	cases := []string{"", "..", "../evil", "with space", "a/b", "a\x00b"}
	for _, c := range cases {
		_, err := store.ReadIndex(c)
		if err == nil {
			t.Errorf("expected error for name %q", c)
		}
	}
}

// TestIndexSortsBySemver: storage ordering is semver-ascending even
// when publishes arrive out of order.
func TestIndexSortsBySemver(t *testing.T) {
	hts, _ := newTestServer(t)
	c := NewClient(hts.URL)
	c.Token = "dev"
	for _, v := range []string{"1.10.0", "1.2.0", "1.2.0-pre", "2.0.0"} {
		body := []byte(v)
		if err := c.Publish(context.Background(), PublishRequest{
			Name: "sv", Version: v,
			Checksum: sha256Hex(body),
			Tarball:  bytes.NewReader(body),
		}); err != nil {
			t.Fatalf("publish %s: %v", v, err)
		}
	}
	got, err := c.Versions(context.Background(), "sv")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1.2.0-pre", "1.2.0", "1.10.0", "2.0.0"}
	if len(got) != len(want) {
		t.Fatalf("len mismatch: %v", got)
	}
	for i, v := range got {
		if v.Version != want[i] {
			t.Errorf("pos %d: got %s want %s", i, v.Version, want[i])
		}
	}
}

// TestLoadTokenAuth exercises the JSON file path.
func TestLoadTokenAuth(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")
	os.WriteFile(path, []byte(`{"tokens":[
		{"token":"k1","subject":"alice","allow_all":true},
		{"token":"k2","packages":["x","y"]}
	]}`), 0o600)
	ta, err := LoadTokenAuth(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ta.Authorize("k1", "anything"); err != nil {
		t.Errorf("k1/anything: %v", err)
	}
	if err := ta.Authorize("k2", "x"); err != nil {
		t.Errorf("k2/x: %v", err)
	}
	if err := ta.Authorize("k2", "z"); err == nil {
		t.Errorf("k2/z: expected forbidden")
	}
	if err := ta.Authorize("nope", "x"); err == nil {
		t.Errorf("nope/x: expected unauthorized")
	}
}
