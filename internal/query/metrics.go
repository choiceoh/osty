package query

import "sync/atomic"

// Metrics is the Database-wide counter set tracked across all
// [Query.Get] and [Input.Set] calls. Counters are monotonic and
// thread-safe. The expected use is assertive: tests snapshot before a
// scripted sequence and after, then compare the delta against the
// expected cache effect ("this edit should cause exactly one rerun
// and N-1 validations").
type Metrics struct {
	hits     atomic.Uint64 // cached value returned without re-running
	misses   atomic.Uint64 // slot absent, executed fresh
	reruns   atomic.Uint64 // slot present but stale, re-executed with value change
	cutoffs  atomic.Uint64 // re-executed but output hash unchanged — downstream spared
	inputSet atomic.Uint64 // Input.Set calls that actually bumped the revision
}

// MetricsSnapshot is a point-in-time copy of the counters.
type MetricsSnapshot struct {
	Hits     uint64
	Misses   uint64
	Reruns   uint64
	Cutoffs  uint64
	InputSet uint64
}

// Snapshot copies all counters atomically-per-field. Field-level
// tearing is harmless (tests compare deltas, not absolute totals).
func (m *Metrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		Hits:     m.hits.Load(),
		Misses:   m.misses.Load(),
		Reruns:   m.reruns.Load(),
		Cutoffs:  m.cutoffs.Load(),
		InputSet: m.inputSet.Load(),
	}
}

// Sub returns the counter delta between two snapshots.
func (a MetricsSnapshot) Sub(b MetricsSnapshot) MetricsSnapshot {
	return MetricsSnapshot{
		Hits:     a.Hits - b.Hits,
		Misses:   a.Misses - b.Misses,
		Reruns:   a.Reruns - b.Reruns,
		Cutoffs:  a.Cutoffs - b.Cutoffs,
		InputSet: a.InputSet - b.InputSet,
	}
}
