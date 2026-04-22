package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/toolchain"
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
// -rf .osty/cache/checker/`). The validity segment binds entries to a
// specific checker binary so upgrading the managed checker (new
// `.osty/toolchain/<ver>/osty-native-checker`) transparently
// invalidates every record without requiring a manual purge.
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
	validity := checkerCacheValidity()
	check.UseCachedDefaultNativeChecker(cacheDir, validity)
}

// checkerCacheValidity returns a stable identifier for the current
// checker binary. The cache invalidates whenever this changes, so the
// implementation must capture every way the checker's behavior can
// shift: binary content (via path + size + mtime), or — when the
// managed checker isn't present — the embedded selfhost checker's
// version.
//
// We compute lazily and memoize: the validity only depends on process-
// lifetime immutables (tool version, managed checker path) so a
// single cache dir name suffices per run.
func checkerCacheValidity() string {
	validityOnce.Do(func() {
		h := sha256.New()
		fmt.Fprintf(h, "tool=%s\n", toolVersion())
		// Managed-checker binary stamp: path + size + mtime. If the
		// binary can't be located (dev host without it, or shutdown
		// race), the hash still carries tool version so entries are
		// validity-scoped within the process.
		if path, err := toolchain.EnsureNativeChecker("."); err == nil {
			if info, err := os.Stat(path); err == nil {
				fmt.Fprintf(h, "checker=%s size=%d mtime=%d\n",
					path, info.Size(), info.ModTime().UnixNano())
			}
		}
		validity = hex.EncodeToString(h.Sum(nil))[:16]
	})
	return validity
}

var (
	validityOnce sync.Once
	validity     string
)
