package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/registry"
)

// newTestServer wires a Server on top of a temp-dir storage and a
// single-token auth table. Returns the httptest.Server; tests call
// srv.URL with the existing registry client.
func newTestServer(t *testing.T, token string) (*httptest.Server, *Storage) {
	t.Helper()
	dir := t.TempDir()
	st, err := NewStorage(dir)
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}
	tokens := NewTokenDB([]Token{
		{Token: token, Owner: "test", Scopes: []string{"publish:*"}},
	})
	// Silent logger: tests don't care about access log output and we
	// don't want them to spam go test -v.
	s := New(Config{
		Storage: st,
		Tokens:  tokens,
		Logger:  nopLogger(t),
	})
	return httptest.NewServer(s), st
}

// makeTarball builds a .tgz containing only osty.toml with the given
// manifest body. Mirrors the archive shape pkgmgr.CreateTarGz
// produces for a real osty package.
func makeTarball(t *testing.T, manifestBody string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: "osty.toml",
		Mode: 0o644,
		Size: int64(len(manifestBody)),
	}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if _, err := tw.Write([]byte(manifestBody)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tw.Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz.Close: %v", err)
	}
	return buf.Bytes()
}

const sampleManifest = `
[package]
name = "hello"
version = "1.2.3"
edition = "0.3"

[dependencies]
json = "^0.3"
`

// TestPublishThenFetchRoundTrip drives the full publish → index →
// download loop through the official client. This is the integration
// test the whole package exists for.
func TestPublishThenFetchRoundTrip(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	defer srv.Close()

	tarball := makeTarball(t, sampleManifest)
	sum := sha256.Sum256(tarball)
	checksum := "sha256:" + hex.EncodeToString(sum[:])

	c := registry.NewClient(srv.URL)
	c.Token = "secret"
	err := c.Publish(context.Background(), registry.PublishRequest{
		Name:     "hello",
		Version:  "1.2.3",
		Checksum: checksum,
		Tarball:  bytes.NewReader(tarball),
		Metadata: registry.PublishMetadata{Description: "hi", License: "MIT"},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Index should list the new version with the deps the server
	// extracted from osty.toml.
	vs, err := c.Versions(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(vs) != 1 {
		t.Fatalf("want 1 version, got %d", len(vs))
	}
	if vs[0].Version != "1.2.3" {
		t.Errorf("version: got %q, want %q", vs[0].Version, "1.2.3")
	}
	if vs[0].Checksum != checksum {
		t.Errorf("checksum: got %q, want %q", vs[0].Checksum, checksum)
	}
	if len(vs[0].Dependencies) != 1 || vs[0].Dependencies[0].Name != "json" {
		t.Errorf("dependencies: got %+v", vs[0].Dependencies)
	}

	// Download should yield the exact bytes we published.
	dir := t.TempDir()
	dest := filepath.Join(dir, "pkg.tgz")
	if err := c.DownloadTarball(context.Background(), "hello", "1.2.3", dest); err != nil {
		t.Fatalf("DownloadTarball: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, tarball) {
		t.Errorf("downloaded bytes differ from published bytes")
	}
}

// TestPublishRejectsBadToken ensures an unknown token → 401 and a
// known token with the wrong scope → 403.
func TestPublishRejectsBadToken(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	defer srv.Close()
	tarball := makeTarball(t, sampleManifest)

	c := registry.NewClient(srv.URL)
	c.Token = "nope"
	err := c.Publish(context.Background(), registry.PublishRequest{
		Name: "hello", Version: "1.2.3",
		Tarball: bytes.NewReader(tarball),
	})
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("want 401, got %v", err)
	}
}

// TestPublishRejectsChecksumMismatch — if the client claims a
// checksum the server didn't compute, reject with 400.
func TestPublishRejectsChecksumMismatch(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	defer srv.Close()
	tarball := makeTarball(t, sampleManifest)

	c := registry.NewClient(srv.URL)
	c.Token = "secret"
	err := c.Publish(context.Background(), registry.PublishRequest{
		Name: "hello", Version: "1.2.3",
		Checksum: "sha256:deadbeef",
		Tarball:  bytes.NewReader(tarball),
	})
	if err == nil || !strings.Contains(err.Error(), "Osty-Checksum mismatch") {
		t.Errorf("want mismatch error, got %v", err)
	}
}

// TestPublishRejectsNameMismatch — manifest name must match URL.
func TestPublishRejectsNameMismatch(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	defer srv.Close()
	tarball := makeTarball(t, sampleManifest) // package.name = "hello"

	c := registry.NewClient(srv.URL)
	c.Token = "secret"
	sum := sha256.Sum256(tarball)
	err := c.Publish(context.Background(), registry.PublishRequest{
		Name: "world", Version: "1.2.3",
		Checksum: "sha256:" + hex.EncodeToString(sum[:]),
		Tarball:  bytes.NewReader(tarball),
	})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("want name-mismatch error, got %v", err)
	}
}

// TestPublishDuplicateVersion — a second publish of the same
// (name,version) → 409.
func TestPublishDuplicateVersion(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	defer srv.Close()
	tarball := makeTarball(t, sampleManifest)
	sum := sha256.Sum256(tarball)
	checksum := "sha256:" + hex.EncodeToString(sum[:])

	c := registry.NewClient(srv.URL)
	c.Token = "secret"
	pub := func() error {
		return c.Publish(context.Background(), registry.PublishRequest{
			Name: "hello", Version: "1.2.3",
			Checksum: checksum,
			Tarball:  bytes.NewReader(tarball),
		})
	}
	if err := pub(); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	err := pub()
	if err == nil || !strings.Contains(err.Error(), "already published") {
		t.Errorf("want 409 already published, got %v", err)
	}
}

// TestScopedTokenOnlyPublishesOnePackage verifies scope enforcement —
// a token with publish:foo must not be able to publish bar.
func TestScopedTokenOnlyPublishesOnePackage(t *testing.T) {
	dir := t.TempDir()
	st, err := NewStorage(dir)
	if err != nil {
		t.Fatal(err)
	}
	tokens := NewTokenDB([]Token{
		{Token: "narrow", Scopes: []string{"publish:hello"}},
	})
	srv := httptest.NewServer(New(Config{Storage: st, Tokens: tokens, Logger: nopLogger(t)}))
	defer srv.Close()

	tarball := makeTarball(t, sampleManifest)
	sum := sha256.Sum256(tarball)
	checksum := "sha256:" + hex.EncodeToString(sum[:])
	c := registry.NewClient(srv.URL)
	c.Token = "narrow"

	// "hello" allowed.
	if err := c.Publish(context.Background(), registry.PublishRequest{
		Name: "hello", Version: "1.2.3", Checksum: checksum,
		Tarball: bytes.NewReader(tarball),
	}); err != nil {
		t.Fatalf("expected hello to publish, got %v", err)
	}
	// Building a second tarball for name "other" so the body validates.
	otherTB := makeTarball(t, strings.Replace(sampleManifest, `name = "hello"`, `name = "other"`, 1))
	sum2 := sha256.Sum256(otherTB)
	err = c.Publish(context.Background(), registry.PublishRequest{
		Name: "other", Version: "1.0.0",
		Checksum: "sha256:" + hex.EncodeToString(sum2[:]),
		Tarball:  bytes.NewReader(otherTB),
	})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("want 403 for scoped token, got %v", err)
	}
}

// TestIndexMissingPackage — 404 with a useful error body.
func TestIndexMissingPackage(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/crates/ghost")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: %d", resp.StatusCode)
	}
}

// TestHealth — simple probe.
func TestHealth(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: %d", resp.StatusCode)
	}
}

// TestYankHidesVersionFromClient — yanking should make the client's
// Versions() drop the entry.
func TestYankHidesVersionFromClient(t *testing.T) {
	srv, st := newTestServer(t, "secret")
	defer srv.Close()
	tarball := makeTarball(t, sampleManifest)
	sum := sha256.Sum256(tarball)
	checksum := "sha256:" + hex.EncodeToString(sum[:])

	c := registry.NewClient(srv.URL)
	c.Token = "secret"
	if err := c.Publish(context.Background(), registry.PublishRequest{
		Name: "hello", Version: "1.2.3", Checksum: checksum,
		Tarball: bytes.NewReader(tarball),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Yank("hello", "1.2.3", true); err != nil {
		t.Fatal(err)
	}
	vs, err := c.Versions(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Errorf("yanked version still visible: %+v", vs)
	}
}

// TestRootListsPackages — operator-facing endpoint.
func TestRootListsPackages(t *testing.T) {
	srv, _ := newTestServer(t, "secret")
	defer srv.Close()
	tarball := makeTarball(t, sampleManifest)
	sum := sha256.Sum256(tarball)
	c := registry.NewClient(srv.URL)
	c.Token = "secret"
	if err := c.Publish(context.Background(), registry.PublishRequest{
		Name: "hello", Version: "1.2.3",
		Checksum: "sha256:" + hex.EncodeToString(sum[:]),
		Tarball:  bytes.NewReader(tarball),
	}); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Packages []string `json:"packages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Packages) != 1 || body.Packages[0] != "hello" {
		t.Errorf("packages: %+v", body.Packages)
	}
}
