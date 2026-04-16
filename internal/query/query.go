package query

import (
	"bytes"

	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// ---- Frame ----

// frame is one entry on the execution stack. Exactly one frame per
// actively-executing query body; while a body runs, every child
// [Query.Fetch] / [Input.Fetch] call records a dep edge onto the top
// frame's deps slice.
type frame struct {
	qid  QueryID
	key  any
	deps []depRecord
}

// currentFrame returns the top of the stack or nil if empty. Must be
// called under db.mu.
func (db *Database) currentFrame() *frame {
	if n := len(db.stack); n > 0 {
		return db.stack[n-1]
	}
	return nil
}

// pushFrame / popFrame are internal helpers; must be called under db.mu.
func (db *Database) pushFrame(f *frame) { db.stack = append(db.stack, f) }

func (db *Database) popFrame() *frame {
	n := len(db.stack)
	f := db.stack[n-1]
	db.stack = db.stack[:n-1]
	return f
}

// ---- Ctx ----

// Ctx is handed to a query body while it executes. It exposes the
// Database-level constants (prelude, stdlib) and is the token required
// to call [Query.Fetch] / [Input.Fetch] — since Ctx can only be
// obtained from within a body, this prevents callers from bypassing
// the Database mutex by calling the non-locking Fetch path.
type Ctx struct {
	db *Database
}

// Prelude returns the process-lifetime prelude scope. Not dependency-
// tracked — it's a Database construction-time constant.
func (c *Ctx) Prelude() *resolve.Scope { return c.db.prelude }

// Stdlib returns the process-lifetime stdlib registry. Not dependency-
// tracked for the same reason as Prelude.
func (c *Ctx) Stdlib() *stdlib.Registry { return c.db.stdlib }

// ---- QueryFn, Query, Input ----

// QueryFn is the body of a derived query. It receives the current
// [Ctx] and the query-specific key, and returns the computed value.
// The body may call [Query.Fetch] / [Input.Fetch] to read other
// queries and inputs; those reads are tracked automatically.
type QueryFn[K comparable, V any] func(*Ctx, K) V

// Query is a registered derived query. Construct with [Register].
type Query[K comparable, V any] struct {
	id     QueryID
	name   string
	fn     QueryFn[K, V]
	hashFn func(V) [32]byte
}

// ID reports the engine-internal identifier. Stable within one
// Database instance; not meaningful across processes.
func (q *Query[K, V]) ID() QueryID { return q.id }

// Name returns the human-readable label supplied at registration.
func (q *Query[K, V]) Name() string { return q.name }

// Input is a registered input. Construct with [RegisterInput]. Inputs
// differ from derived queries in two ways: they are externally
// [Input.Set], and their hashFn is mandatory (used to decide whether
// a Set actually bumps the revision).
type Input[K comparable, V any] struct {
	q      *Query[K, V] // hosts the slot machinery; fn is unused for inputs
	hashFn func(V) [32]byte
}

// ID forwards the underlying QueryID.
func (i *Input[K, V]) ID() QueryID { return i.q.id }

// ---- Registration ----

// Register adds a derived query to db. The optional hashFn enables
// early cutoff: when a re-run produces a value whose hash matches the
// cached value's hash, the slot's computedAt is left untouched so
// downstream queries that read this one can reuse their own caches.
// Pass nil hashFn to disable early cutoff (all re-runs bump
// computedAt).
func Register[K comparable, V any](
	db *Database,
	name string,
	fn QueryFn[K, V],
	hashFn func(V) [32]byte,
) *Query[K, V] {
	id := db.allocID(name, false)
	q := &Query[K, V]{id: id, name: name, fn: fn, hashFn: hashFn}
	db.mu.Lock()
	if db.validators == nil {
		db.validators = make(map[QueryID]validator)
	}
	db.validators[id] = func(d *Database, key any) *slot {
		k, ok := key.(K)
		if !ok {
			return nil
		}
		return q.validateOrRunLocked(d, k)
	}
	db.mu.Unlock()
	return q
}

// RegisterInput adds an input to db. The hashFn must be non-nil and
// will decide whether [Input.Set] bumps the revision (a Set with a
// value whose hash matches the current cached hash is a no-op).
func RegisterInput[K comparable, V any](
	db *Database,
	name string,
	hashFn func(V) [32]byte,
) *Input[K, V] {
	if hashFn == nil {
		panic("query: input hashFn must be non-nil")
	}
	id := db.allocID(name, true)
	q := &Query[K, V]{id: id, name: name, hashFn: hashFn}
	// Inputs do not need a validator: their slots are only written
	// via Input.Set and read via the derived dep-walk, which reads
	// the slot directly.
	db.mu.Lock()
	if db.validators == nil {
		db.validators = make(map[QueryID]validator)
	}
	db.validators[id] = func(d *Database, key any) *slot {
		// Input "validation" is trivial: whatever is in the slot
		// table is the truth; Set bumps rev when value actually
		// changes, so no recomputation needed here.
		m := d.slots[id]
		if m == nil {
			return nil
		}
		return m[key]
	}
	db.mu.Unlock()
	return &Input[K, V]{q: q, hashFn: hashFn}
}

// ---- validator dispatch ----

// validator is the type-erased entry point to a query's validate-or-run
// path. Every Register stores one; validateDepLocked dispatches via
// this table during dep-walks.
type validator func(*Database, any) *slot

// validateDepLocked invokes the validator for dep d. Used by
// validateOrRunLocked when walking a slot's recorded deps. Must be
// called under db.mu.
func (db *Database) validateDepLocked(d depRecord) *slot {
	v := db.validators[d.qid]
	if v == nil {
		return nil
	}
	return v(db, d.key)
}

// ---- Query.Get / Query.Fetch ----

// Get is the user-facing entry point. Acquires db.mu, validates or
// runs the slot for key, records a dep edge on the caller's frame if
// one is active, and returns the value.
//
// Body code inside another query must use [Query.Fetch] instead — Get
// would try to re-acquire the mutex and deadlock.
func (q *Query[K, V]) Get(db *Database, key K) V {
	db.mu.Lock()
	defer db.mu.Unlock()
	return q.getLocked(db, key)
}

// Fetch is the body-facing entry point. The [Ctx] is the witness that
// db.mu is already held; Fetch performs the same logic as Get without
// re-locking.
func (q *Query[K, V]) Fetch(ctx *Ctx, key K) V {
	return q.getLocked(ctx.db, key)
}

func (q *Query[K, V]) getLocked(db *Database, key K) V {
	// Capture parent BEFORE any recursion so dep edges are attributed
	// correctly when this call was issued from a body.
	parent := db.currentFrame()

	if err := db.checkCycle(q.id, key); err != nil {
		panic(err)
	}

	s := q.validateOrRunLocked(db, key)

	if parent != nil {
		parent.deps = append(parent.deps, depRecord{
			qid:       q.id,
			key:       key,
			changedAt: s.computedAt,
		})
	}

	return s.value.(V)
}

// validateOrRunLocked is the heart of the engine. Returns the slot
// for (q, key) guaranteed to be verified at db.rev. Must be called
// under db.mu.
func (q *Query[K, V]) validateOrRunLocked(db *Database, key K) *slot {
	m := db.slots[q.id]
	s := m[any(key)]

	if s == nil {
		// Cold slot — run fresh. Accounts as miss.
		return q.runAndStoreLocked(db, key, nil)
	}

	if s.verifiedAt == db.rev {
		db.metrics.hits.Add(1)
		return s
	}

	// Walk deps. If any dep's computedAt has advanced beyond what we
	// recorded, this slot is stale.
	stale := false
	for _, d := range s.deps {
		depSlot := db.validateDepLocked(d)
		if depSlot == nil || depSlot.computedAt > d.changedAt {
			stale = true
			break
		}
	}

	if !stale {
		// All deps still valid — bump verifiedAt, keep value.
		s.verifiedAt = db.rev
		db.metrics.hits.Add(1)
		return s
	}

	return q.runAndStoreLocked(db, key, s)
}

// runAndStoreLocked executes the body, hashes the result, and writes
// a new slot. If oldSlot is non-nil and the hashFn matches, early
// cutoff applies: the new slot's computedAt is inherited from the old
// slot, sparing dependents a cascade. Must be called under db.mu.
func (q *Query[K, V]) runAndStoreLocked(db *Database, key K, oldSlot *slot) *slot {
	isInput := db.isInput[q.id]
	if isInput {
		// Inputs shouldn't reach here: their slots are created by
		// Input.Set and validateDepLocked only reads them. A rerun
		// attempt for a missing input slot is a programmer error.
		panic("query: input " + db.names[q.id] + " has no slot (did you forget to Set it?)")
	}

	f := &frame{qid: q.id, key: key}
	db.pushFrame(f)
	value := q.fn(&Ctx{db: db}, key)
	popped := db.popFrame()
	_ = popped // == f

	var newHash [32]byte
	hasHash := q.hashFn != nil
	if hasHash {
		newHash = q.hashFn(value)
	}

	computedAt := db.rev
	cutoff := false
	if oldSlot != nil && hasHash && oldSlot.hasHash && bytes.Equal(newHash[:], oldSlot.outputHash[:]) {
		// Early cutoff: value is semantically identical.
		computedAt = oldSlot.computedAt
		cutoff = true
	}

	newSlot := &slot{
		value:      value,
		outputHash: newHash,
		hasHash:    hasHash,
		computedAt: computedAt,
		verifiedAt: db.rev,
		deps:       f.deps,
	}

	if db.slots[q.id] == nil {
		db.slots[q.id] = make(map[any]*slot)
	}
	db.slots[q.id][any(key)] = newSlot

	if oldSlot == nil {
		db.metrics.misses.Add(1)
	} else if cutoff {
		db.metrics.cutoffs.Add(1)
	} else {
		db.metrics.reruns.Add(1)
	}

	return newSlot
}

// ---- Input ----

// Set writes val into the input slot for key. If val's hash matches
// the existing slot's hash, this is a no-op — no revision bump, no
// invalidation cascade. Otherwise the revision is bumped and the slot
// is updated; downstream queries will notice on their next Get.
func (i *Input[K, V]) Set(db *Database, key K, val V) {
	db.mu.Lock()
	defer db.mu.Unlock()

	newHash := i.hashFn(val)
	m := db.slots[i.q.id]
	if m == nil {
		m = make(map[any]*slot)
		db.slots[i.q.id] = m
	}
	if old, ok := m[any(key)]; ok && old.hasHash && bytes.Equal(newHash[:], old.outputHash[:]) {
		// Same content — don't bump anything.
		return
	}

	db.rev++
	m[any(key)] = &slot{
		value:      val,
		outputHash: newHash,
		hasHash:    true,
		computedAt: db.rev,
		verifiedAt: db.rev,
	}
	db.metrics.inputSet.Add(1)
}

// Clear removes the slot for key. Bumps the revision unconditionally
// since anything that read the input needs to re-check. Callers that
// might re-Set the same value after clearing should just call Set —
// content-hash cutoff avoids the cascade there.
func (i *Input[K, V]) Clear(db *Database, key K) {
	db.mu.Lock()
	defer db.mu.Unlock()
	m := db.slots[i.q.id]
	if m == nil {
		return
	}
	if _, ok := m[any(key)]; !ok {
		return
	}
	delete(m, any(key))
	db.rev++
}

// Get returns the current value for key. Panics if the key has never
// been Set — inputs have no default; callers must seed them before
// any dependent query is pulled.
func (i *Input[K, V]) Get(db *Database, key K) V {
	db.mu.Lock()
	defer db.mu.Unlock()
	return i.getLocked(db, key)
}

// Fetch is the body-facing counterpart to Get. Records a dep edge on
// the currently-executing frame.
func (i *Input[K, V]) Fetch(ctx *Ctx, key K) V {
	return i.getLocked(ctx.db, key)
}

func (i *Input[K, V]) getLocked(db *Database, key K) V {
	parent := db.currentFrame()
	m := db.slots[i.q.id]
	s, ok := m[any(key)]
	if !ok {
		panic("query: input " + db.names[i.q.id] + " has no value set for key")
	}
	if parent != nil {
		parent.deps = append(parent.deps, depRecord{
			qid:       i.q.id,
			key:       key,
			changedAt: s.computedAt,
		})
	}
	db.metrics.hits.Add(1)
	return s.value.(V)
}

// Has reports whether key has been Set. Never records a dep edge
// (checking existence is orthogonal to reading the value). Exposed
// primarily for LSP seeding logic.
func (i *Input[K, V]) Has(db *Database, key K) bool {
	db.mu.Lock()
	defer db.mu.Unlock()
	_, ok := db.slots[i.q.id][any(key)]
	return ok
}
