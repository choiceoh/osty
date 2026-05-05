package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/osty/osty/internal/toolchain/selfhostcache"
)

// TestManifestSelfUsageMessage verifies the CLI usage string lists
// the canonical flags so future help-text generation stays in sync
// with the `osty manifest-self` interface.
func TestManifestSelfUsageMessage(t *testing.T) {
	got := manifestSelfUsage()
	for _, want := range []string{
		"osty manifest-self",
		"--bin",
		"--binary-url",
		"--triple",
		"--out",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("usage missing %q:\n%s", want, got)
		}
	}
}

// buildOstyBinaryForTest produces a host osty binary into the test's
// tempdir. Returns its absolute path. Cached via t.TempDir so each
// test run gets an isolated copy without polluting the workspace.
func buildOstyBinaryForTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "osty")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/osty")
	cmd.Dir = mustRepoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build osty: %v\n%s", err, out)
	}
	return bin
}

// mustRepoRoot walks up from the test file to the repo root by
// looking for go.mod — keeps the helper independent of the
// `go test` working directory which differs between invocations.
func mustRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found from cwd")
		}
		dir = parent
	}
}

// TestRunManifestSelfWritesValidManifest invokes the CLI end-to-end
// with a synthetic toolchain layout, an arbitrary binary body, and
// confirms the produced manifest matches the layout consumed by
// `selfhostcache.HTTPFetcher`.
func TestRunManifestSelfWritesValidManifest(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)

	// Synthetic project root with a non-empty toolchain dir + a fake
	// `osty-self` body. The CLI runs in this directory.
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "osty.toml"), []byte("[package]\nname = \"fake\"\n"), 0o644); err != nil {
		t.Fatalf("osty.toml: %v", err)
	}
	tcDir := filepath.Join(projectRoot, "toolchain")
	if err := os.MkdirAll(tcDir, 0o755); err != nil {
		t.Fatalf("toolchain mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tcDir, "main.osty"), []byte("// stub\n"), 0o644); err != nil {
		t.Fatalf("main.osty: %v", err)
	}

	binPath := filepath.Join(projectRoot, "fake-osty-self")
	body := []byte("fake osty-self body bytes")
	if err := os.WriteFile(binPath, body, 0o755); err != nil {
		t.Fatalf("write fake bin: %v", err)
	}

	outPath := filepath.Join(projectRoot, "manifest.json")
	cmd := exec.Command(ostyBin,
		"manifest-self",
		"--bin", binPath,
		"--binary-url", "/linux-amd64/osty-self",
		"--triple", "linux-amd64",
		"--osty-version", "test-0.0.0",
		"--out", outPath,
	)
	cmd.Dir = projectRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("osty manifest-self: %v\n%s", err, out)
	}

	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var got selfhostcache.Manifest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode manifest: %v\n%s", err, raw)
	}
	if got.Version != selfhostcache.ManifestVersion {
		t.Errorf("Version = %d, want %d", got.Version, selfhostcache.ManifestVersion)
	}
	if !strings.HasSuffix(got.Key, "-linux-amd64") {
		t.Errorf("Key = %q, want suffix -linux-amd64", got.Key)
	}
	wantSHA := sha256.Sum256(body)
	if got.BinarySHA256 != hex.EncodeToString(wantSHA[:]) {
		t.Errorf("BinarySHA256 = %q, want %q", got.BinarySHA256, hex.EncodeToString(wantSHA[:]))
	}
	if got.BinaryURL != "/linux-amd64/osty-self" {
		t.Errorf("BinaryURL = %q", got.BinaryURL)
	}
	if got.OstyVersion != "test-0.0.0" {
		t.Errorf("OstyVersion = %q", got.OstyVersion)
	}
}

// TestRunManifestSelfRoundTripsThroughHTTPFetcher publishes the
// manifest produced by the CLI to a httptest registry, points
// `HTTPFetcher` at it, and verifies the (manifest, body) pair fetches
// + verifies cleanly. Closes the loop on the producer/consumer
// contract.
func TestRunManifestSelfRoundTripsThroughHTTPFetcher(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)

	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "osty.toml"), []byte("[package]\nname = \"fake\"\n"), 0o644); err != nil {
		t.Fatalf("osty.toml: %v", err)
	}
	tcDir := filepath.Join(projectRoot, "toolchain")
	if err := os.MkdirAll(tcDir, 0o755); err != nil {
		t.Fatalf("toolchain mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tcDir, "main.osty"), []byte("// stub\n"), 0o644); err != nil {
		t.Fatalf("main.osty: %v", err)
	}

	binPath := filepath.Join(projectRoot, "fake-osty-self")
	body := []byte("round-trip body")
	if err := os.WriteFile(binPath, body, 0o755); err != nil {
		t.Fatalf("write fake bin: %v", err)
	}

	// Run the CLI with a relative binary URL — HTTPFetcher resolves
	// it against the registry base URL.
	outPath := filepath.Join(projectRoot, "manifest.json")
	cmd := exec.Command(ostyBin,
		"manifest-self",
		"--bin", binPath,
		"--binary-url", "osty-self",
		"--triple", selfhostcache.HostTriple(),
		"--out", outPath,
	)
	cmd.Dir = projectRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("osty manifest-self: %v\n%s", err, out)
	}

	manifestRaw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var pub selfhostcache.Manifest
	if err := json.Unmarshal(manifestRaw, &pub); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}

	// Stand up a registry that serves the manifest + binary at the
	// canonical paths the CLI assumed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".json"):
			w.Header().Set("Content-Type", "application/json")
			w.Write(manifestRaw)
		case strings.HasSuffix(r.URL.Path, "osty-self"):
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	fetcher := selfhostcache.HTTPFetcher{BaseURL: srv.URL}
	gotManifest, gotBody, err := fetcher.Fetch(context.Background(), selfhostcache.Key{
		ToolchainSHA: strings.TrimSuffix(pub.Key, "-"+selfhostcache.HostTriple()),
		Triple:       selfhostcache.HostTriple(),
	})
	if err != nil {
		t.Fatalf("fetcher.Fetch: %v", err)
	}
	defer gotBody.Close()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(gotBody); err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), body) {
		t.Errorf("body mismatch: got %q want %q", buf.Bytes(), body)
	}
	if gotManifest.BinarySHA256 != pub.BinarySHA256 {
		t.Errorf("manifest sha mismatch: got %q want %q", gotManifest.BinarySHA256, pub.BinarySHA256)
	}
}

// TestRunManifestSelfRequiresFlags makes sure the CLI rejects
// invocations missing the required arguments instead of silently
// emitting a malformed manifest.
func TestRunManifestSelfRequiresFlags(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "osty.toml"), []byte("[package]\nname = \"fake\"\n"), 0o644); err != nil {
		t.Fatalf("osty.toml: %v", err)
	}
	tcDir := filepath.Join(projectRoot, "toolchain")
	if err := os.MkdirAll(tcDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tcDir, "x.osty"), []byte("// stub\n"), 0o644); err != nil {
		t.Fatalf("x.osty: %v", err)
	}

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"missing bin", []string{"manifest-self", "--binary-url", "x"}, "--bin is required"},
		{"missing url", []string{"manifest-self", "--bin", "x"}, "--binary-url is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(ostyBin, tc.args...)
			cmd.Dir = projectRoot
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("expected exit error; got success\n%s", out)
			}
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("expected ExitError; got %v", err)
			}
			if ee.ExitCode() != 2 {
				t.Errorf("exit code = %d, want 2", ee.ExitCode())
			}
			if !strings.Contains(string(out), tc.want) {
				t.Errorf("output missing %q:\n%s", tc.want, out)
			}
		})
	}
}
