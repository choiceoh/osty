package selfhostcache

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultGCKeep is the number of most-recently-modified cache
// entries the GC keeps when no `--keep` is supplied. Picked small
// enough that cache footprint stays bounded but large enough to
// preserve a short rollback window — typical workflows iterate on
// ~3 toolchain versions during a refactor.
const DefaultGCKeep = 5

// Entry describes one cached osty-self artifact. The GC enumerates
// these directly off `.osty/cache/self-host/` so the resolver
// implementation doesn't have to grow new public API every time the
// GC adds another policy dimension.
type Entry struct {
	// Key is the cache subdirectory name `<sha>-<triple>`. May not
	// parse cleanly into a `selfhostcache.Key` if the directory was
	// hand-edited; GC treats unparseable entries as candidates for
	// removal so cruft does not accumulate.
	Key string
	// Path is the absolute directory holding `osty-self`.
	Path string
	// ModTime is the binary's mtime. `Install` sets this when the
	// entry is promoted, so for normal use it tracks insertion order
	// closely enough to drive an LRU-by-mtime policy.
	ModTime time.Time
	// Size is the binary size in bytes. Useful for `--report`
	// output and for size-based caps in future passes.
	Size int64
	// CurrentSHA is true when the entry's SHA matches the current
	// `ComputeKey(projectRoot)` toolchain hash. The default GC
	// policy always keeps these regardless of mtime so a `gc` after
	// a fresh `install-self` never throws away the just-built entry.
	CurrentSHA bool
}

// GCOptions tunes the GC policy. All fields zero-valued mean
// "default policy": keep every entry whose SHA matches the current
// toolchain, plus the `DefaultGCKeep` most recently modified
// entries among the rest.
type GCOptions struct {
	// Keep overrides DefaultGCKeep. A negative value means "no
	// numeric cap" — only OlderThan applies.
	Keep int
	// OlderThan, when non-zero, marks any entry with mtime older
	// than `now - OlderThan` for removal regardless of LRU position.
	// Combines with Keep: an entry is removed only if both criteria
	// would remove it.
	OlderThan time.Duration
	// DryRun, when true, returns the plan without touching disk.
	DryRun bool
	// Now overrides the wall-clock used for OlderThan comparisons;
	// zero means `time.Now()`. Tests inject deterministic clocks.
	Now time.Time
}

// GCResult summarises one GC pass.
type GCResult struct {
	Kept    []Entry
	Removed []Entry
	// BytesRemoved is the total binary size released. Computed from
	// `Entry.Size` so the value is meaningful in DryRun mode too.
	BytesRemoved int64
}

// ListEntries enumerates every `<sha>-<triple>` directory under the
// cache root. Directories that do not contain a usable `osty-self`
// binary at the canonical path are still returned (with `Size=0`)
// so the GC can prune partial / abandoned entries left by
// interrupted `Install` calls.
func ListEntries(projectRoot string) ([]Entry, error) {
	cacheRoot := filepath.Join(projectRoot, CacheDirName)
	dir, err := os.Open(cacheRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("selfhostcache: open cache: %w", err)
	}
	defer dir.Close()

	names, err := dir.Readdirnames(-1)
	if err != nil {
		return nil, fmt.Errorf("selfhostcache: readdir: %w", err)
	}

	currentKey, _ := ComputeKey(projectRoot) // best-effort; empty SHA ⇒ no current entries
	currentSHA := currentKey.ToolchainSHA

	out := make([]Entry, 0, len(names))
	for _, name := range names {
		entryPath := filepath.Join(cacheRoot, name)
		info, err := os.Stat(entryPath)
		if err != nil || !info.IsDir() {
			continue
		}
		bin := filepath.Join(entryPath, BinaryName())
		var size int64
		var mtime time.Time
		if binInfo, err := os.Stat(bin); err == nil && !binInfo.IsDir() {
			size = binInfo.Size()
			mtime = binInfo.ModTime()
		} else {
			// Fall back to the directory mtime so partial entries
			// still get an ordering signal.
			mtime = info.ModTime()
		}
		entry := Entry{
			Key:     name,
			Path:    entryPath,
			ModTime: mtime,
			Size:    size,
		}
		if currentSHA != "" && strings.HasPrefix(name, currentSHA+"-") {
			entry.CurrentSHA = true
		}
		out = append(out, entry)
	}
	return out, nil
}

// Plan computes which entries the GC would keep vs. remove without
// touching disk. Useful for `--dry-run` and for tests that want to
// assert ordering without inspecting the filesystem afterwards.
func Plan(projectRoot string, opts GCOptions) (GCResult, error) {
	entries, err := ListEntries(projectRoot)
	if err != nil {
		return GCResult{}, err
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	keep := opts.Keep
	if keep == 0 {
		keep = DefaultGCKeep
	}
	// Sort by mtime descending so the "newest first" slice doubles
	// as the LRU candidate ordering.
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].ModTime.After(entries[j].ModTime)
	})

	var result GCResult
	keptCount := 0
	for _, e := range entries {
		// 1. Always keep entries matching the current toolchain SHA.
		//    Protects a freshly built artifact from being pruned by
		//    a tight `--keep=0` request.
		if e.CurrentSHA {
			result.Kept = append(result.Kept, e)
			continue
		}
		// 2. Numeric LRU cap. Negative `keep` means "unlimited".
		if keep >= 0 && keptCount >= keep {
			if olderThanCutoff(e, opts.OlderThan, now) {
				result.Removed = append(result.Removed, e)
				result.BytesRemoved += e.Size
				continue
			}
		}
		// 3. OlderThan-only path: even within the LRU cap an entry
		//    older than the cutoff is removed when the option is set.
		if opts.OlderThan > 0 && now.Sub(e.ModTime) > opts.OlderThan {
			result.Removed = append(result.Removed, e)
			result.BytesRemoved += e.Size
			continue
		}
		result.Kept = append(result.Kept, e)
		keptCount++
	}
	return result, nil
}

// olderThanCutoff returns true when no `--older-than` is set OR the
// entry is older than the cutoff. The negation pattern lets the
// caller AND the LRU cap with the OlderThan check: an entry is only
// removed via the LRU branch if both criteria agree.
func olderThanCutoff(e Entry, olderThan time.Duration, now time.Time) bool {
	if olderThan <= 0 {
		return true
	}
	return now.Sub(e.ModTime) > olderThan
}

// RunGC applies the policy. On `DryRun=true` the result is computed
// but no entries are removed. The returned `GCResult.Removed` lists
// entries that *were* (or *would be*) removed, in the same order as
// `Plan` would have reported them.
func RunGC(projectRoot string, opts GCOptions) (GCResult, error) {
	plan, err := Plan(projectRoot, opts)
	if err != nil {
		return GCResult{}, err
	}
	if opts.DryRun {
		return plan, nil
	}
	for _, e := range plan.Removed {
		if err := os.RemoveAll(e.Path); err != nil {
			return plan, fmt.Errorf("selfhostcache: remove %s: %w", e.Path, err)
		}
	}
	return plan, nil
}
