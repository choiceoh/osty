package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/pkgmgr"
	"github.com/osty/osty/internal/registry"
)

// runPublish implements `osty publish`: build a tarball of the
// current package and upload it to a configured registry.
//
// Flow:
//
//  1. Load the manifest and sanity-check it: `name` + `version`
//     must be set; `version` must be strict SemVer 2.0.0.
//  2. Produce a deterministic gzipped tar (via pkgmgr.CreateTarGz)
//     that contains osty.toml + every .osty source. Archive files
//     are sorted for reproducibility, so two publishes of the same
//     tree produce identical sha256 checksums.
//  3. Compute the sha256 checksum of the tarball.
//  4. POST to the configured registry's publish endpoint. The
//     registry address is picked from the `--registry` flag, or the
//     package's default registry ("" key in env.Registries).
//  5. Emit a summary on success. Failures surface the registry's
//     own error body.
//
// Authentication: the upload carries a `Bearer <token>` header.
// Tokens come from, in order of preference:
//   - `--token` flag
//   - `OSTY_PUBLISH_TOKEN` environment variable
//   - The token recorded under [registries.<name>] in osty.toml
//
// `--dry-run` performs every step except the upload and leaves the
// tarball in the project's `.osty/publish/` dir for inspection.
//
// Exit codes: 0 success, 1 I/O / network failure, 2 manifest or
// usage error.
func runPublish(args []string, cliF cliFlags) {
	fs := flag.NewFlagSet("publish", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty publish [--registry NAME] [--token T] [--dry-run]")
	}
	var (
		regName string
		token   string
		dryRun  bool
	)
	fs.StringVar(&regName, "registry", "", "registry name (defaults to the package's default registry)")
	fs.StringVar(&token, "token", "", "API token (defaults to $OSTY_PUBLISH_TOKEN or osty.toml)")
	fs.BoolVar(&dryRun, "dry-run", false, "build the tarball but do not upload")
	_ = fs.Parse(args)

	m, root, abort := loadManifestWithDiag(".", cliF)
	if abort {
		os.Exit(2)
	}
	if !m.HasPackage {
		fmt.Fprintln(os.Stderr, "osty publish: only package projects can be published (no [package] table)")
		os.Exit(2)
	}
	if m.Package.Name == "" || m.Package.Version == "" {
		fmt.Fprintln(os.Stderr, "osty publish: osty.toml must set both package.name and package.version")
		os.Exit(2)
	}

	// Build the tarball into <root>/.osty/publish/<name>-<version>.tgz.
	outDir := filepath.Join(root, ".osty", "publish")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "osty publish: %v\n", err)
		os.Exit(1)
	}
	tarPath := filepath.Join(outDir, fmt.Sprintf("%s-%s.tgz", m.Package.Name, m.Package.Version))
	var buf bytes.Buffer
	if err := pkgmgr.CreateTarGz(root, &buf); err != nil {
		fmt.Fprintf(os.Stderr, "osty publish: build tarball: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(tarPath, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "osty publish: %v\n", err)
		os.Exit(1)
	}
	checksum := pkgmgr.HashBytes(buf.Bytes())
	fmt.Printf("Packed %s-%s  (%d bytes, %s)\n",
		m.Package.Name, m.Package.Version, buf.Len(), shortHash(checksum))

	if dryRun {
		fmt.Printf("Dry run: tarball at %s (not uploaded)\n", tarPath)
		return
	}

	// Locate the target registry.
	regURL, err := pickRegistryURL(m, regName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty publish: %v\n", err)
		os.Exit(2)
	}
	if token == "" {
		token = os.Getenv("OSTY_PUBLISH_TOKEN")
	}
	if token == "" {
		token = tokenFromManifest(m, regName)
	}
	if token == "" {
		token = credentialFromStore(regName)
	}
	if token == "" {
		fmt.Fprintln(os.Stderr,
			"osty publish: no publish token. pass --token, set $OSTY_PUBLISH_TOKEN, run `osty login`, or add one to [registries.<name>] in osty.toml")
		os.Exit(2)
	}

	client := registry.NewClient(regURL)
	client.Token = token
	req := registry.PublishRequest{
		Name:     m.Package.Name,
		Version:  m.Package.Version,
		Checksum: checksum,
		Tarball:  bytes.NewReader(buf.Bytes()),
		Metadata: registry.PublishMetadata{
			Description: m.Package.Description,
			License:     m.Package.License,
			Authors:     m.Package.Authors,
			Repository:  m.Package.Repository,
			Homepage:    m.Package.Homepage,
			Keywords:    m.Package.Keywords,
		},
	}
	if err := client.Publish(context.Background(), req); err != nil {
		fmt.Fprintf(os.Stderr, "osty publish: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Published %s %s to %s\n", m.Package.Name, m.Package.Version, regURL)
	_ = cliF
}

// pickRegistryURL returns the URL of the registry named `name`.
// The empty name selects the default registry: first checks the
// manifest's [registries.] subtable, then falls back to
// pkgmgr.DefaultRegistryURL.
func pickRegistryURL(m *manifest.Manifest, name string) (string, error) {
	for _, r := range m.Registries {
		if r.Name == name {
			if r.URL == "" {
				return "", fmt.Errorf("registry %q has no url set in osty.toml", name)
			}
			return r.URL, nil
		}
	}
	if name == "" {
		return pkgmgr.DefaultRegistryURL, nil
	}
	return "", fmt.Errorf("registry %q not declared in osty.toml", name)
}

// tokenFromManifest returns the stored token for `name` if one is
// present in [registries.<name>]. We look the name up case-sensitively.
// A missing entry returns "".
func tokenFromManifest(m *manifest.Manifest, name string) string {
	for _, r := range m.Registries {
		if r.Name == name {
			return r.Token
		}
	}
	return ""
}

// shortHash returns the first 12 hex chars of a sha256:... checksum
// for display. Full hashes are logged on error but clutter the
// success summary.
func shortHash(full string) string {
	s := strings.TrimPrefix(full, pkgmgr.HashPrefix)
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
