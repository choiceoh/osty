// Package query implements a Salsa-style incremental query engine for
// pull-based, revision-tracked computation with early cutoff.
//
// The engine has three top-level abstractions:
//
//   - [Database] — the top-level holder. Owns the global revision
//     counter, the slot storage for every registered query, and the
//     execution stack used for dependency tracking and cycle detection.
//   - [Input] — a source of truth. Callers [Input.Set] values keyed by
//     something comparable; reads via [Input.Get] or via a downstream
//     query reading the same key.
//   - [Query] — a derived computation. Pure function of its inputs
//     plus other queries. Registered with a [QueryFn] and an optional
//     hashFn used for early cutoff.
//
// The engine is independent of the Osty compiler; callers build their
// query set on top via [Register] and [RegisterInput]. See
// [github.com/osty/osty/internal/query/osty] for the compiler-specific
// binding.
package query

import (
	"sync"

	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// Revision is a monotonically increasing counter stamped onto every
// slot. An input [Input.Set] call that observes a hash-different value
// bumps the revision; derived queries use revisions to decide whether
// their cached value is still valid without recomputing.
type Revision uint64

// QueryID is the engine's internal handle for a registered query or
// input. Values are assigned in registration order and are not
// meaningful across processes; they exist only to key slot storage and
// to identify frames on the execution stack.
type QueryID uint32

// Database is the root of a query universe. All queries registered
// against the same Database share one slot store, one revision
// counter, and one execution stack.
//
// The MVP uses a single big mutex. Every [Query.Get] and [Input.Set]
// call serializes through it. This matches the LSP's existing
// per-handler serialization and is correct by construction for
// dependency recording; per-slot locks can be added later without API
// changes.
type Database struct {
	mu sync.Mutex

	// rev is the current global revision. Bumps whenever an input's
	// Set observes a hash-different payload.
	rev Revision

	// slots[qid][key] is the cached record for one (query, key) pair.
	slots map[QueryID]map[any]*slot

	// names records a human-readable label per QueryID for error
	// messages and metrics.
	names map[QueryID]string

	// isInput records whether a QueryID was registered as an input.
	// Only inputs may be Set/Cleared.
	isInput map[QueryID]bool

	// validators is the type-erased dispatch table used during
	// dep-walks. Each Register / RegisterInput call installs one
	// entry keyed by the query's QueryID. See validator type in
	// query.go.
	validators map[QueryID]validator

	// nextID hands out QueryIDs in registration order.
	nextID QueryID

	// stack is the currently-running call chain. Each frame records
	// the deps observed while its query body executes. Used both for
	// dep-edge recording and for cycle detection.
	stack []*frame

	// metrics count hits / misses / reruns / cutoffs across the
	// Database lifetime. See [MetricsSnapshot].
	metrics Metrics

	// prelude and stdlib are process-lifetime singletons baked into
	// the Database at construction. Queries read them directly from
	// [Ctx.Prelude] / [Ctx.Stdlib] without recording a dep edge —
	// they never change.
	prelude *resolve.Scope
	stdlib  *stdlib.Registry
}

// NewDatabase constructs a Database with the given process-lifetime
// prelude and stdlib registry. Both may be nil for test harnesses that
// don't need them.
func NewDatabase(prelude *resolve.Scope, reg *stdlib.Registry) *Database {
	return &Database{
		slots:      make(map[QueryID]map[any]*slot),
		names:      make(map[QueryID]string),
		isInput:    make(map[QueryID]bool),
		validators: make(map[QueryID]validator),
		prelude:    prelude,
		stdlib:     reg,
	}
}

// Revision returns the current global revision. Mostly useful for
// tests asserting "an edit did (not) bump the revision".
func (db *Database) Revision() Revision {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.rev
}

// Metrics returns a snapshot of the hit/miss/rerun/cutoff counters.
func (db *Database) Metrics() MetricsSnapshot {
	return db.metrics.Snapshot()
}

// allocID reserves a new QueryID for a registration. Must be called
// under db.mu (or before any concurrent access, i.e. during setup).
func (db *Database) allocID(name string, input bool) QueryID {
	db.mu.Lock()
	defer db.mu.Unlock()
	id := db.nextID
	db.nextID++
	db.names[id] = name
	db.isInput[id] = input
	db.slots[id] = make(map[any]*slot)
	return id
}
