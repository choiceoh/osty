package query

// slot is the cached record for one (QueryID, key) pair.
//
// The three revision numbers encode the Salsa invariant:
//
//   - computedAt: the revision at which value was last produced. A
//     downstream query decides "my dep changed" by comparing its
//     recorded depRecord.changedAt against the dep's current
//     computedAt.
//   - verifiedAt: the revision at which we last confirmed that every
//     dep was still valid. Advances without running the query body
//     whenever the dep-walk finds no dep stale.
//   - deps: the exact edges captured when the body last ran.
//
// Early cutoff: when re-running the body produces a value whose hash
// matches the stored outputHash, we leave computedAt alone — only
// verifiedAt advances. Dependents that ask "did my dep's computedAt
// move?" answer no, and cascade stops.
type slot struct {
	value      any
	outputHash [32]byte // zero if the query has no hashFn
	hasHash    bool
	computedAt Revision
	verifiedAt Revision
	deps       []depRecord
}

// depRecord is one edge captured when a query body read a value. The
// recorded changedAt is the dep slot's computedAt at the moment of
// capture — we detect staleness by comparing it against the dep slot's
// current computedAt, not its verifiedAt.
type depRecord struct {
	qid       QueryID
	key       any
	changedAt Revision
}
