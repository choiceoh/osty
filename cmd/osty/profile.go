package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/osty/osty/internal/manifest"
	"github.com/osty/osty/internal/profile"
)

// runProfiles implements `osty profiles`: print every known profile
// (built-ins + any declared in osty.toml) with its key flags so users
// can see at a glance how debug differs from release and which
// manifest overrides are in effect.
//
// Exit codes: 0 on success, 1 on manifest-load failure. Running
// outside a project directory is fine — the built-ins still print.
func runProfiles(args []string, flags cliFlags) {
	fs := flag.NewFlagSet("profiles", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty profiles [--verbose]")
	}
	var verbose bool
	fs.BoolVar(&verbose, "verbose", false, "also print go-flags, env, and inheritance")
	fs.BoolVar(&verbose, "v", false, "alias for --verbose")
	_ = fs.Parse(args)

	cfg := loadProfileConfig(flags)
	names := cfg.ProfileNames()
	w := tabular(os.Stdout)
	fmt.Fprintln(w, "NAME\tOPT\tDEBUG\tSTRIP\tOVERFLOW\tLTO\tSOURCE")
	for _, n := range names {
		p := cfg.Profiles[n]
		src := "built-in"
		if p.UserDefined {
			src = "manifest"
			if p.Inherits != "" {
				src = "manifest (inherits " + p.Inherits + ")"
			}
		}
		fmt.Fprintf(w, "%s\t%d\t%v\t%v\t%v\t%v\t%s\n",
			p.Name, p.OptLevel, p.Debug, p.Strip, p.Overflow, p.LTO, src)
	}
	flushTabular(w)
	if verbose {
		for _, n := range names {
			p := cfg.Profiles[n]
			fmt.Printf("\n[profile.%s]\n", p.Name)
			if len(p.GoFlags) > 0 {
				fmt.Printf("  go-flags: %s\n", strings.Join(p.GoFlags, " "))
			}
			if len(p.Env) > 0 {
				keys := make([]string, 0, len(p.Env))
				for k := range p.Env {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					fmt.Printf("  env.%s = %q\n", k, p.Env[k])
				}
			}
		}
	}
}

// runTargets prints the declared cross-compilation targets.
func runTargets(args []string, flags cliFlags) {
	fs := flag.NewFlagSet("targets", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty targets")
	}
	_ = fs.Parse(args)
	cfg := loadProfileConfig(flags)
	names := cfg.TargetNames()
	if len(names) == 0 {
		fmt.Println("no [target.*] entries declared in osty.toml")
		fmt.Println("hint: add one, e.g.")
		fmt.Println("    [target.amd64-linux]")
		fmt.Println("    cgo = false")
		return
	}
	w := tabular(os.Stdout)
	fmt.Fprintln(w, "TRIPLE\tGOARCH\tGOOS\tCGO")
	for _, n := range names {
		t := cfg.Targets[n]
		cgo := "(host)"
		if t.CGO != nil {
			if *t.CGO {
				cgo = "on"
			} else {
				cgo = "off"
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.Triple, t.Arch, t.OS, cgo)
	}
	flushTabular(w)
}

// runFeatures prints the declared features plus the default set.
func runFeatures(args []string, flags cliFlags) {
	fs := flag.NewFlagSet("features", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty features")
	}
	_ = fs.Parse(args)
	cfg := loadProfileConfig(flags)
	if len(cfg.Features) == 0 && len(cfg.DefaultFeatures) == 0 {
		fmt.Println("no [features] table declared in osty.toml")
		return
	}
	if len(cfg.DefaultFeatures) > 0 {
		fmt.Printf("default = [%s]\n", strings.Join(cfg.DefaultFeatures, ", "))
	}
	for _, name := range cfg.FeatureNames() {
		items := cfg.Features[name]
		fmt.Printf("%s = [%s]\n", name, strings.Join(items, ", "))
	}
}

// runCache dispatches `osty cache <subcommand>`: ls (default), clean,
// info. Each lists / mutates entries under <project>/.osty/cache/.
func runCache(args []string, flags cliFlags) {
	sub := "ls"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "ls":
		runCacheLs(args, flags)
	case "clean":
		runCacheClean(args, flags)
	case "info":
		runCacheInfo(args, flags)
	default:
		fmt.Fprintf(os.Stderr, "osty cache: unknown subcommand %q\n", sub)
		fmt.Fprintln(os.Stderr, "usage: osty cache [ls|clean|info]")
		os.Exit(2)
	}
}

func runCacheLs(_ []string, flags cliFlags) {
	root := projectRootOrExit(flags)
	entries, err := profile.ListCache(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty cache: %v\n", err)
		os.Exit(1)
	}
	if len(entries) == 0 {
		fmt.Println("cache is empty (never run `osty build` in this project)")
		return
	}
	w := tabular(os.Stdout)
	fmt.Fprintln(w, "PROFILE\tTARGET\tBACKEND\tEMIT\tSOURCES\tSIZE\tBUILT")
	for _, e := range entries {
		target := e.Target
		if target == "" {
			target = "(host)"
		}
		emit := e.Emit
		if emit == "" {
			emit = "(unknown)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			e.Profile, target, e.Backend, emit, e.Sources, humanBytes(e.Size),
			e.BuiltAt.Local().Format("2006-01-02 15:04"))
	}
	flushTabular(w)
}

func runCacheClean(_ []string, flags cliFlags) {
	root := projectRootOrExit(flags)
	total, err := profile.CleanCache(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty cache clean: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("cleaned %s of build artifacts under %s\n", humanBytes(total),
		filepath.Join(".osty"))
}

func runCacheInfo(args []string, flags cliFlags) {
	fs := flag.NewFlagSet("cache info", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty cache info [--profile NAME] [--target TRIPLE] [--backend NAME]")
	}
	var profileName, triple, backendName string
	fs.StringVar(&profileName, "profile", profile.NameDebug, "profile name")
	fs.StringVar(&triple, "target", "", "target triple (empty = host)")
	fs.StringVar(&backendName, "backend", defaultBackendName(), "backend name")
	_ = fs.Parse(args)
	backendID, err := parseCLIBackend(backendName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty cache info: %v\n", err)
		os.Exit(2)
	}
	root := projectRootOrExit(flags)
	fp, err := profile.ReadFingerprintForBackend(root, profileName, triple, backendID.String())
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty cache info: %v\n", err)
		os.Exit(1)
	}
	if fp == nil {
		fmt.Printf("no cache entry for profile=%s target=%q backend=%s\n",
			profileName, triple, backendID)
		os.Exit(1)
	}
	fmt.Printf("profile:      %s\n", fp.Profile)
	if fp.Target != "" {
		fmt.Printf("target:       %s\n", fp.Target)
	}
	if fp.Backend != "" {
		fmt.Printf("backend:      %s\n", fp.Backend)
	}
	if fp.Emit != "" {
		fmt.Printf("emit:         %s\n", fp.Emit)
	}
	fmt.Printf("tool version: %s\n", fp.ToolVersion)
	fmt.Printf("built at:     %s\n", fp.BuiltAt.Local().Format(time.RFC3339))
	fmt.Printf("sources:      %d files\n", len(fp.Sources))
	if len(fp.Features) > 0 {
		fmt.Printf("features:     %s\n", strings.Join(fp.Features, ", "))
	}
	// Sort for stable output.
	paths := make([]string, 0, len(fp.Sources))
	for p := range fp.Sources {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		fmt.Printf("  %s  %s\n", fp.Sources[p][:12], p)
	}
}

// loadProfileConfig is the shared helper: finds osty.toml (or falls
// back to built-in defaults when outside a project) and merges it
// with profile.Defaults(). Unlike the build-path helpers this one
// never prints manifest diagnostics — `osty profiles` is a read-only
// informational command that should work even with a broken
// manifest by falling back to built-ins.
func loadProfileConfig(_ cliFlags) *profile.Config {
	root, err := manifest.FindRoot(".")
	if err != nil {
		return profile.Defaults()
	}
	m, _, rerr := manifest.Load(filepath.Join(root, manifest.ManifestFile))
	if rerr != nil || m == nil {
		return profile.Defaults()
	}
	cfg, err := profile.BuildConfig(m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty: %v\n", err)
		os.Exit(1)
	}
	return cfg
}

// projectRootOrExit finds osty.toml relative to cwd and exits with a
// usage error when there isn't one. Used by `osty cache` subcommands
// that need a project to operate against.
func projectRootOrExit(flags cliFlags) string {
	m, root, abort := loadManifestWithDiag(".", flags)
	_ = m
	if abort {
		os.Exit(2)
	}
	return root
}

// tabular / flushTabular wrap text/tabwriter so the CLI's columnar
// listings stay aligned without every caller repeating the ritual of
// NewWriter + Flush. The column writer aligns on tabs; callers
// separate fields with '\t'.
func tabular(w *os.File) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
}

func flushTabular(w *tabwriter.Writer) {
	_ = w.Flush()
}

// humanBytes renders a byte count in KB / MB / GB, whichever fits.
// Used by cache ls/clean output.
func humanBytes(n int64) string {
	const kb = 1024
	if n < kb {
		return fmt.Sprintf("%d B", n)
	}
	if n < kb*kb {
		return fmt.Sprintf("%.1f KB", float64(n)/kb)
	}
	if n < kb*kb*kb {
		return fmt.Sprintf("%.1f MB", float64(n)/(kb*kb))
	}
	return fmt.Sprintf("%.2f GB", float64(n)/(kb*kb*kb))
}
