package backend

import (
	"sync"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// stdlibCheckCache memoizes check.Result per stdlib module so the
// (potentially 20–30 s) cold checker pass runs once per module for the
// lifetime of a compiler process. The cache is keyed by stdlib.Module
// pointer, not module name — a freshly cloned Registry is a distinct
// key, which matches the lifetime of a typical compile-a-program run
// where `stdlib.LoadCached()` returns a shared immutable content.
var stdlibCheckCache sync.Map // map[*stdlib.Module]*checkCacheEntry

type checkCacheEntry struct {
	once   sync.Once
	result *check.Result
}

// stdlibCheckResult returns the check.Result for a stdlib module, lazily
// running the selfhost checker the first time it is requested. Returns
// nil when the module or its parsed file is absent — the caller passes
// that nil straight through to ir.LowerFnDecl, which degrades to
// ErrTypeVal the same way it does for any missing checker output.
//
// Uses check.SelfhostFile instead of check.File so stdlib compilation
// skips the AST-level builder-desugar pass: stdlib sources are first-
// party and don't exercise the user-facing `.builder()` / `.build()`
// auto-derive chains that desugar targets. Shaves a linear walk per
// module off the cold path; more importantly, makes the stdlib side of
// the Phase 1c.4 migration the first production site to bypass the
// check.File "everything" wrapper.
//
// Concurrency: sync.Once guarantees at-most-once checker invocation per
// module even if multiple PrepareEntry calls race.
func stdlibCheckResult(reg *stdlib.Registry, module string) *check.Result {
	if reg == nil {
		return nil
	}
	mod, ok := reg.Modules[module]
	if !ok || mod == nil || mod.File == nil || mod.Package == nil || len(mod.Package.Files) == 0 {
		return nil
	}
	entryAny, _ := stdlibCheckCache.LoadOrStore(mod, &checkCacheEntry{})
	entry := entryAny.(*checkCacheEntry)
	entry.once.Do(func() {
		pf := mod.Package.Files[0]
		rr := &resolve.Result{
			RefsByID:      pf.RefsByID,
			TypeRefsByID:  pf.TypeRefsByID,
			RefIdents:     pf.RefIdents,
			TypeRefIdents: pf.TypeRefIdents,
			FileScope:     pf.FileScope,
		}
		// Source bytes are required — the selfhost checker reads them
		// to drive its parse + type inference pipeline. Without them it
		// returns a "checker unavailable" diagnostic and leaves Types
		// empty. Stdlib modules self-reference other stdlib modules
		// (`use std.*`), so the registry itself supplies the provider.
		entry.result = check.SelfhostFile(mod.File, rr, check.Opts{
			Source: mod.Source,
			Stdlib: reg,
		})
	})
	return entry.result
}
