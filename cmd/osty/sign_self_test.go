package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/toolchain/selfhostcache"
)

// TestSignSelfUsageMessage covers the static help text so future
// help-banner generation does not silently drop the canonical flags.
func TestSignSelfUsageMessage(t *testing.T) {
	got := signSelfUsage()
	for _, want := range []string{
		"osty sign-self",
		"--manifest",
		"--key",
		"genkey",
		"OSTY_SELF_TRUSTED_KEY",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("usage missing %q:\n%s", want, got)
		}
	}
}

// TestSignSelfGenKeyProducesValidPair invokes `osty sign-self
// genkey` and confirms the printed public/private pair round-trips
// through the canonical encoding.
func TestSignSelfGenKeyProducesValidPair(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "priv.hex")
	cmd := exec.Command(ostyBin, "sign-self", "genkey", "--out", keyPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("genkey: %v\n%s", err, out)
	}
	stdout := string(out)
	if !strings.Contains(stdout, "public-key:") {
		t.Errorf("missing public-key line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "private-key:") {
		t.Errorf("missing private-key line:\n%s", stdout)
	}

	raw, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read priv: %v", err)
	}
	priv, err := loadPrivateKey(keyPath)
	if err != nil {
		t.Fatalf("load priv: %v", err)
	}
	if len(priv) != ed25519.PrivateKeySize {
		t.Errorf("priv length = %d, want %d", len(priv), ed25519.PrivateKeySize)
	}

	// File mode should restrict access — secret material lives here.
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("key file perm = %v, want 0600", info.Mode().Perm())
	}

	if !strings.HasSuffix(strings.TrimSpace(string(raw)), hex.EncodeToString(priv)[len(hex.EncodeToString(priv))-32:]) {
		t.Errorf("on-disk key tail does not match parsed key")
	}
}

// TestSignSelfSignsManifestRoundTrip generates a key, signs a
// manifest, and verifies the signature through the registry path
// HTTPFetcher uses. Closes the producer/consumer loop the way the
// real CI workflow will.
func TestSignSelfSignsManifestRoundTrip(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)
	dir := t.TempDir()

	// 1. Generate a keypair.
	keyPath := filepath.Join(dir, "priv.hex")
	out, err := exec.Command(ostyBin, "sign-self", "genkey", "--out", keyPath).CombinedOutput()
	if err != nil {
		t.Fatalf("genkey: %v\n%s", err, out)
	}
	pubHex := pickPublicKey(t, string(out))

	// 2. Hand-craft a manifest in the canonical format. The CLI
	//    `osty manifest-self` produces the same shape; using a
	//    direct json.Marshal keeps the test independent of that
	//    surface.
	manifest := selfhostcache.Manifest{
		Version:      selfhostcache.ManifestVersion,
		Key:          strings.Repeat("c", 64) + "-linux-amd64",
		BinarySHA256: strings.Repeat("d", 64),
		BinaryURL:    "/osty-self",
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	manifestBytes = append(manifestBytes, '\n')
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	// 3. Sign.
	out, err = exec.Command(ostyBin, "sign-self", "--manifest", manifestPath, "--key", keyPath).CombinedOutput()
	if err != nil {
		t.Fatalf("sign-self: %v\n%s", err, out)
	}
	sigPath := manifestPath + selfhostcache.SignatureSuffix
	if _, err := os.Stat(sigPath); err != nil {
		t.Fatalf("expected signature at %s: %v", sigPath, err)
	}
	sigBody, err := os.ReadFile(sigPath)
	if err != nil {
		t.Fatalf("read sig: %v", err)
	}

	// 4. Stand up a registry that serves manifest + signature, point
	//    HTTPFetcher at it, and let it verify under the public key.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".json.sig"):
			w.Write(sigBody)
		case strings.HasSuffix(r.URL.Path, ".json"):
			w.Write(manifestBytes)
		case strings.HasSuffix(r.URL.Path, "/osty-self"):
			w.Write([]byte("dummy binary"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	pub, err := selfhostcache.ParseTrustedKey(pubHex)
	if err != nil {
		t.Fatalf("parse pub: %v", err)
	}
	fetcher := selfhostcache.HTTPFetcher{BaseURL: srv.URL, TrustedKey: pub}
	key := selfhostcache.Key{
		ToolchainSHA: strings.TrimSuffix(manifest.Key, "-linux-amd64"),
		Triple:       "linux-amd64",
	}
	gotManifest, body, err := fetcher.Fetch(context.Background(), key)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer body.Close()
	if gotManifest.Key != manifest.Key {
		t.Errorf("Key = %q, want %q", gotManifest.Key, manifest.Key)
	}
}

// TestSignSelfRequiresFlags makes sure an incomplete invocation
// fails fast instead of producing an empty `.sig` file silently.
func TestSignSelfRequiresFlags(t *testing.T) {
	ostyBin := buildOstyBinaryForTest(t)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"missing manifest", []string{"sign-self", "--key", "x"}, "--manifest is required"},
		{"missing key", []string{"sign-self", "--manifest", "x"}, "--key is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(ostyBin, tc.args...)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("expected exit error\n%s", out)
			}
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("expected ExitError; got %v", err)
			}
			if ee.ExitCode() != 2 {
				t.Errorf("exit = %d, want 2", ee.ExitCode())
			}
			if !strings.Contains(string(out), tc.want) {
				t.Errorf("output missing %q:\n%s", tc.want, out)
			}
		})
	}
}

// pickPublicKey extracts the hex public key from `osty sign-self
// genkey --out` output. The CLI prints both `private-key:` and
// `public-key:` lines on success.
func pickPublicKey(t *testing.T, stdout string) string {
	t.Helper()
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "public-key:") {
			continue
		}
		val := strings.TrimSpace(strings.TrimPrefix(line, "public-key:"))
		return val
	}
	t.Fatalf("no public-key line in:\n%s", stdout)
	return ""
}
