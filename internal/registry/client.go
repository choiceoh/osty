// Package registry speaks the HTTP protocol an osty registry
// exposes. The wire shape is small and pragmatic — we model it
// after the simplified Cargo protocol (GET index JSON, GET tarball
// blob, PUT publish) so existing static-site hosting or a tiny
// custom server can serve as a registry.
//
// Endpoints:
//
//	GET  <base>/v1/crates/<name>           → IndexEntry (JSON)
//	GET  <base>/v1/crates/<name>/<ver>/tar → binary .tgz
//	PUT  <base>/v1/crates/<name>/<ver>     → upload a tarball
//	                                          (body: application/x-tar+gzip)
//	                                          (auth: Bearer <token>)
//
// The client treats 4xx / 5xx responses as errors; the body is
// surfaced unchanged. Success responses are decoded as JSON when a
// JSON body is expected; otherwise streamed to a caller-provided
// destination file.
package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// DefaultTimeout bounds how long any single request may take. Tuned
// for small indexes and moderate tarball downloads on a typical
// connection; large packages may want a longer timeout or a custom
// http.Client.
const DefaultTimeout = 60 * time.Second

// Client is the HTTP client for one registry. Zero-value is not
// usable; construct with NewClient. The UserAgent header identifies
// us to the registry for metrics / rate-limiting; set Token to
// authenticate publish requests.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	UserAgent string
	Token   string
}

// NewClient builds a client with default HTTP settings. Callers that
// need custom transport wiring (corporate proxies, mTLS) can set
// HTTP directly afterward.
func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		HTTP:      &http.Client{Timeout: DefaultTimeout},
		UserAgent: "osty/0.3",
	}
}

// IndexEntry is the `/v1/crates/<name>` response. It lists every
// version ever published. Yanked versions are marked so clients can
// exclude them from resolution unless explicitly requested.
type IndexEntry struct {
	Name     string    `json:"name"`
	Versions []Version `json:"versions"`
}

// Version is one published version as the registry sees it. The
// fields here are enough for semver resolution, checksum verification,
// and dependency graph construction.
type Version struct {
	Version      string             `json:"version"`
	Checksum     string             `json:"checksum"`
	PublishedAt  time.Time          `json:"published_at"`
	Yanked       bool               `json:"yanked"`
	Dependencies []VersionDependency `json:"dependencies"`
	Features     map[string][]string `json:"features,omitempty"`
}

// VersionDependency is one entry of a published version's declared
// deps. `Req` is the semver requirement; `Kind` distinguishes
// "normal" from "dev".
type VersionDependency struct {
	Name string `json:"name"`
	Req  string `json:"req"`
	Kind string `json:"kind"` // "normal" | "dev"
}

// Versions returns every non-yanked version of `name`. The returned
// slice is in registry-supplied order; callers sort by semver
// precedence if they need a specific order.
func (c *Client) Versions(ctx context.Context, name string) ([]Version, error) {
	u, err := c.endpoint("v1", "crates", name)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.addHeaders(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return nil, err
	}
	var out IndexEntry
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode index: %w", err)
	}
	// Filter yanked versions up front — callers can't accept them
	// from the normal resolver path anyway.
	var keep []Version
	for _, v := range out.Versions {
		if !v.Yanked {
			keep = append(keep, v)
		}
	}
	return keep, nil
}

// DownloadTarball streams the package tarball for (name, version)
// into destPath. destPath's directory must already exist. On success
// the file is flushed before the function returns.
func (c *Client) DownloadTarball(ctx context.Context, name, version, destPath string) error {
	u, err := c.endpoint("v1", "crates", name, version, "tar")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	c.addHeaders(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp, http.StatusOK); err != nil {
		return err
	}
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, resp.Body); err != nil {
		_ = os.Remove(destPath)
		return err
	}
	return nil
}

// PublishRequest is the payload `Publish` consumes. Tarball should
// be a ready-to-upload `.tgz` (see pkgmgr.CreateTarGz).
//
// Metadata is transmitted in an `Osty-Metadata` header as URL-encoded
// JSON. Registries parse this to populate their listing pages.
type PublishRequest struct {
	Name     string
	Version  string
	Checksum string // "sha256:<hex>"
	Tarball  io.Reader
	Metadata PublishMetadata
}

// PublishMetadata mirrors the manifest fields a registry surfaces on
// its listing page. Kept separate from the manifest struct so the
// client doesn't pull the manifest package (which would create a
// cycle with pkgmgr → registry → manifest).
type PublishMetadata struct {
	Description string   `json:"description,omitempty"`
	License     string   `json:"license,omitempty"`
	Authors     []string `json:"authors,omitempty"`
	Repository  string   `json:"repository,omitempty"`
	Homepage    string   `json:"homepage,omitempty"`
	Keywords    []string `json:"keywords,omitempty"`
}

// Publish uploads a tarball + metadata to the registry. Requires
// c.Token to be set; the server decides whether the token is
// authorized to publish the given name.
func (c *Client) Publish(ctx context.Context, r PublishRequest) error {
	if c.Token == "" {
		return fmt.Errorf("no publish token configured for registry %s", c.BaseURL)
	}
	u, err := c.endpoint("v1", "crates", r.Name, r.Version)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, r.Tarball)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-tar+gzip")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Osty-Checksum", r.Checksum)
	meta, err := json.Marshal(r.Metadata)
	if err != nil {
		return err
	}
	// The metadata is short enough that header encoding is fine. A
	// multipart upload would be cleaner but more protocol surface to
	// version.
	req.Header.Set("Osty-Metadata", string(meta))
	c.addHeaders(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkStatus(resp, http.StatusOK, http.StatusCreated, http.StatusAccepted)
}

// endpoint builds a URL from BaseURL + path segments.
func (c *Client) endpoint(segments ...string) (string, error) {
	if c.BaseURL == "" {
		return "", fmt.Errorf("registry base URL is empty")
	}
	parts := append([]string{c.BaseURL}, segments...)
	u := strings.Join(parts, "/")
	// Make sure the result still parses — a malformed BaseURL should
	// fail clearly rather than during the request.
	if _, err := url.Parse(u); err != nil {
		return "", err
	}
	return u, nil
}

// addHeaders sets headers common to every request.
func (c *Client) addHeaders(req *http.Request) {
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	req.Header.Set("Accept", "application/json")
}

// checkStatus returns nil iff resp.StatusCode is in allowed.
// Consumes a portion of the body for the error message so the user
// sees the registry's own diagnostic.
func checkStatus(resp *http.Response, allowed ...int) error {
	for _, code := range allowed {
		if resp.StatusCode == code {
			return nil
		}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<14))
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("registry %s: HTTP %d: %s", resp.Request.URL, resp.StatusCode, msg)
}
