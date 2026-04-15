package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/osty/osty/internal/pkgmgr/semver"
	"github.com/osty/osty/internal/registry"
)

// runInfo implements `osty info NAME [--registry NAME]` — fetch the
// registry index entry for a package and print a human-friendly
// summary: latest version (semver-sorted), all versions, checksum,
// declared dependencies. A drop-in equivalent of `cargo info` /
// `npm view`.
//
// Exit codes: 0 success, 1 network/registry error, 2 usage error
// (missing name).
func runInfo(args []string, cliF cliFlags) {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty info [--registry NAME] [--all-versions] PACKAGE")
	}
	var (
		regName     string
		allVersions bool
	)
	fs.StringVar(&regName, "registry", "", "registry name to query (defaults to the project's default)")
	fs.BoolVar(&allVersions, "all-versions", false, "list every published version, not just the latest")
	_ = fs.Parse(args)

	if fs.NArg() == 0 {
		fs.Usage()
		os.Exit(2)
	}
	name := fs.Arg(0)

	regURL, err := pickSearchRegistryURL(regName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty info: %v\n", err)
		os.Exit(2)
	}

	client := registry.NewClient(regURL)
	versions, err := client.Versions(context.Background(), name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty info: %v\n", err)
		os.Exit(1)
	}
	if len(versions) == 0 {
		fmt.Printf("No published versions for %q on %s\n", name, regURL)
		return
	}
	// Sort newest first using semver precedence so the report
	// shows the most-likely-relevant version up top regardless of
	// the registry's response order.
	sort.SliceStable(versions, func(i, j int) bool {
		vi, errI := semver.ParseVersion(versions[i].Version)
		vj, errJ := semver.ParseVersion(versions[j].Version)
		if errI != nil || errJ != nil {
			return versions[i].Version > versions[j].Version
		}
		return semver.Less(vj, vi)
	})
	latest := versions[0]
	fmt.Printf("Package:   %s\n", name)
	fmt.Printf("Latest:    %s\n", latest.Version)
	fmt.Printf("Checksum:  %s\n", latest.Checksum)
	fmt.Printf("Published: %s\n", latest.PublishedAt.Format("2006-01-02"))
	fmt.Printf("Registry:  %s\n", regURL)
	if len(latest.Dependencies) > 0 {
		fmt.Println("Dependencies:")
		for _, d := range latest.Dependencies {
			kind := d.Kind
			if kind == "" {
				kind = "normal"
			}
			fmt.Printf("  - %s %s (%s)\n", d.Name, d.Req, kind)
		}
	}
	if len(latest.Features) > 0 {
		fmt.Println("Features:")
		featNames := make([]string, 0, len(latest.Features))
		for n := range latest.Features {
			featNames = append(featNames, n)
		}
		sort.Strings(featNames)
		for _, n := range featNames {
			fmt.Printf("  - %s -> %v\n", n, latest.Features[n])
		}
	}
	if allVersions {
		fmt.Println("All versions:")
		for _, v := range versions {
			fmt.Printf("  %s  %s\n", v.Version, v.Checksum)
		}
	} else if len(versions) > 1 {
		fmt.Printf("(+%d earlier version(s); pass --all-versions to list)\n", len(versions)-1)
	}
	_ = cliF
}
