package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/osty/osty/internal/toolchain/selfhostcache"
)

// runGCSelf prunes stale entries from `.osty/cache/self-host/` so a
// long-lived dev workspace does not accumulate gigabytes of
// historical osty-self builds. Default policy keeps every entry
// matching the current toolchain SHA plus the
// `selfhostcache.DefaultGCKeep` most recently modified entries.
//
// Flags:
//
//	--keep N              keep the N most recently modified entries
//	                      among those that don't match the current
//	                      toolchain SHA. Negative ⇒ unlimited.
//	--older-than DUR      remove entries with mtime older than DUR
//	                      (e.g. "168h", "30m"). Combines with --keep:
//	                      an entry is only pruned via the LRU branch
//	                      if it is also older than DUR.
//	--dry-run             print the plan without touching disk.
func runGCSelf(args []string, _ cliFlags) {
	fs := flag.NewFlagSet("gc-self", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, gcSelfUsage())
		fs.PrintDefaults()
	}
	var (
		keep      int
		olderRaw  string
		dryRun    bool
		quiet     bool
		olderThan time.Duration
	)
	fs.IntVar(&keep, "keep", selfhostcache.DefaultGCKeep, "keep N most recent non-current entries; negative = unlimited")
	fs.StringVar(&olderRaw, "older-than", "", "remove entries older than this Go duration (e.g. 168h, 30m)")
	fs.BoolVar(&dryRun, "dry-run", false, "print the plan without removing anything")
	fs.BoolVar(&quiet, "quiet", false, "suppress per-entry log lines on success")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if olderRaw != "" {
		d, err := time.ParseDuration(olderRaw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty gc-self: invalid --older-than %q: %v\n", olderRaw, err)
			os.Exit(2)
		}
		if d < 0 {
			fmt.Fprintln(os.Stderr, "osty gc-self: --older-than must be non-negative")
			os.Exit(2)
		}
		olderThan = d
	}

	root, err := selfhostcache.LocateProjectRoot(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty gc-self: locate project root: %v\n", err)
		os.Exit(1)
	}

	result, err := selfhostcache.RunGC(root, selfhostcache.GCOptions{
		Keep:      keep,
		OlderThan: olderThan,
		DryRun:    dryRun,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty gc-self: %v\n", err)
		os.Exit(1)
	}

	header := "removed:"
	if dryRun {
		header = "would remove:"
	}
	if !quiet {
		for _, e := range result.Removed {
			fmt.Printf("%s   %s  %s  %s\n", header, e.Key, formatBytes(e.Size), e.ModTime.Format(time.RFC3339))
		}
		for _, e := range result.Kept {
			marker := "kept:"
			if e.CurrentSHA {
				marker = "kept(current):"
			}
			fmt.Printf("%-15s %s  %s  %s\n", marker, e.Key, formatBytes(e.Size), e.ModTime.Format(time.RFC3339))
		}
	}
	verb := "freed"
	if dryRun {
		verb = "would free"
	}
	fmt.Printf("summary: %s, kept %d entries (%s)\n", verb+" "+formatBytes(result.BytesRemoved), len(result.Kept), pluralEntry(len(result.Removed), "removable"))
}

// formatBytes renders byte counts in the same compact form `du -h`
// uses. Keeps gc output readable when historical entries pile up.
func formatBytes(n int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case n >= GB:
		return fmt.Sprintf("%.1fGB", float64(n)/GB)
	case n >= MB:
		return fmt.Sprintf("%.1fMB", float64(n)/MB)
	case n >= KB:
		return fmt.Sprintf("%.1fKB", float64(n)/KB)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func pluralEntry(n int, label string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s entry", label)
	}
	return fmt.Sprintf("%d %s entries", n, label)
}

func gcSelfUsage() string {
	return strings.TrimSpace(`
osty gc-self [--keep N] [--older-than DUR] [--dry-run] [--quiet]
    Prune stale entries from .osty/cache/self-host/. Default policy
    keeps every entry matching the current toolchain SHA plus the
    5 most recently modified other entries. Use --dry-run to preview
    the plan before letting the GC delete anything.
`)
}
