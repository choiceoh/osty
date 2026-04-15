package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/registry"
)

// runYank implements `osty yank --version V [--registry NAME] [NAME]`
// — mark a published version as yanked. Yanked versions remain
// downloadable for clients with an existing lockfile pin so we don't
// break reproducible builds, but disappear from new resolutions.
//
// runUnyank reverses that. Both share most of the wiring; we
// dispatch on `unyank` flag or sibling entry point in main.go.
//
// Default behavior (no positional NAME): yank the local package as
// declared in osty.toml — the common case for fixing a bad release.
//
// Exit codes: 0 success, 1 network/registry failure, 2 usage error
// (missing version, no manifest + no NAME, no token).
func runYank(args []string, cliF cliFlags) {
	yankToggleCmd("yank", args, cliF, func(c *registry.Client, ctx context.Context, name, ver string) error {
		return c.Yank(ctx, name, ver)
	})
}

// runUnyank is the inverse of runYank.
func runUnyank(args []string, cliF cliFlags) {
	yankToggleCmd("unyank", args, cliF, func(c *registry.Client, ctx context.Context, name, ver string) error {
		return c.Unyank(ctx, name, ver)
	})
}

func yankToggleCmd(verb string, args []string, cliF cliFlags, do func(*registry.Client, context.Context, string, string) error) {
	fs := flag.NewFlagSet(verb, flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: osty %s --version V [--registry NAME] [--token T] [PACKAGE_NAME]\n", verb)
	}
	var (
		version string
		regName string
		token   string
	)
	fs.StringVar(&version, "version", "", "version to "+verb+" (required)")
	fs.StringVar(&regName, "registry", "", "registry name (defaults to the package's default registry)")
	fs.StringVar(&token, "token", "", "API token (defaults to $OSTY_PUBLISH_TOKEN or osty.toml)")
	_ = fs.Parse(args)

	if version == "" {
		fmt.Fprintf(os.Stderr, "osty %s: --version is required\n", verb)
		fs.Usage()
		os.Exit(2)
	}

	// Resolve the package name and registry URL. If a positional NAME
	// is supplied we use it directly; otherwise the local manifest
	// must declare one.
	var (
		pkgName string
		regURL  string
		m       *manifest.Manifest
	)
	if fs.NArg() > 0 {
		pkgName = fs.Arg(0)
	}
	if root, err := manifest.FindRoot("."); err == nil {
		mm, mErr := manifest.Read(root + "/" + manifest.ManifestFile)
		if mErr == nil {
			m = mm
		}
	}
	if pkgName == "" {
		if m == nil || !m.HasPackage || m.Package.Name == "" {
			fmt.Fprintf(os.Stderr, "osty %s: no PACKAGE_NAME given and no [package] in osty.toml\n", verb)
			os.Exit(2)
		}
		pkgName = m.Package.Name
	}
	regURL, err := pickRegistryURL(m, regName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty %s: %v\n", verb, err)
		os.Exit(2)
	}

	if token == "" {
		token = os.Getenv("OSTY_PUBLISH_TOKEN")
	}
	if token == "" && m != nil {
		token = tokenFromManifest(m, regName)
	}
	if token == "" {
		token = credentialFromStore(regName)
	}
	if token == "" {
		fmt.Fprintf(os.Stderr,
			"osty %s: no token. pass --token, set $OSTY_PUBLISH_TOKEN, or `osty login --registry %s`\n",
			verb, regName)
		os.Exit(2)
	}

	client := registry.NewClient(regURL)
	client.Token = token
	if err := do(client, context.Background(), pkgName, version); err != nil {
		fmt.Fprintf(os.Stderr, "osty %s: %v\n", verb, err)
		os.Exit(1)
	}
	fmt.Printf("%sed %s %s on %s\n", verb, pkgName, version, regURL)
	_ = cliF
}
