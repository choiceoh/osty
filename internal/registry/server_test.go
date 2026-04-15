package registry

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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
	"time"
)

func TestFileServerPublishSearchDownloadAndYank(t *testing.T) {
	handler := NewServer(NewFileStore(t.TempDir()))
	handler.Authorize = BearerTokenAuth("tok")
	handler.Now = func() time.Time {
		return time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	payload := packageTar(t, "foo", "1.2.3", `
[dependencies]
bar = "^1.0.0"

[features]
default = ["extra"]
extra = []
`)
	client := NewClient(srv.URL)
	client.Token = "tok"
	if err := client.Publish(context.Background(), PublishRequest{
		Name:     "foo",
		Version:  "1.2.3",
		Checksum: testChecksum(payload),
		Tarball:  bytes.NewReader(payload),
		Metadata: PublishMetadata{
			Description: "friendly foo package",
			License:     "MIT",
			Keywords:    []string{"demo", "foo"},
		},
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	versions, err := client.Versions(context.Background(), "foo")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("versions = %+v", versions)
	}
	if versions[0].Version != "1.2.3" || versions[0].Checksum != testChecksum(payload) {
		t.Fatalf("version entry = %+v", versions[0])
	}
	if len(versions[0].Dependencies) != 1 || versions[0].Dependencies[0].Name != "bar" {
		t.Fatalf("dependencies = %+v", versions[0].Dependencies)
	}
	if got := versions[0].Features["default"]; len(got) != 1 || got[0] != "extra" {
		t.Fatalf("features = %+v", versions[0].Features)
	}

	results, err := client.Search(context.Background(), "friendly", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if results.Total != 1 || len(results.Hits) != 1 {
		t.Fatalf("search results = %+v", results)
	}
	if results.Hits[0].Name != "foo" || results.Hits[0].LatestVersion != "1.2.3" {
		t.Fatalf("search hit = %+v", results.Hits[0])
	}

	dest := filepath.Join(t.TempDir(), "foo.tgz")
	if err := client.DownloadTarball(context.Background(), "foo", "1.2.3", dest); err != nil {
		t.Fatalf("DownloadTarball: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("downloaded bytes differ")
	}

	results, err = client.Search(context.Background(), "foo", 5)
	if err != nil {
		t.Fatalf("Search after download: %v", err)
	}
	if results.Hits[0].Downloads != 1 {
		t.Fatalf("downloads = %d, want 1", results.Hits[0].Downloads)
	}

	if err := client.Yank(context.Background(), "foo", "1.2.3"); err != nil {
		t.Fatalf("Yank: %v", err)
	}
	versions, err = client.Versions(context.Background(), "foo")
	if err != nil {
		t.Fatalf("Versions after yank: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("yanked version should be hidden, got %+v", versions)
	}
	if err := client.DownloadTarball(context.Background(), "foo", "1.2.3", filepath.Join(t.TempDir(), "yanked.tgz")); err != nil {
		t.Fatalf("yanked versions should remain downloadable: %v", err)
	}
	if err := client.Unyank(context.Background(), "foo", "1.2.3"); err != nil {
		t.Fatalf("Unyank: %v", err)
	}
	versions, err = client.Versions(context.Background(), "foo")
	if err != nil || len(versions) != 1 {
		t.Fatalf("Versions after unyank: %v / %+v", err, versions)
	}
}

func TestFileServerRejectsBadAuthDuplicateAndChecksum(t *testing.T) {
	handler := NewServer(NewFileStore(t.TempDir()))
	handler.Authorize = BearerTokenAuth("good")
	srv := httptest.NewServer(handler)
	defer srv.Close()

	payload := packageTar(t, "authpkg", "0.1.0", "")
	client := NewClient(srv.URL)
	client.Token = "bad"
	err := client.Publish(context.Background(), PublishRequest{
		Name:     "authpkg",
		Version:  "0.1.0",
		Checksum: testChecksum(payload),
		Tarball:  bytes.NewReader(payload),
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Fatalf("bad auth error = %v", err)
	}

	client.Token = "good"
	err = client.Publish(context.Background(), PublishRequest{
		Name:     "authpkg",
		Version:  "0.1.0",
		Checksum: "sha256:0000",
		Tarball:  bytes.NewReader(payload),
	})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("checksum error = %v", err)
	}

	err = client.Publish(context.Background(), PublishRequest{
		Name:     "authpkg",
		Version:  "0.1.0",
		Checksum: testChecksum(payload),
		Tarball:  bytes.NewReader(payload),
	})
	if err != nil {
		t.Fatalf("first publish: %v", err)
	}
	err = client.Publish(context.Background(), PublishRequest{
		Name:     "authpkg",
		Version:  "0.1.0",
		Checksum: testChecksum(payload),
		Tarball:  bytes.NewReader(payload),
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 409") {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestFileServerIndexETag(t *testing.T) {
	handler := NewServer(NewFileStore(t.TempDir()))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	payload := packageTar(t, "etagpkg", "1.0.0", "")
	client := NewClient(srv.URL)
	client.Token = "anything"
	if err := client.Publish(context.Background(), PublishRequest{
		Name:     "etagpkg",
		Version:  "1.0.0",
		Checksum: testChecksum(payload),
		Tarball:  bytes.NewReader(payload),
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	resp, err := http.Get(srv.URL + "/v1/crates/etagpkg")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	etag := resp.Header.Get("ETag")
	if resp.StatusCode != http.StatusOK || etag == "" {
		t.Fatalf("first index status=%d etag=%q", resp.StatusCode, etag)
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/crates/etagpkg", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("If-None-Match", etag)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotModified {
		t.Fatalf("second index status=%d, want 304", resp.StatusCode)
	}
}

func packageTar(t *testing.T, name, version, extraManifest string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	manifest := `[package]
name = "` + name + `"
version = "` + version + `"
edition = "0.3"
description = "test package"
license = "MIT"
` + extraManifest
	files := map[string]string{
		"osty.toml": manifest,
		"lib.osty":  "pub fn value() -> Int { 1 }\n",
	}
	for name, body := range files {
		data := []byte(body)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func testChecksum(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
