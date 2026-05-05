package selfhostcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ManifestVersion is the schema version stage0 / production knows
// how to deserialize. Bump this when fields are added or renamed in
// an incompatible way.
const ManifestVersion = 1

// RegistryURLEnv selects a base URL for the artifact registry. When
// unset (or empty) the network fetch path is disabled — the resolver
// falls through to ErrNotCached / stage0 fallback as before.
const RegistryURLEnv = "OSTY_SELF_REGISTRY_URL"

// RegistryOfflineEnv hard-disables the network fetcher even when the
// registry URL is set. Useful for CI jobs that must build everything
// locally + air-gapped environments. Truthy values: "1", "true",
// "yes", "on" (case-insensitive).
const RegistryOfflineEnv = "OSTY_SELF_REGISTRY_OFFLINE"

// ErrNotAvailable signals that the registry has no artifact for the
// requested Key. Distinct from a network failure or a SHA mismatch —
// callers treat it as a normal cache miss.
var ErrNotAvailable = errors.New("selfhostcache: artifact not available in registry")

// ErrSHAMismatch surfaces when a downloaded binary's SHA-256 does
// not match the manifest's `binarySHA256` field. The cache is left
// untouched on this error to avoid storing tampered binaries.
var ErrSHAMismatch = errors.New("selfhostcache: binary SHA-256 mismatch")

// Manifest is the published metadata for one (toolchain SHA, host
// triple) tuple. The schema is intentionally narrow so future
// signing or transport changes can layer over without renaming
// existing fields.
type Manifest struct {
	Version      int       `json:"version"`
	Key          string    `json:"key"`
	BinarySHA256 string    `json:"binarySHA256"`
	BinaryURL    string    `json:"binaryURL"`
	OstyVersion  string    `json:"ostyVersion,omitempty"`
	CreatedAt    time.Time `json:"createdAt,omitempty"`
}

// Fetcher retrieves a (manifest, body) pair for one artifact key.
// Implementations are responsible for transport-level error
// handling; SHA-256 verification + caching is performed by the
// surrounding resolver.
type Fetcher interface {
	// Fetch returns the Manifest plus a reader producing the binary
	// bytes referenced by `Manifest.BinaryURL`. The caller must
	// `Close()` the reader.
	Fetch(ctx context.Context, key Key) (Manifest, io.ReadCloser, error)
}

// HTTPFetcher resolves artifacts against a base URL using the
// canonical layout:
//
//	<baseURL>/<sha>-<triple>.json   ← manifest
//	<manifest.BinaryURL>            ← binary (resolved relative to baseURL)
//
// HTTPS is required for production; an http:// base URL is accepted
// for local registry tests but emits an explicit error in offline
// mode.
type HTTPFetcher struct {
	BaseURL string
	Client  *http.Client
}

// Fetch implements Fetcher.
func (f HTTPFetcher) Fetch(ctx context.Context, key Key) (Manifest, io.ReadCloser, error) {
	if f.BaseURL == "" {
		return Manifest{}, nil, ErrNotAvailable
	}
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}

	manifestURL, err := joinURL(f.BaseURL, key.String()+".json")
	if err != nil {
		return Manifest{}, nil, fmt.Errorf("selfhostcache: manifest URL: %w", err)
	}
	manifest, err := fetchManifest(ctx, client, manifestURL)
	if err != nil {
		return Manifest{}, nil, err
	}
	if manifest.Version != ManifestVersion {
		return Manifest{}, nil, fmt.Errorf("selfhostcache: manifest version %d unsupported (expected %d)", manifest.Version, ManifestVersion)
	}
	if manifest.Key != key.String() {
		return Manifest{}, nil, fmt.Errorf("selfhostcache: manifest key %q does not match requested %q", manifest.Key, key.String())
	}
	if !validHexSHA256(manifest.BinarySHA256) {
		return Manifest{}, nil, fmt.Errorf("selfhostcache: manifest BinarySHA256 not a 64-char hex string")
	}
	if manifest.BinaryURL == "" {
		return Manifest{}, nil, fmt.Errorf("selfhostcache: manifest BinaryURL empty")
	}

	binURL, err := resolveBinaryURL(f.BaseURL, manifest.BinaryURL)
	if err != nil {
		return Manifest{}, nil, fmt.Errorf("selfhostcache: binary URL: %w", err)
	}
	body, err := fetchBody(ctx, client, binURL)
	if err != nil {
		return Manifest{}, nil, err
	}
	return manifest, body, nil
}

func fetchManifest(ctx context.Context, client *http.Client, url string) (Manifest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Manifest{}, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return Manifest{}, fmt.Errorf("selfhostcache: GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Manifest{}, ErrNotAvailable
	}
	if resp.StatusCode != http.StatusOK {
		return Manifest{}, fmt.Errorf("selfhostcache: GET %s: HTTP %d", url, resp.StatusCode)
	}
	const maxManifestSize = 64 * 1024
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestSize+1))
	if err != nil {
		return Manifest{}, fmt.Errorf("selfhostcache: read manifest: %w", err)
	}
	if len(body) > maxManifestSize {
		return Manifest{}, fmt.Errorf("selfhostcache: manifest exceeds %d bytes", maxManifestSize)
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return Manifest{}, fmt.Errorf("selfhostcache: parse manifest: %w", err)
	}
	return m, nil
}

func fetchBody(ctx context.Context, client *http.Client, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("selfhostcache: GET %s: %w", url, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrNotAvailable
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("selfhostcache: GET %s: HTTP %d", url, resp.StatusCode)
	}
	return resp.Body, nil
}

func joinURL(base, suffix string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(suffix, "/")
	return u.String(), nil
}

func resolveBinaryURL(base, candidate string) (string, error) {
	if candidate == "" {
		return "", errors.New("empty URL")
	}
	cu, err := url.Parse(candidate)
	if err != nil {
		return "", err
	}
	if cu.IsAbs() {
		return cu.String(), nil
	}
	bu, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	return bu.ResolveReference(cu).String(), nil
}

func validHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// FetchAndInstall pulls the artifact for `key` from `fetcher`,
// verifies its SHA-256 against the manifest, and promotes the
// verified binary into the local cache. On success the absolute
// path of the cached binary is returned.
//
// Failure modes leave the cache untouched:
//
//   - ErrNotAvailable when the registry has no published artifact.
//   - ErrSHAMismatch when downloaded body fails the manifest hash.
//   - any transport / IO error from the underlying fetcher.
func FetchAndInstall(ctx context.Context, projectRoot string, key Key, fetcher Fetcher) (string, error) {
	if fetcher == nil {
		return "", ErrNotAvailable
	}
	if key.String() == "" {
		return "", errors.New("selfhostcache: fetch: zero key")
	}
	manifest, body, err := fetcher.Fetch(ctx, key)
	if err != nil {
		return "", err
	}
	defer body.Close()

	target := CachePath(projectRoot, key)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("selfhostcache: mkdir cache: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".osty-self-fetch-*.tmp")
	if err != nil {
		return "", fmt.Errorf("selfhostcache: create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, hasher), body); err != nil {
		tmp.Close()
		return "", fmt.Errorf("selfhostcache: download: %w", err)
	}
	gotSHA := hex.EncodeToString(hasher.Sum(nil))
	if gotSHA != strings.ToLower(manifest.BinarySHA256) {
		tmp.Close()
		return "", fmt.Errorf("%w: got %s, manifest %s", ErrSHAMismatch, gotSHA, manifest.BinarySHA256)
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return "", fmt.Errorf("selfhostcache: chmod: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("selfhostcache: close tmp: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return "", fmt.Errorf("selfhostcache: rename: %w", err)
	}
	return target, nil
}

// EnvFetcher returns the default network fetcher configured from
// process environment. It returns nil when the registry URL is unset
// or when offline mode is requested — both signals collapse the
// resolver back to its 3-step lookup chain.
func EnvFetcher() Fetcher {
	if isOffline() {
		return nil
	}
	base := strings.TrimSpace(os.Getenv(RegistryURLEnv))
	if base == "" {
		return nil
	}
	return HTTPFetcher{BaseURL: base}
}

func isOffline() bool {
	switch strings.TrimSpace(os.Getenv(RegistryOfflineEnv)) {
	case "1", "true", "TRUE", "True", "yes", "YES", "Yes", "on", "ON", "On":
		return true
	}
	return false
}

// ResolveBinaryWithFetch is like ResolveBinary but consults
// `fetcher` after a cache miss. On a successful fetch + verify + install
// the returned path is the cached path the in-process resolver would
// hit on subsequent calls; downstream callers therefore do not need
// to know whether the binary came from the local cache or the
// network.
//
// `fetcher` may be nil — in that case the function behaves exactly
// like `ResolveBinary`.
func ResolveBinaryWithFetch(ctx context.Context, projectRoot string, fetcher Fetcher) (string, Key, error) {
	bin, key, err := ResolveBinary(projectRoot)
	if err == nil {
		return bin, key, nil
	}
	if !errors.Is(err, ErrNotCached) || fetcher == nil {
		return "", key, err
	}
	if key.String() == "" {
		// ResolveBinary couldn't compute a key (no toolchain dir).
		// Network fetch is meaningless without one.
		return "", key, ErrNotCached
	}
	cached, fetchErr := FetchAndInstall(ctx, projectRoot, key, fetcher)
	if fetchErr != nil {
		// ErrNotAvailable / SHA mismatch / network errors all collapse
		// back to ErrNotCached so the upstream chain (stage0,
		// IsOstySelfMissing) is unchanged.
		if errors.Is(fetchErr, ErrNotAvailable) {
			return "", key, ErrNotCached
		}
		return "", key, fetchErr
	}
	return cached, key, nil
}
