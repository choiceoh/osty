package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/pkgmgr"
	"github.com/osty/osty/internal/registry"
)

// runSearch implements `osty search QUERY [--registry NAME] [--limit N]`:
// query the registry's full-text search endpoint and print the
// matching packages in a small table. Runs without a manifest when
// possible — callers may invoke it outside any project. When invoked
// inside a project, [registries] entries from osty.toml are honored
// so a private registry can be searched without extra flags.
//
// Exit codes: 0 success (any number of hits, including zero),
// 1 network / registry error, 2 usage error.
func runSearch(args []string, cliF cliFlags) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty search [--registry NAME] [--limit N] QUERY")
	}
	var (
		regName string
		limit   int
	)
	fs.StringVar(&regName, "registry", "", "registry name to query (defaults to the project's default registry)")
	fs.IntVar(&limit, "limit", 20, "max number of hits to display (0 = registry default)")
	_ = fs.Parse(args)

	if fs.NArg() == 0 {
		fs.Usage()
		os.Exit(2)
	}
	query := fs.Arg(0)

	regURL, err := pickSearchRegistryURL(regName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty search: %v\n", err)
		os.Exit(2)
	}

	client := registry.NewClient(regURL)
	results, err := client.Search(context.Background(), query, limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty search: %v\n", err)
		os.Exit(1)
	}
	if len(results.Hits) == 0 {
		fmt.Printf("No matches for %q on %s\n", query, regURL)
		return
	}
	// Width-fitted columns. Keep the layout small enough to read on
	// a narrow terminal but wide enough to show the version + a
	// snippet of the description.
	nameW := len("NAME")
	verW := len("LATEST")
	for _, h := range results.Hits {
		if len(h.Name) > nameW {
			nameW = len(h.Name)
		}
		if len(h.LatestVersion) > verW {
			verW = len(h.LatestVersion)
		}
	}
	fmt.Printf("%-*s  %-*s  %s\n", nameW, "NAME", verW, "LATEST", "DESCRIPTION")
	for _, h := range results.Hits {
		desc := h.Description
		if len(desc) > 60 {
			desc = desc[:57] + "..."
		}
		fmt.Printf("%-*s  %-*s  %s\n", nameW, h.Name, verW, h.LatestVersion, desc)
	}
	if results.Total > len(results.Hits) {
		fmt.Printf("\n...showing %d of %d hits (raise --limit for more)\n",
			len(results.Hits), results.Total)
	}
	_ = cliF
}

// pickSearchRegistryURL resolves a registry name to a base URL.
// Unlike publish, search may be invoked outside a project; we still
// try to read [registries] from a local manifest if one is reachable,
// but a missing manifest is not fatal — we fall back to the built-in
// default registry.
func pickSearchRegistryURL(name string) (string, error) {
	root, err := manifest.FindRoot(".")
	if err == nil {
		m, mErr := manifest.Read(root + "/" + manifest.ManifestFile)
		if mErr == nil && m != nil {
			for _, r := range m.Registries {
				if r.Name == name {
					if r.URL == "" {
						return "", fmt.Errorf("registry %q has no url set in osty.toml", name)
					}
					return r.URL, nil
				}
			}
		}
	}
	if name != "" {
		return "", fmt.Errorf("registry %q not declared in osty.toml", name)
	}
	return pkgmgr.DefaultRegistryURL, nil
}
