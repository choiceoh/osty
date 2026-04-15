package registry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVersionsDecodesAndFiltersYanked stands up a test server that
// returns a mixed set of yanked / active versions. The client should
// hide yanked entries from the returned slice.
func TestVersionsDecodesAndFiltersYanked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/crates/foo" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(IndexEntry{
			Name: "foo",
			Versions: []Version{
				{Version: "1.0.0", Checksum: "sha256:aaa"},
				{Version: "1.1.0", Checksum: "sha256:bbb", Yanked: true},
				{Version: "2.0.0", Checksum: "sha256:ccc"},
			},
		})
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	got, err := c.Versions(context.Background(), "foo")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 non-yanked versions, got %d", len(got))
	}
	if got[0].Version != "1.0.0" || got[1].Version != "2.0.0" {
		t.Errorf("unexpected versions: %+v", got)
	}
}

// TestDownloadTarballStreams checks that a binary body is written to
// the requested path byte-for-byte.
func TestDownloadTarballStreams(t *testing.T) {
	payload := []byte("fake tarball bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/crates/foo/1.2.3/tar" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	tmp := t.TempDir()
	dest := filepath.Join(tmp, "out.tgz")
	if err := c.DownloadTarball(context.Background(), "foo", "1.2.3", dest); err != nil {
		t.Fatalf("download: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Errorf("body mismatch: got %q, want %q", got, payload)
	}
}

// TestPublishRequiresToken confirms the client refuses to call the
// upload endpoint without a token. Prevents accidental anonymous
// publishes.
func TestPublishRequiresToken(t *testing.T) {
	c := NewClient("http://example.invalid")
	err := c.Publish(context.Background(), PublishRequest{
		Name: "x", Version: "0.1", Checksum: "sha256:00",
		Tarball: strings.NewReader(""),
	})
	if err == nil || !strings.Contains(err.Error(), "token") {
		t.Errorf("expected token-missing error, got %v", err)
	}
}

// TestPublishSendsHeaders sets a token, makes the request, and
// verifies Authorization / Content-Type / Osty-Checksum headers
// reach the server with the expected values. Also exercises the
// metadata header shape.
func TestPublishSendsHeaders(t *testing.T) {
	var gotAuth, gotType, gotChecksum, gotMeta string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotType = r.Header.Get("Content-Type")
		gotChecksum = r.Header.Get("Osty-Checksum")
		gotMeta = r.Header.Get("Osty-Metadata")
		b, _ := io.ReadAll(r.Body)
		gotBody = b
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	c.Token = "abc"
	body := strings.NewReader("PAYLOAD")
	err := c.Publish(context.Background(), PublishRequest{
		Name:     "pkg",
		Version:  "1.0.0",
		Checksum: "sha256:deadbeef",
		Tarball:  body,
		Metadata: PublishMetadata{
			Description: "a",
			License:     "MIT",
			Authors:     []string{"A <a@b>"},
			Keywords:    []string{"x", "y"},
		},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if gotAuth != "Bearer abc" {
		t.Errorf("auth: %q", gotAuth)
	}
	if gotType != "application/x-tar+gzip" {
		t.Errorf("content-type: %q", gotType)
	}
	if gotChecksum != "sha256:deadbeef" {
		t.Errorf("checksum: %q", gotChecksum)
	}
	if !strings.Contains(gotMeta, `"license":"MIT"`) ||
		!strings.Contains(gotMeta, `"keywords":["x","y"]`) {
		t.Errorf("metadata: %q", gotMeta)
	}
	if string(gotBody) != "PAYLOAD" {
		t.Errorf("body: %q", gotBody)
	}
}

// TestErrorBodyPropagated: when the registry returns an error, the
// server's body should appear in the returned error so users see
// the reason.
func TestErrorBodyPropagated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "name already taken", http.StatusConflict)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	c.Token = "t"
	err := c.Publish(context.Background(), PublishRequest{
		Name: "x", Version: "1", Checksum: "sha256:00",
		Tarball: strings.NewReader(""),
	})
	if err == nil || !strings.Contains(err.Error(), "already taken") {
		t.Errorf("expected body in error, got %v", err)
	}
}
