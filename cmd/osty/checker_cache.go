package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/osty/osty/internal/check"
)

// enableCheckerCacheForRoot activates the on-disk native-checker cache
// scoped to the project at root. First-time `osty check` / `osty build`
// invocations pay the full checker cost; subsequent invocations with
// unchanged package inputs short-circuit to a JSON read — turning
// incremental edits from multi-second type-check passes into
// near-zero ones.
//
// The cache lives at `<root>/.osty/cache/checker/<validity>/` so it is
// trivially inspectable (`cat`, `ls`) and trivially invalidated (`rm
// -rf .osty/cache/checker/`). The validity segment binds entries to the
// embedded checker sources by default, so regenerating
// `internal/selfhost/generated.go` transparently invalidates every record
// without requiring a manual purge.
//
// No-op when OSTY_CHECKER_CACHE=0. Idempotent: safe to call multiple
// times in a process — once overrides take, subsequent calls just
// reset the factory with the same validity string.
func enableCheckerCacheForRoot(root string) {
	if root == "" {
		return
	}
	if v := os.Getenv("OSTY_CHECKER_CACHE"); v == "0" || v == "false" {
		return
	}
	cacheDir := filepath.Join(root, ".osty", "cache", "checker")
	validity := checkerCacheValidity(root)
	check.UseCachedDefaultNativeChecker(cacheDir, validity)
}

// checkerCacheValidity returns a stable identifier for the default checker
// backend. When the embedded selfhost checker sources are readable from root,
// they define the cache namespace; otherwise we fall back to a coarser tool
// version token so callers still get a deterministic cache directory.
//
// We compute lazily and memoize: the validity only depends on process-lifetime
// immutables for a single repo checkout, so one cache dir name suffices per run.
func checkerCacheValidity(root string) string {
	validityOnce.Do(func() {
		if fp := check.EmbeddedCheckerFingerprint(root); fp != "" {
			validity = fp
			return
		}
		h := sha256.New()
		fmt.Fprintf(h, "tool=%s\n", toolVersion())
		validity = hex.EncodeToString(h.Sum(nil))[:16]
	})
	return validity
}

var (
	validityOnce sync.Once
	validity     string
)
